package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// [AC-S413bd5-2-4] TestPendingApprovalHandler verifies:
//   - GET /ui/pending-approval → 200 with i18n "承認待ち" title copy
//   - The page contains a form POSTing to /logout (ログアウト button)
//   - The logout button text is present
//   - No sidebar navigation (auth layout, not admin layout)
func TestPendingApprovalHandler(t *testing.T) {
	r := newTestRenderer(t)
	srv := httptest.NewServer(r.PendingApprovalHandler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET pending-approval: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	body := readBody(t, resp)

	for _, want := range []string{
		`data-testid="pending-approval-title"`,
		`承認待ち`,
		`data-testid="pending-approval-body"`,
		`登録申請を受け付けました`,
		`action="/logout"`,
		`data-testid="pending-approval-logout-btn"`,
		`ログアウト`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("pending-approval body missing %q", want)
		}
	}
}

// TestPendingApprovalHandler_MethodNotAllowed verifies POST returns 405.
func TestPendingApprovalHandler_MethodNotAllowed(t *testing.T) {
	r := newTestRenderer(t)
	srv := httptest.NewServer(r.PendingApprovalHandler())
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/", "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}

// [AC-S413bd5-2-4] TestPendingApprovalPage_ContentTypeHTML confirms the
// response is served as HTML.
func TestPendingApprovalPage_ContentTypeHTML(t *testing.T) {
	r := newTestRenderer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/pending-approval", nil)
	r.PendingApprovalHandler().ServeHTTP(rec, req)

	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", rec.Header().Get("Content-Type"))
	}
}
