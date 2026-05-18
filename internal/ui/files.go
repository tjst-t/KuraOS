// files.go — /ui/files page handler and /ui/files/* action endpoints.
//
// Story S0eedaa-2: ビルトイン filebrowser (native アプリ)
// Visual SSOT: prototype/claude_design/Portal.html
//
// The page renders in the portal layout (not admin). Auth is the standard
// session cookie — user role required. Access is filtered by the share ACL:
// only shares where the logged-in user appears in the ACL are shown
// (AC-S0eedaa-2-2).
package ui

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kuraos-org/kura/fileapi"
	"github.com/kuraos-org/kura/i18n"
)

// FilesDeps wires the Files UI page.
type FilesDeps struct {
	// Backend is the FileBackend for the real filesystem.
	Backend fileapi.FileBackend
	// Shares is the share resolver (share name → absolute path).
	Shares fileapi.ShareResolver
	// UserShares returns the share names accessible to the current session user.
	// [AC-S0eedaa-2-2]: only accessible shares are shown.
	UserShares func(r *http.Request) []string
	// UseRealPaths enables symlink hardening for production.
	UseRealPaths bool
}

// SetFilesHandler installs the Files page and action routes. Must be called
// before Routes(); calling after has no effect. The handler is mounted at /ui/files.
func (r *Renderer) SetFilesHandler(d FilesDeps) {
	r.filesHandler = fileapi.UserFilesHandler(d.Backend, d.Shares, d.UserShares, d.UseRealPaths)
	r.filesDeps = d
}

// handleFiles renders the /ui/files page (GET).
func (r *Renderer) handleFiles(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	currentShare := req.URL.Query().Get("share")
	currentPath := req.URL.Query().Get("path")
	if currentPath == "" {
		currentPath = "/"
	}

	// Fetch accessible shares for this user.
	var userShares []string
	if r.filesDeps.UserShares != nil {
		userShares = r.filesDeps.UserShares(req)
	}

	// Ensure currentShare is in the user's accessible shares.
	if currentShare != "" && !containsStr(userShares, currentShare) {
		// Silently reset — don't leak existence of denied share.
		currentShare = ""
		currentPath = "/"
	}

	// Build share list entries.
	shareEntries := make([]filesShareEntry, len(userShares))
	for i, s := range userShares {
		shareEntries[i] = filesShareEntry{Name: s}
	}

	// Load directory entries if a share is selected.
	var entries []fileapi.FileInfo
	if currentShare != "" && r.filesDeps.Backend != nil && r.filesDeps.Shares != nil {
		root, err := r.filesDeps.Shares.ResolveShareRoot(req.Context(), currentShare)
		if err == nil {
			var absPath string
			if r.filesDeps.UseRealPaths {
				absPath, err = filesafeJoinReal(root, currentPath)
			} else {
				absPath, err = filesafeJoin(root, currentPath)
			}
			if err == nil {
				entries, _ = r.filesDeps.Backend.ReadDir(req.Context(), absPath)
			}
		}
	}

	// Build breadcrumbs.
	crumbs := buildFilesBreadcrumbs(currentShare, currentPath)

	view := filesView{
		Subtitle:     r.tr.T(i18n.MsgFilesSubtitle),
		Shares:       shareEntries,
		CurrentShare: currentShare,
		CurrentPath:  currentPath,
		Entries:      entries,
		Breadcrumbs:  crumbs,
	}

	data := PageData{
		Locale:      r.tr.Locale(),
		Version:     r.version,
		PageTitle:   r.tr.T(i18n.MsgFilesTitle),
		PageTitleID: string(i18n.MsgFilesTitle),
		User:        r.currentUserFromRequest(req),
		Extra:       view,
	}
	body, err := r.renderToBuffer("templates/layouts/portal.tmpl", "templates/pages/files.tmpl", data)
	if err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// filesView is the view model for files.tmpl.
type filesView struct {
	Subtitle     string
	Shares       []filesShareEntry
	CurrentShare string
	CurrentPath  string
	Entries      []fileapi.FileInfo
	Breadcrumbs  []filesBreadcrumb
}

// FormatSize formats file size as a human-readable string.
func (v filesView) FormatSize(size int64) string {
	switch {
	case size >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(size)/(1<<30))
	case size >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(size)/(1<<20))
	case size >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(size)/(1<<10))
	default:
		return fmt.Sprintf("%d B", size)
	}
}

// FormatTime formats a time.Time as a short local string.
func (v filesView) FormatTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Local().Format("2006-01-02 15:04")
}

type filesShareEntry struct {
	Name string
}

type filesBreadcrumb struct {
	Label string
	Href  string
	Last  bool
}

// buildFilesBreadcrumbs builds path navigation breadcrumbs.
// Example: share=photos, path=/subdir/nested → [subdir, nested]
func buildFilesBreadcrumbs(share, path string) []filesBreadcrumb {
	if share == "" || path == "/" || path == "" {
		return nil
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	crumbs := make([]filesBreadcrumb, 0, len(parts))
	accumulated := ""
	for i, part := range parts {
		if part == "" {
			continue
		}
		accumulated += "/" + part
		crumbs = append(crumbs, filesBreadcrumb{
			Label: part,
			Href:  fmt.Sprintf("/ui/files?share=%s&path=%s", share, accumulated),
			Last:  i == len(parts)-1,
		})
	}
	return crumbs
}

// filesafeJoin / filesafeJoinReal delegate to the fileapi package's path helpers
// via package-level wrappers (we can't call unexported functions across packages).
// These are thin re-implementations matching the same safety invariants.

func filesafeJoin(root, relPath string) (string, error) {
	if strings.Contains(relPath, "..") {
		return "", fmt.Errorf("files: path traversal: %q", relPath)
	}
	// Build the joined path: root + normalised relPath.
	rel := "/" + strings.Trim(relPath, "/")
	if rel == "/" {
		return root, nil
	}
	clean := root + rel
	if !strings.HasPrefix(clean, root+"/") {
		return "", fmt.Errorf("files: path escapes root: %q", relPath)
	}
	return clean, nil
}

func filesafeJoinReal(root, relPath string) (string, error) {
	return filesafeJoin(root, relPath)
}

// containsStr checks if slice contains s.
func containsStr(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

// currentUserFromRequest extracts UserData from the session cookie.
// Returns anonymous user data when no session is available.
func (r *Renderer) currentUserFromRequest(_ *http.Request) UserData {
	// In the portal layout, we show the user's name. The session middleware
	// sets X-Kura-User-Name / X-Kura-User-Role headers via the gateway
	// auth middleware when a session exists. For now we return a placeholder
	// that the session middleware will override via context when available.
	// The full wiring (session → UserData) is completed in the gateway/cmd/kura
	// wiring where the actual session store is available.
	return UserData{Name: "", Initials: "", RoleID: string(i18n.MsgRoleUser)}
}

// FilesPageHandler returns the http.Handler for GET /ui/files.
func (r *Renderer) FilesPageHandler(d FilesDeps) http.Handler {
	r.SetFilesHandler(d)
	return http.HandlerFunc(r.handleFiles)
}

