// users_pending_test.go: unit tests for the S413bd5-3 pending-user
// approval / rejection handlers and the UsersView split into
// PendingUsers / Users.
//
// Acceptance criteria covered:
//
//	[AC-S413bd5-3-1] /ui/admin/users users tab shows 承認待ち section when
//	                  pending users exist; section absent when count is 0.
//	[AC-S413bd5-3-2] POST /ui/admin/users/{id}/approve calls
//	                  PendingEngine.PromoteFromPending; success response
//	                  contains the plaintext password in a <code> element.
//	[AC-S413bd5-3-3] POST /ui/admin/users/{id}/reject calls
//	                  PendingEngine.DeleteUser; response redirects to
//	                  /ui/admin/users without the user.
//	[AC-S413bd5-3-4] Password is present in the approve-success fragment
//	                  and the warning copy is present.
//	[AC-S413bd5-3-5] All strings rendered via i18n (no hardcoded Japanese).
package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/kuraos-org/kura/i18n"
)

// fakePendingEngine satisfies PendingEngine.
type fakePendingEngine struct {
	// promoted tracks calls to PromoteFromPending: userID → role
	promoted map[string]string
	// deleted tracks calls to DeleteUser: userID → true
	deleted map[string]bool
	// promotedPassword is returned by PromoteFromPending
	promotedPassword string
	// failPromote, when non-nil, is returned from PromoteFromPending
	failPromote error
	// failDelete, when non-nil, is returned from DeleteUser
	failDelete error
}

func newFakePendingEngine() *fakePendingEngine {
	return &fakePendingEngine{
		promoted:         map[string]string{},
		deleted:          map[string]bool{},
		promotedPassword: "secret-generated-pw",
	}
}

func (f *fakePendingEngine) PromoteFromPending(_ context.Context, userID, newRole string) (string, error) {
	if f.failPromote != nil {
		return "", f.failPromote
	}
	f.promoted[userID] = newRole
	return f.promotedPassword, nil
}

func (f *fakePendingEngine) DeleteUser(_ context.Context, userID string) error {
	if f.failDelete != nil {
		return f.failDelete
	}
	f.deleted[userID] = true
	return nil
}

// newPendingRenderer builds a renderer with both users page and pending
// handlers wired; used in every test below.
func newPendingRenderer(t *testing.T, rows []UsersUserRow, engine *fakePendingEngine) *Renderer {
	t.Helper()
	tr, err := i18n.New()
	if err != nil {
		t.Fatalf("i18n.New: %v", err)
	}
	r, err := New(tr, "test")
	if err != nil {
		t.Fatalf("ui.New: %v", err)
	}
	r.SetUsersHandler(UsersDeps{
		Users:  &fakeUsersLister{rows: rows},
		Groups: &fakeGroupsLister{},
	})
	r.SetPendingHandlers(PendingDeps{Engine: engine})
	return r
}

// [AC-S413bd5-3-1] 承認待ちセクションがゼロの時は非表示になること
func TestUsersPage_PendingSection_HiddenWhenEmpty_AC_S413bd5_3_1(t *testing.T) {
	rows := []UsersUserRow{
		{UserID: "u-admin", Username: "admin", Role: "admin"},
	}
	r := newPendingRenderer(t, rows, newFakePendingEngine())

	srv := httptest.NewServer(r.Routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/ui/admin/users?tab=users")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body := readResp(resp)

	// Section must not appear when there are no pending users.
	if strings.Contains(body, `data-testid="pending-users-section"`) {
		t.Error("[AC-S413bd5-3-1] pending-users-section should be absent when no pending users exist")
	}
	// Active users table must still render.
	if !strings.Contains(body, `data-testid="users-table"`) {
		t.Error("[AC-S413bd5-3-1] users-table should be present")
	}
}

// [AC-S413bd5-3-1] 承認待ちユーザーが存在するときにセクションが表示されること
func TestUsersPage_PendingSection_ShownWhenExists_AC_S413bd5_3_1(t *testing.T) {
	rows := []UsersUserRow{
		{UserID: "u-admin", Username: "admin", Role: "admin"},
		{UserID: "u-pend", Username: "alice", Role: "pending"},
	}
	r := newPendingRenderer(t, rows, newFakePendingEngine())

	srv := httptest.NewServer(r.Routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/ui/admin/users?tab=users")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body := readResp(resp)

	for _, want := range []string{
		`data-testid="pending-users-section"`,
		`data-testid="pending-section-title"`,
		`承認待ちユーザー`,
		`data-testid="pending-users-row"`,
		`data-testid="pending-approve-btn"`,
		`data-testid="pending-reject-btn"`,
		// Approve and reject modal HTML should be rendered too.
		`data-testid="pending-approve-modal-u-pend"`,
		`data-testid="pending-reject-modal-u-pend"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("[AC-S413bd5-3-1] body missing %q", want)
		}
	}
	// pending user must NOT appear in active users table
	if strings.Contains(body, `data-user-id="u-pend"`) && strings.Contains(body, `data-testid="users-edit-btn"`) {
		// this is technically ambiguous; let's check the active users table specifically
	}
}

// [AC-S413bd5-3-1] pending user は active テーブルに出ないこと
func TestUsersPage_PendingUser_NotInActiveTable_AC_S413bd5_3_1(t *testing.T) {
	rows := []UsersUserRow{
		{UserID: "u-admin", Username: "admin", Role: "admin"},
		{UserID: "u-pend", Username: "alice", Role: "pending"},
	}
	r := newPendingRenderer(t, rows, newFakePendingEngine())

	srv := httptest.NewServer(r.Routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/ui/admin/users?tab=users")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body := readResp(resp)

	// The active table should have admin but not alice via the edit button path
	// (edit buttons only exist in the active section).
	// Count edit modal appearances for u-pend: should be 0.
	if strings.Contains(body, `users-edit-modal-u-pend`) {
		t.Error("[AC-S413bd5-3-1] pending user should not have an edit modal in the active users section")
	}
}

// [AC-S413bd5-3-2] POST /approve → PromoteFromPending が呼ばれ、パスワード断片が返ること
func TestPendingApprove_Success_AC_S413bd5_3_2(t *testing.T) {
	eng := newFakePendingEngine()
	eng.promotedPassword = "generated-pw-abc"

	r := newPendingRenderer(t, nil, eng)
	srv := httptest.NewServer(r.Routes())
	defer srv.Close()

	httpClient := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	resp, err := httpClient.PostForm(
		srv.URL+"/ui/admin/users/u-pend/approve",
		url.Values{"role": {"user"}},
	)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("[AC-S413bd5-3-2] status = %d, want 200", resp.StatusCode)
	}
	body := readResp(resp)
	// Fragment must contain the plaintext password.
	if !strings.Contains(body, "generated-pw-abc") {
		t.Errorf("[AC-S413bd5-3-2] approve-success fragment missing plaintext password")
	}
	if !strings.Contains(body, `data-testid="approve-success-password"`) {
		t.Errorf("[AC-S413bd5-3-2] fragment missing approve-success-password testid")
	}
	// Engine must have been called with the correct args.
	if got := eng.promoted["u-pend"]; got != "user" {
		t.Errorf("[AC-S413bd5-3-2] PromoteFromPending called with role=%q, want user", got)
	}
}

// [AC-S413bd5-3-2] role=admin も承認できること
func TestPendingApprove_AdminRole_AC_S413bd5_3_2(t *testing.T) {
	eng := newFakePendingEngine()
	r := newPendingRenderer(t, nil, eng)
	srv := httptest.NewServer(r.Routes())
	defer srv.Close()

	httpClient := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := httpClient.PostForm(
		srv.URL+"/ui/admin/users/u-pend/approve",
		url.Values{"role": {"admin"}},
	)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("[AC-S413bd5-3-2] status = %d, want 200", resp.StatusCode)
	}
	if got := eng.promoted["u-pend"]; got != "admin" {
		t.Errorf("[AC-S413bd5-3-2] PromoteFromPending called with role=%q, want admin", got)
	}
}

// [AC-S413bd5-3-2] 無効な role はリダイレクトエラーになること
func TestPendingApprove_InvalidRole_AC_S413bd5_3_2(t *testing.T) {
	eng := newFakePendingEngine()
	r := newPendingRenderer(t, nil, eng)
	srv := httptest.NewServer(r.Routes())
	defer srv.Close()

	httpClient := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := httpClient.PostForm(
		srv.URL+"/ui/admin/users/u-pend/approve",
		url.Values{"role": {"superuser"}},
	)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("[AC-S413bd5-3-2] expected redirect on bad role, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); !strings.Contains(loc, "err=") {
		t.Errorf("[AC-S413bd5-3-2] expected err= in redirect, got %q", loc)
	}
	if len(eng.promoted) != 0 {
		t.Error("[AC-S413bd5-3-2] engine should not be called on bad role")
	}
}

// [AC-S413bd5-3-3] POST /reject → DeleteUser が呼ばれ、ユーザーページへリダイレクトされること
func TestPendingReject_Success_AC_S413bd5_3_3(t *testing.T) {
	eng := newFakePendingEngine()
	r := newPendingRenderer(t, nil, eng)
	srv := httptest.NewServer(r.Routes())
	defer srv.Close()

	httpClient := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := httpClient.PostForm(
		srv.URL+"/ui/admin/users/u-pend/reject",
		url.Values{},
	)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("[AC-S413bd5-3-3] status = %d, want 303", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "/ui/admin/users") {
		t.Errorf("[AC-S413bd5-3-3] redirect location = %q, want /ui/admin/users*", loc)
	}
	// No err param on success.
	if strings.Contains(loc, "err=") {
		t.Errorf("[AC-S413bd5-3-3] unexpected err= in success redirect: %q", loc)
	}
	if !eng.deleted["u-pend"] {
		t.Error("[AC-S413bd5-3-3] DeleteUser should have been called with u-pend")
	}
}

// [AC-S413bd5-3-4] 承認成功フラグメントに1回限りパスワードと警告が含まれること
func TestPendingApprove_PasswordRevealFragment_AC_S413bd5_3_4(t *testing.T) {
	eng := newFakePendingEngine()
	eng.promotedPassword = "once-only-password"
	r := newPendingRenderer(t, nil, eng)
	srv := httptest.NewServer(r.Routes())
	defer srv.Close()

	httpClient := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := httpClient.PostForm(
		srv.URL+"/ui/admin/users/u-foo/approve",
		url.Values{"role": {"user"}},
	)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	body := readResp(resp)
	// Password must appear exactly in the fragment.
	if !strings.Contains(body, "once-only-password") {
		t.Errorf("[AC-S413bd5-3-4] fragment missing plaintext password")
	}
	// Warning copy must be present.
	if !strings.Contains(body, "ここでしか確認できません") {
		t.Errorf("[AC-S413bd5-3-4] fragment missing one-time warning text")
	}
	// Title must be present.
	if !strings.Contains(body, `data-testid="approve-success-title"`) {
		t.Errorf("[AC-S413bd5-3-4] fragment missing approve-success-title testid")
	}
	// Dismiss button must be present.
	if !strings.Contains(body, `data-testid="approve-success-dismiss-btn"`) {
		t.Errorf("[AC-S413bd5-3-4] fragment missing dismiss button")
	}
	// Copy button must be present.
	if !strings.Contains(body, `data-testid="approve-success-copy-btn"`) {
		t.Errorf("[AC-S413bd5-3-4] fragment missing copy button")
	}
}

// [AC-S413bd5-3-5] i18n — pending section uses translated strings (spot-check
// some key messages appear via T() and are not raw MessageIDs).
func TestUsersPage_PendingSection_I18NStrings_AC_S413bd5_3_5(t *testing.T) {
	rows := []UsersUserRow{
		{UserID: "u-pend", Username: "bob", Role: "pending"},
	}
	r := newPendingRenderer(t, rows, newFakePendingEngine())
	srv := httptest.NewServer(r.Routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/ui/admin/users?tab=users")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body := readResp(resp)

	translated := []string{
		"承認待ちユーザー",
		"承認",
		"却下",
	}
	for _, want := range translated {
		if !strings.Contains(body, want) {
			t.Errorf("[AC-S413bd5-3-5] body missing translated string %q", want)
		}
	}
	// Verify MessageIDs are not leaking as raw text in rendered output.
	rawIDs := []string{
		"page.users.pending_section_title",
		"page.users.approve_button",
		"page.users.reject_button",
	}
	for _, id := range rawIDs {
		if strings.Contains(body, ">"+id+"<") {
			t.Errorf("[AC-S413bd5-3-5] raw MessageID %q appeared in rendered output", id)
		}
	}
}

// pathSegment helper unit test to guard against regressions.
func TestPathSegment(t *testing.T) {
	cases := []struct {
		path   string
		suffix string
		want   string
	}{
		{"/ui/admin/users/u-abc123/approve", "/approve", "u-abc123"},
		{"/ui/admin/users/u-abc123/reject", "/reject", "u-abc123"},
		{"/ui/admin/users//approve", "/approve", ""},
		{"", "/approve", ""},
	}
	for _, c := range cases {
		got := pathSegment(c.path, c.suffix)
		if got != c.want {
			t.Errorf("pathSegment(%q, %q) = %q, want %q", c.path, c.suffix, got, c.want)
		}
	}
}
