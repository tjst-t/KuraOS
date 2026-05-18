package fileapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kuraos-org/kura/fileapi"
)

// Ensure httptest is used for the server in scope_test.go too.
var _ = httptest.NewServer

// testSetup builds a Handler backed by MemBackend with a "photos" share
// pre-seeded with a hello.txt file.
func testSetup(t *testing.T) (*fileapi.Handler, *fileapi.MemBackend, *httptest.Server) {
	t.Helper()
	mem := fileapi.NewMemBackend()
	mem.WriteDir("/photos")
	mem.WriteFile("/photos/hello.txt", []byte("hello world\n"))
	mem.WriteDir("/photos/subdir")
	mem.WriteFile("/photos/subdir/nested.txt", []byte("nested\n"))

	resolver := &fileapi.FixedShareResolver{
		Roots: map[string]string{"photos": "/photos"},
	}
	scope := &fileapi.FixedScopeStore{
		Scope: fileapi.AppScope{Shares: []string{"photos"}},
	}
	validator := fileapi.NoopValidator{}

	h := fileapi.NewHandler(fileapi.HandlerConfig{
		Backend:      mem,
		Validator:    validator,
		Scopes:       scope,
		Shares:       resolver,
		UseRealPaths: false,
	})
	srv := httptest.NewServer(h.Routes())
	t.Cleanup(srv.Close)
	return h, mem, srv
}

func doReq(t *testing.T, method, url string, body io.Reader, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	// Default token.
	if req.Header.Get("X-Kura-Token") == "" {
		req.Header.Set("X-Kura-Token", "filebrowser.admin.0.9999999999")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	return resp
}

// [AC-S0eedaa-1-1] GET list root directory.
func TestListShareRoot(t *testing.T) {
	_, _, srv := testSetup(t)

	resp := doReq(t, http.MethodGet, srv.URL+"/api/files/photos", nil, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var out struct {
		Share   string             `json:"share"`
		Path    string             `json:"path"`
		Entries []fileapi.FileInfo `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Share != "photos" {
		t.Errorf("want share=photos, got %q", out.Share)
	}
	if len(out.Entries) == 0 {
		t.Error("want non-empty entries")
	}
}

// [AC-S0eedaa-1-1] GET download a file.
func TestDownloadFile(t *testing.T) {
	_, _, srv := testSetup(t)

	resp := doReq(t, http.MethodGet, srv.URL+"/api/files/photos/hello.txt", nil, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "hello world") {
		t.Errorf("want file content, got: %q", body)
	}
}

// [AC-S0eedaa-1-3] Range request — partial read.
func TestRangeRequest(t *testing.T) {
	_, _, srv := testSetup(t)

	resp := doReq(t, http.MethodGet, srv.URL+"/api/files/photos/hello.txt", nil, map[string]string{
		"Range": "bytes=0-4",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("want 206 Partial Content, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello" {
		t.Errorf("want partial body %q, got %q", "hello", body)
	}
}

// [AC-S0eedaa-1-1] GET stat.
func TestStatFile(t *testing.T) {
	_, _, srv := testSetup(t)

	resp := doReq(t, http.MethodGet, srv.URL+"/api/files/photos/hello.txt?stat=1", nil, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var info fileapi.FileInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if info.Name != "hello.txt" {
		t.Errorf("want name=hello.txt, got %q", info.Name)
	}
	if info.IsDir {
		t.Error("want is_dir=false")
	}
}

// [AC-S0eedaa-1-1] PUT upload via multipart.
func TestUploadMultipart(t *testing.T) {
	_, mem, srv := testSetup(t)

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("file", "uploaded.txt")
	fw.Write([]byte("uploaded content"))
	w.Close()

	resp := doReq(t, http.MethodPut,
		srv.URL+"/api/files/photos/uploaded.txt",
		&buf,
		map[string]string{"Content-Type": w.FormDataContentType()},
	)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, body)
	}
	content, ok := mem.FileContent("/photos/uploaded.txt")
	if !ok {
		t.Fatal("file not created in backend")
	}
	if string(content) != "uploaded content" {
		t.Errorf("unexpected content: %q", content)
	}
}

// [AC-S0eedaa-1-1] PUT upload via raw body.
func TestUploadRawPUT(t *testing.T) {
	_, mem, srv := testSetup(t)

	resp := doReq(t, http.MethodPut,
		srv.URL+"/api/files/photos/raw.txt",
		strings.NewReader("raw content"),
		map[string]string{"Content-Type": "application/octet-stream"},
	)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, body)
	}
	content, ok := mem.FileContent("/photos/raw.txt")
	if !ok {
		t.Fatal("file not created")
	}
	if string(content) != "raw content" {
		t.Errorf("want 'raw content', got %q", content)
	}
}

// [AC-S0eedaa-1-1] DELETE a file.
func TestDeleteFile(t *testing.T) {
	_, mem, srv := testSetup(t)

	resp := doReq(t, http.MethodDelete, srv.URL+"/api/files/photos/hello.txt", nil, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if mem.Exists("/photos/hello.txt") {
		t.Error("file still exists after delete")
	}
}

// [AC-S0eedaa-1-1] POST mkdir.
func TestMkdir(t *testing.T) {
	_, mem, srv := testSetup(t)

	body := `{"path":"newdir"}`
	resp := doReq(t, http.MethodPost,
		srv.URL+"/api/files/photos/mkdir",
		strings.NewReader(body),
		map[string]string{"Content-Type": "application/json"},
	)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, b)
	}
	if !mem.Exists("/photos/newdir") {
		t.Error("directory not created")
	}
}

// [AC-S0eedaa-1-1] POST copy.
func TestCopyFile(t *testing.T) {
	_, mem, srv := testSetup(t)

	body := `{"src":"hello.txt","dst":"hello_copy.txt"}`
	resp := doReq(t, http.MethodPost,
		srv.URL+"/api/files/photos/copy",
		strings.NewReader(body),
		map[string]string{"Content-Type": "application/json"},
	)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, b)
	}
	if !mem.Exists("/photos/hello_copy.txt") {
		t.Error("copy not created")
	}
	// Original still there.
	if !mem.Exists("/photos/hello.txt") {
		t.Error("original deleted during copy")
	}
}

// [AC-S0eedaa-1-1] POST move.
func TestMoveFile(t *testing.T) {
	_, mem, srv := testSetup(t)

	body := `{"src":"hello.txt","dst":"hello_moved.txt"}`
	resp := doReq(t, http.MethodPost,
		srv.URL+"/api/files/photos/move",
		strings.NewReader(body),
		map[string]string{"Content-Type": "application/json"},
	)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, b)
	}
	if !mem.Exists("/photos/hello_moved.txt") {
		t.Error("moved file not found at destination")
	}
	if mem.Exists("/photos/hello.txt") {
		t.Error("source still exists after move")
	}
}

// [AC-S0eedaa-1-1] Missing token → 401.
func TestMissingToken(t *testing.T) {
	_, _, srv := testSetup(t)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/files/photos", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", resp.StatusCode)
	}
}

// [AC-S0eedaa-1-1] Not-found path → 404.
func TestNotFoundPath(t *testing.T) {
	_, _, srv := testSetup(t)

	resp := doReq(t, http.MethodGet, srv.URL+"/api/files/photos/nonexistent.txt", nil, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("want 404, got %d", resp.StatusCode)
	}
}
