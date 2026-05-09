package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kuraos-org/kura/i18n"
)

// newTestRenderer builds a Renderer wired to the embedded ja locale. Used by
// every test below so the parsing path exercises real templates.
func newTestRenderer(t *testing.T) *Renderer {
	t.Helper()
	tr, err := i18n.New()
	if err != nil {
		t.Fatalf("i18n.New: %v", err)
	}
	r, err := New(tr, "test")
	if err != nil {
		t.Fatalf("ui.New: %v", err)
	}
	return r
}

func TestAdminPaths_ReturnHTML(t *testing.T) {
	r := newTestRenderer(t)
	srv := httptest.NewServer(r.Routes())
	defer srv.Close()

	cases := []struct {
		name string
		path string
	}{
		{"dashboard", "/ui/admin/dashboard"},
		{"storage", "/ui/admin/storage"},
		{"shares", "/ui/admin/shares"},
		{"users", "/ui/admin/users"},
		{"network", "/ui/admin/network"},
		{"apps", "/ui/admin/apps"},
		{"settings", "/ui/admin/settings"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, err := http.Get(srv.URL + c.path)
			if err != nil {
				t.Fatalf("GET %s: %v", c.path, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			ct := resp.Header.Get("Content-Type")
			if !strings.HasPrefix(ct, "text/html") {
				t.Fatalf("Content-Type = %q, want text/html...", ct)
			}
		})
	}
}

// Regression: htmx.min.js must actually be served. Without it every hx-get /
// hx-post button on the admin UI is inert in a real browser, yet server-side
// handlers pass their unit tests because they're hit directly via httptest.
// This caught a production gap where dist/ shipped only kura.css + kura.js
// and every htmx-driven flow (Share ACL row picker, Group members modal,
// app install SSE progress) silently no-op'd.
func TestStaticAssets_HTMXAndCSSReachable(t *testing.T) {
	r := newTestRenderer(t)
	srv := httptest.NewServer(r.Routes())
	defer srv.Close()

	cases := []struct {
		name string
		path string
		mime string
	}{
		{"htmx", "/ui/static/htmx.min.js", "javascript"},
		{"kura.js", "/ui/static/kura.js", "javascript"},
		{"kura.css", "/ui/static/kura.css", "css"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, err := http.Get(srv.URL + c.path)
			if err != nil {
				t.Fatalf("GET %s: %v", c.path, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				t.Fatalf("status = %d, want 200 (asset missing from internal/ui/dist/?)", resp.StatusCode)
			}
			if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, c.mime) {
				t.Fatalf("Content-Type = %q, want substring %q", ct, c.mime)
			}
		})
	}
}

func TestAdminDashboard_RendersJaStrings(t *testing.T) {
	r := newTestRenderer(t)
	srv := httptest.NewServer(r.Routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/ui/admin/dashboard")
	if err != nil {
		t.Fatalf("GET dashboard: %v", err)
	}
	defer resp.Body.Close()
	body := readBody(t, resp)

	// [AC-S464e47-1-2] every visible string flows through i18n. Spot-check by
	// looking for translated values rather than the raw MessageIDs.
	wantTexts := []string{
		"ダッシュボード", // page.dashboard.title + nav.dashboard
		"ストレージ",   // nav.storage
		"共有",      // nav.shares
		"ユーザー",    // nav.users
		"ネットワーク",  // nav.network
		"アプリ",     // nav.apps
		"設定",      // nav.settings
		"システム管理",  // nav.section.admin
	}
	for _, w := range wantTexts {
		if !strings.Contains(body, w) {
			t.Errorf("body missing translated string %q", w)
		}
	}

	// [AC-S464e47-1-1] sidebar must contain 7 items with hrefs to admin pages.
	expectedHrefs := []string{
		"/ui/admin/dashboard",
		"/ui/admin/storage",
		"/ui/admin/shares",
		"/ui/admin/users",
		"/ui/admin/network",
		"/ui/admin/apps",
		"/ui/admin/settings",
	}
	for _, href := range expectedHrefs {
		if !strings.Contains(body, `href="`+href+`"`) {
			t.Errorf("body missing nav href %q", href)
		}
	}
}

func TestAdminDashboard_HasNoRawMessageIDs(t *testing.T) {
	r := newTestRenderer(t)
	srv := httptest.NewServer(r.Routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/ui/admin/dashboard")
	if err != nil {
		t.Fatalf("GET dashboard: %v", err)
	}
	defer resp.Body.Close()
	body := readBody(t, resp)

	// If a MessageID falls through unresolved, the Translator returns the id
	// itself (e.g. "nav.dashboard"). That literal showing up in HTML output
	// means we forgot a key in ja.json — turn that into a test failure.
	bad := []string{
		"nav.dashboard",
		"nav.storage",
		"page.dashboard.title",
		"brand.name",
	}
	for _, id := range bad {
		// Allow the id to appear as a data attribute or class, but not as
		// rendered text. We approximate by checking it doesn't appear in
		// places that aren't preceded by a quote/= sign — simpler check:
		// the translated value differs from the id.
		if strings.Contains(body, ">"+id+"<") {
			t.Errorf("found unresolved MessageID %q in rendered text", id)
		}
	}
}

func TestAdminRoot_RedirectsToDashboard(t *testing.T) {
	r := newTestRenderer(t)
	srv := httptest.NewServer(r.Routes())
	defer srv.Close()

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Get(srv.URL + "/ui/admin")
	if err != nil {
		t.Fatalf("GET /ui/admin: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/ui/admin/dashboard" {
		t.Fatalf("Location = %q, want /ui/admin/dashboard", loc)
	}
}

func TestAllSidebarMessageIDsPresent(t *testing.T) {
	tr, err := i18n.New()
	if err != nil {
		t.Fatalf("i18n.New: %v", err)
	}
	required := []i18n.MessageID{
		i18n.MsgBrandName,
		i18n.MsgNavSectionAdmin,
		i18n.MsgNavDashboard,
		i18n.MsgNavStorage,
		i18n.MsgNavShares,
		i18n.MsgNavUsers,
		i18n.MsgNavNetwork,
		i18n.MsgNavApps,
		i18n.MsgNavSettings,
		i18n.MsgRoleAdmin,
		i18n.MsgRoleUser,
		i18n.MsgHeaderToggleSidebar,
		i18n.MsgHeaderToggleTheme,
		i18n.MsgHeaderNotifications,
		i18n.MsgHeaderSearch,
		i18n.MsgDashboardTitle,
		i18n.MsgDashboardSubtitle,
		i18n.MsgDashboardEmpty,
		i18n.MsgBtnRefresh,
		i18n.MsgBtnSave,
		i18n.MsgBtnCancel,
		i18n.MsgBtnApply,
		i18n.MsgConfigExportEmptyState,
		i18n.MsgConfigInvalidJSON,
		i18n.MsgConfigDiffNoChanges,
	}
	for _, id := range required {
		t.Run(string(id), func(t *testing.T) {
			if !tr.Has(id) {
				t.Fatalf("ja.json missing translation for %q", id)
			}
		})
	}
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			sb.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
	return sb.String()
}
