package fileapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// ShareResolver resolves a share name to its absolute filesystem root path.
// Engine.List + path lookup satisfies this naturally; test fakes use FixedShareResolver.
type ShareResolver interface {
	ResolveShareRoot(ctx context.Context, shareName string) (string, error)
}

// FixedShareResolver maps share names to pre-configured roots. For tests.
type FixedShareResolver struct {
	Roots map[string]string // shareName → absolute path
}

func (f *FixedShareResolver) ResolveShareRoot(_ context.Context, shareName string) (string, error) {
	root, ok := f.Roots[shareName]
	if !ok {
		return "", fmt.Errorf("fileapi: share %q not found", shareName)
	}
	return root, nil
}

// Handler is the http.Handler for all File API endpoints. Routes are:
//
//	GET  /api/files/{share}/{path...}   — download (Range supported)
//	PUT  /api/files/{share}/{path...}   — upload
//	DELETE /api/files/{share}/{path...} — delete
//	GET  /api/files/{share}             — list directory (or stat with ?stat=1)
//	POST /api/files/{share}/mkdir       — create directory (body: {"path":"..."})
//	POST /api/files/{share}/copy        — copy file (body: {"src":"...","dst":"..."})
//	POST /api/files/{share}/move        — move/rename (body: {"src":"...","dst":"..."})
//	GET  /api/files/{share}/{path...}?stat=1 — stat a single entry
//
// Authentication: X-Kura-Token header is required for all requests.
// The built-in filebrowser (/ui/files) uses a session-token bridge instead
// (internal package wires that separately with NoopValidator).
type Handler struct {
	backend   FileBackend
	validator TokenValidator
	scopes    ScopeStore
	shares    ShareResolver
	// useRealPaths controls whether safeJoinReal (symlink check) is used.
	// True in production, false in unit tests using MemBackend.
	useRealPaths bool
}

// HandlerConfig wires the File API handler.
type HandlerConfig struct {
	Backend      FileBackend
	Validator    TokenValidator
	Scopes       ScopeStore
	Shares       ShareResolver
	UseRealPaths bool
}

// NewHandler returns a new Handler. Routes() returns the http.Handler.
func NewHandler(cfg HandlerConfig) *Handler {
	return &Handler{
		backend:      cfg.Backend,
		validator:    cfg.Validator,
		scopes:       cfg.Scopes,
		shares:       cfg.Shares,
		useRealPaths: cfg.UseRealPaths,
	}
}

// Routes returns an http.Handler that dispatches the 8 File API endpoints.
// Mount at /api/files/ (note trailing slash).
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	// All routes under /api/files/{share}/...
	// Go 1.22 wildcard routing: {share} captures one segment.
	mux.HandleFunc("GET /api/files/{share}/mkdir", methodNotAllowed("POST"))
	mux.HandleFunc("POST /api/files/{share}/mkdir", h.handleMkdir)
	mux.HandleFunc("POST /api/files/{share}/copy", h.handleCopy)
	mux.HandleFunc("POST /api/files/{share}/move", h.handleMove)
	// Catch-all for /{share}/{path...} — dispatch by method.
	mux.HandleFunc("/api/files/{share}/{path...}", h.handlePath)
	// Bare /api/files/{share} — list root directory.
	mux.HandleFunc("/api/files/{share}", h.handleShareRoot)
	return mux
}

func methodNotAllowed(allow string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", allow)
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
	}
}

// handleShareRoot handles GET /api/files/{share} — list the share's root directory.
func (h *Handler) handleShareRoot(w http.ResponseWriter, r *http.Request) {
	claims, shareName, err := h.authAndScope(r, "")
	if err != nil {
		h.writeError(w, err)
		return
	}
	root, err := h.shares.ResolveShareRoot(r.Context(), shareName)
	if err != nil {
		h.writeError(w, err)
		return
	}
	_ = claims
	entries, err := h.backend.ReadDir(r.Context(), root)
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, listResponse{Share: shareName, Path: "/", Entries: entries})
}

// handlePath dispatches GET / PUT / DELETE on /api/files/{share}/{path...}
func (h *Handler) handlePath(w http.ResponseWriter, r *http.Request) {
	shareName := r.PathValue("share")
	relPath := "/" + r.PathValue("path")

	claims, _, err := h.authAndScope(r, relPath)
	if err != nil {
		h.writeError(w, err)
		return
	}
	_ = claims

	root, err := h.shares.ResolveShareRoot(r.Context(), shareName)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var absPath string
	if h.useRealPaths {
		absPath, err = safeJoinReal(root, relPath)
	} else {
		absPath, err = safeJoin(root, relPath)
	}
	if err != nil {
		http.Error(w, "forbidden: "+err.Error(), http.StatusForbidden)
		return
	}

	switch r.Method {
	case http.MethodGet:
		// ?stat=1 returns metadata only.
		if r.URL.Query().Get("stat") == "1" {
			h.handleStat(w, r, absPath)
			return
		}
		h.handleDownload(w, r, absPath)
	case http.MethodPut:
		h.handleUpload(w, r, absPath)
	case http.MethodDelete:
		h.handleDelete(w, r, absPath)
	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
	}
}

// handleDownload serves a file using http.ServeContent for Range support.
// [AC-S0eedaa-1-1] — GET
func (h *Handler) handleDownload(w http.ResponseWriter, r *http.Request, absPath string) {
	rsc, info, err := h.backend.Open(r.Context(), absPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rsc.Close()
	// http.ServeContent handles Range, ETag, Last-Modified.
	// [AC-S0eedaa-1-3] — Range request support
	http.ServeContent(w, r, info.Name, info.ModTime, rsc)
}

// handleStat returns JSON metadata for a single path.
// [AC-S0eedaa-1-1] — stat
func (h *Handler) handleStat(w http.ResponseWriter, r *http.Request, absPath string) {
	info, err := h.backend.Stat(r.Context(), absPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// handleUpload creates or overwrites a file.
// Supports multipart/form-data (field "file") and raw PUT body.
// [AC-S0eedaa-1-1] — PUT
func (h *Handler) handleUpload(w http.ResponseWriter, r *http.Request, absPath string) {
	var reader io.Reader
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			http.Error(w, "bad multipart: "+err.Error(), http.StatusBadRequest)
			return
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "missing 'file' field", http.StatusBadRequest)
			return
		}
		defer f.Close()
		reader = f
	} else {
		reader = r.Body
	}

	if err := h.backend.Create(r.Context(), absPath, reader); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleDelete removes a file or directory recursively.
// [AC-S0eedaa-1-1] — DELETE
func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request, absPath string) {
	info, err := h.backend.Stat(r.Context(), absPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if info.IsDir {
		err = h.backend.RemoveAll(r.Context(), absPath)
	} else {
		err = h.backend.Remove(r.Context(), absPath)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleMkdir creates a directory.
// POST /api/files/{share}/mkdir  body: {"path":"subdir/name"}
// [AC-S0eedaa-1-1] — mkdir
func (h *Handler) handleMkdir(w http.ResponseWriter, r *http.Request) {
	shareName := r.PathValue("share")
	claims, _, err := h.authAndScope(r, "")
	if err != nil {
		h.writeError(w, err)
		return
	}
	_ = claims

	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if body.Path == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}

	root, err := h.shares.ResolveShareRoot(r.Context(), shareName)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var absPath string
	if h.useRealPaths {
		absPath, err = safeJoinReal(root, body.Path)
	} else {
		absPath, err = safeJoin(root, body.Path)
	}
	if err != nil {
		http.Error(w, "forbidden: "+err.Error(), http.StatusForbidden)
		return
	}

	if err := h.backend.Mkdir(r.Context(), absPath); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "created", "path": body.Path})
}

// copyMoveBody is the common request body for copy and move.
type copyMoveBody struct {
	Src string `json:"src"`
	Dst string `json:"dst"`
}

// handleCopy copies a file within the same share.
// POST /api/files/{share}/copy  body: {"src":"...","dst":"..."}
// [AC-S0eedaa-1-1] — copy
func (h *Handler) handleCopy(w http.ResponseWriter, r *http.Request) {
	shareName := r.PathValue("share")
	claims, _, err := h.authAndScope(r, "")
	if err != nil {
		h.writeError(w, err)
		return
	}
	_ = claims

	var body copyMoveBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if body.Src == "" || body.Dst == "" {
		http.Error(w, "src and dst are required", http.StatusBadRequest)
		return
	}

	root, err := h.shares.ResolveShareRoot(r.Context(), shareName)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	srcAbs, err := h.joinSafe(root, body.Src)
	if err != nil {
		http.Error(w, "forbidden (src): "+err.Error(), http.StatusForbidden)
		return
	}
	dstAbs, err := h.joinSafe(root, body.Dst)
	if err != nil {
		http.Error(w, "forbidden (dst): "+err.Error(), http.StatusForbidden)
		return
	}

	if err := h.backend.Copy(r.Context(), srcAbs, dstAbs); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "copied"})
}

// handleMove renames / moves a file within the same share.
// POST /api/files/{share}/move  body: {"src":"...","dst":"..."}
// [AC-S0eedaa-1-1] — move
func (h *Handler) handleMove(w http.ResponseWriter, r *http.Request) {
	shareName := r.PathValue("share")
	claims, _, err := h.authAndScope(r, "")
	if err != nil {
		h.writeError(w, err)
		return
	}
	_ = claims

	var body copyMoveBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if body.Src == "" || body.Dst == "" {
		http.Error(w, "src and dst are required", http.StatusBadRequest)
		return
	}

	root, err := h.shares.ResolveShareRoot(r.Context(), shareName)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	srcAbs, err := h.joinSafe(root, body.Src)
	if err != nil {
		http.Error(w, "forbidden (src): "+err.Error(), http.StatusForbidden)
		return
	}
	dstAbs, err := h.joinSafe(root, body.Dst)
	if err != nil {
		http.Error(w, "forbidden (dst): "+err.Error(), http.StatusForbidden)
		return
	}

	if err := h.backend.Rename(r.Context(), srcAbs, dstAbs); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "moved"})
}

// authAndScope validates the token, loads the app scope, and evaluates
// whether the app may access (shareName, relPath).
// Returns (claims, shareName, error).
func (h *Handler) authAndScope(r *http.Request, relPath string) (TokenClaims, string, error) {
	token := r.Header.Get("X-Kura-Token")
	if token == "" {
		return TokenClaims{}, "", &httpError{status: http.StatusUnauthorized, msg: "X-Kura-Token is required"}
	}

	claims, err := h.validator.Validate(r.Context(), token)
	if err != nil {
		return TokenClaims{}, "", &httpError{status: http.StatusUnauthorized, msg: "invalid token: " + err.Error()}
	}

	shareName := r.PathValue("share")

	// Scope evaluation: only when we have a share name and path.
	if shareName != "" {
		scope, err := h.scopes.GetScope(r.Context(), claims.AppName)
		if err != nil {
			return TokenClaims{}, "", &httpError{status: http.StatusForbidden, msg: "no file API scope for app: " + claims.AppName}
		}
		// [AC-S0eedaa-1-2] — scope evaluation
		if evalErr := EvalScope(scope, shareName, relPath); evalErr != nil {
			return TokenClaims{}, "", &httpError{status: http.StatusForbidden, msg: evalErr.Error()}
		}
	}

	return claims, shareName, nil
}

func (h *Handler) joinSafe(root, path string) (string, error) {
	if h.useRealPaths {
		return safeJoinReal(root, path)
	}
	return safeJoin(root, path)
}

// listResponse is the JSON body returned by list endpoints.
type listResponse struct {
	Share   string     `json:"share"`
	Path    string     `json:"path"`
	Entries []FileInfo `json:"entries"`
}

// httpError carries a status code for h.writeError.
type httpError struct {
	status int
	msg    string
}

func (e *httpError) Error() string { return e.msg }

func (h *Handler) writeError(w http.ResponseWriter, err error) {
	var he *httpError
	if errors.As(err, &he) {
		http.Error(w, he.msg, he.status)
		return
	}
	if errors.Is(err, os.ErrNotExist) {
		http.NotFound(w, nil) //nolint:staticcheck
		w.WriteHeader(http.StatusNotFound)
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// UserFilesHandler returns a simplified handler for the built-in filebrowser
// that uses session-based auth (no X-Kura-Token) and has full access to all
// shares the current user's ACL permits.
//
// userShares is a func that returns the share names accessible to the session user.
// This is called per-request so ACL changes take effect immediately.
func UserFilesHandler(backend FileBackend, shares ShareResolver, userShares func(r *http.Request) []string, useRealPaths bool) http.Handler {
	mux := http.NewServeMux()
	h := &userFilesHandler{backend: backend, shares: shares, userShares: userShares, useRealPaths: useRealPaths}
	mux.HandleFunc("/ui/files/list", h.handleList)
	mux.HandleFunc("/ui/files/download/{share}/{path...}", h.handleDownload)
	mux.HandleFunc("POST /ui/files/upload/{share}/{path...}", h.handleUpload)
	mux.HandleFunc("POST /ui/files/delete/{share}/{path...}", h.handleDelete)
	mux.HandleFunc("POST /ui/files/rename/{share}/{path...}", h.handleRename)
	mux.HandleFunc("POST /ui/files/mkdir", h.handleMkdir)
	return mux
}

// userFilesHandler handles the browser-based file manager (no X-Kura-Token).
type userFilesHandler struct {
	backend      FileBackend
	shares       ShareResolver
	userShares   func(r *http.Request) []string
	useRealPaths bool
}

func (h *userFilesHandler) handleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	shareName := r.URL.Query().Get("share")
	relPath := r.URL.Query().Get("path")
	if relPath == "" {
		relPath = "/"
	}

	allowed := h.userShares(r)
	if shareName == "" {
		// Return the list of accessible shares.
		type shareEntry struct {
			Name string `json:"name"`
		}
		var entries []shareEntry
		for _, s := range allowed {
			entries = append(entries, shareEntry{Name: s})
		}
		writeJSON(w, http.StatusOK, map[string]any{"shares": entries})
		return
	}

	// Check user ACL.
	if !contains(allowed, shareName) {
		http.Error(w, "access denied", http.StatusForbidden)
		return
	}

	root, err := h.shares.ResolveShareRoot(r.Context(), shareName)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	absPath, err := h.joinSafe(root, relPath)
	if err != nil {
		http.Error(w, "forbidden: "+err.Error(), http.StatusForbidden)
		return
	}

	entries, err := h.backend.ReadDir(r.Context(), absPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, listResponse{Share: shareName, Path: relPath, Entries: entries})
}

func (h *userFilesHandler) handleDownload(w http.ResponseWriter, r *http.Request) {
	shareName := r.PathValue("share")
	relPath := "/" + r.PathValue("path")

	if !contains(h.userShares(r), shareName) {
		http.Error(w, "access denied", http.StatusForbidden)
		return
	}

	root, err := h.shares.ResolveShareRoot(r.Context(), shareName)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	absPath, err := h.joinSafe(root, relPath)
	if err != nil {
		http.Error(w, "forbidden: "+err.Error(), http.StatusForbidden)
		return
	}

	rsc, info, err := h.backend.Open(r.Context(), absPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rsc.Close()
	// Tell the browser to download rather than render inline. Without this,
	// text/* files open in a new tab and the [AC-S0eedaa-2-1] download e2e
	// never fires a download event. attachment + URL-encoded filename keeps
	// non-ASCII filenames intact (RFC 5987).
	w.Header().Set("Content-Disposition",
		`attachment; filename*=UTF-8''`+url.PathEscape(info.Name))
	// Use ServeContent for Range support (AC-S0eedaa-1-3).
	http.ServeContent(w, r, info.Name, info.ModTime, rsc)
}

func (h *userFilesHandler) handleUpload(w http.ResponseWriter, r *http.Request) {
	shareName := r.PathValue("share")
	relPath := "/" + r.PathValue("path")

	if !contains(h.userShares(r), shareName) {
		http.Error(w, "access denied", http.StatusForbidden)
		return
	}

	root, err := h.shares.ResolveShareRoot(r.Context(), shareName)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	absPath, err := h.joinSafe(root, relPath)
	if err != nil {
		http.Error(w, "forbidden: "+err.Error(), http.StatusForbidden)
		return
	}

	var reader io.Reader
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			http.Error(w, "bad multipart: "+err.Error(), http.StatusBadRequest)
			return
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "missing 'file' field", http.StatusBadRequest)
			return
		}
		defer f.Close()
		reader = f
	} else {
		reader = r.Body
	}

	if err := h.backend.Create(r.Context(), absPath, reader); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *userFilesHandler) handleDelete(w http.ResponseWriter, r *http.Request) {
	shareName := r.PathValue("share")
	relPath := "/" + r.PathValue("path")

	if !contains(h.userShares(r), shareName) {
		http.Error(w, "access denied", http.StatusForbidden)
		return
	}

	root, err := h.shares.ResolveShareRoot(r.Context(), shareName)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	absPath, err := h.joinSafe(root, relPath)
	if err != nil {
		http.Error(w, "forbidden: "+err.Error(), http.StatusForbidden)
		return
	}

	info, err := h.backend.Stat(r.Context(), absPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if info.IsDir {
		err = h.backend.RemoveAll(r.Context(), absPath)
	} else {
		err = h.backend.Remove(r.Context(), absPath)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *userFilesHandler) handleRename(w http.ResponseWriter, r *http.Request) {
	shareName := r.PathValue("share")
	relPath := "/" + r.PathValue("path")

	if !contains(h.userShares(r), shareName) {
		http.Error(w, "access denied", http.StatusForbidden)
		return
	}

	var body struct {
		NewName string `json:"new_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if body.NewName == "" {
		http.Error(w, "new_name is required", http.StatusBadRequest)
		return
	}

	root, err := h.shares.ResolveShareRoot(r.Context(), shareName)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	srcAbs, err := h.joinSafe(root, relPath)
	if err != nil {
		http.Error(w, "forbidden (src): "+err.Error(), http.StatusForbidden)
		return
	}

	// New path is the same directory with a new name.
	dir := strings.TrimSuffix(relPath, "/"+strings.TrimPrefix(lastSegment(relPath), "/"))
	newRelPath := dir + "/" + body.NewName
	dstAbs, err := h.joinSafe(root, newRelPath)
	if err != nil {
		http.Error(w, "forbidden (dst): "+err.Error(), http.StatusForbidden)
		return
	}

	if err := h.backend.Rename(r.Context(), srcAbs, dstAbs); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "renamed", "new_name": body.NewName})
}

func (h *userFilesHandler) handleMkdir(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Share string `json:"share"`
		Path  string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if body.Share == "" || body.Path == "" {
		http.Error(w, "share and path are required", http.StatusBadRequest)
		return
	}

	if !contains(h.userShares(r), body.Share) {
		http.Error(w, "access denied", http.StatusForbidden)
		return
	}

	root, err := h.shares.ResolveShareRoot(r.Context(), body.Share)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	absPath, err := h.joinSafe(root, body.Path)
	if err != nil {
		http.Error(w, "forbidden: "+err.Error(), http.StatusForbidden)
		return
	}

	if err := h.backend.Mkdir(r.Context(), absPath); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "created"})
}

func (h *userFilesHandler) joinSafe(root, path string) (string, error) {
	if h.useRealPaths {
		return safeJoinReal(root, path)
	}
	return safeJoin(root, path)
}

func contains(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func lastSegment(path string) string {
	parts := strings.Split(strings.TrimSuffix(path, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

// now is a package-level var so tests can override it.
var now = func() time.Time { return time.Now() }
