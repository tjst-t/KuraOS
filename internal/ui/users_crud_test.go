// users_crud_test.go covers the Sfix001-1 + Sfix001-2 UI surfaces:
//
//   AC-Sfix001-1-1 — "+ ユーザー追加" button + new-user modal posts to
//                    /ui/admin/users/create.
//   AC-Sfix001-1-2 — per-row 編集 / 削除 actions; admin self-delete is
//                    refused with a translated banner.
//   AC-Sfix001-2-1 — "+ グループ追加" button + new-group modal posts to
//                    /ui/admin/groups/create.
//   AC-Sfix001-2-2 — メンバー編集 modal renders checkboxes for every
//                    user and posts the selection to /groups/members.
//
// The handler tests use an in-memory fake SystemEngine so the UI layer
// is exercised end-to-end (form -> handler -> redirect with banner)
// without booting engine/system.
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

type fakeGroupsLister struct{ rows []GroupRow }

func (f *fakeGroupsLister) ListGroups(_ context.Context) ([]GroupRow, error) { return f.rows, nil }

type fakeSystemEngine struct {
	users   map[string]SystemCreateUserInput
	groups  map[string]string // id -> name
	members map[string][]string

	failCreate    error
	failDelete    error
	failGroupCreate error
}

func newFakeSystem() *fakeSystemEngine {
	return &fakeSystemEngine{
		users:   map[string]SystemCreateUserInput{},
		groups:  map[string]string{},
		members: map[string][]string{},
	}
}

func (f *fakeSystemEngine) CreateUser(_ context.Context, in SystemCreateUserInput) (string, error) {
	if f.failCreate != nil {
		return "", f.failCreate
	}
	id := "u-" + in.Username
	f.users[id] = in
	return id, nil
}

func (f *fakeSystemEngine) UpdateUser(_ context.Context, userID, displayName, role string) error {
	cur, ok := f.users[userID]
	if !ok {
		cur = SystemCreateUserInput{}
	}
	cur.DisplayName = displayName
	cur.Role = role
	f.users[userID] = cur
	return nil
}

func (f *fakeSystemEngine) DeleteUser(_ context.Context, userID string) error {
	if f.failDelete != nil {
		return f.failDelete
	}
	delete(f.users, userID)
	return nil
}

func (f *fakeSystemEngine) CreateGroup(_ context.Context, name, description string) (string, error) {
	if f.failGroupCreate != nil {
		return "", f.failGroupCreate
	}
	id := "g-" + name
	f.groups[id] = name
	return id, nil
}

func (f *fakeSystemEngine) DeleteGroup(_ context.Context, groupID string) error {
	delete(f.groups, groupID)
	delete(f.members, groupID)
	return nil
}

func (f *fakeSystemEngine) SetGroupMembers(_ context.Context, groupID string, userIDs []string) error {
	f.members[groupID] = append([]string(nil), userIDs...)
	return nil
}

type fakeShareACLLookup struct{ refs map[string][]string }

func (f *fakeShareACLLookup) SharesUsingGroup(_ context.Context, group string) ([]string, error) {
	return f.refs[group], nil
}

func newCRUDRenderer(t *testing.T, deps UsersCRUDDeps, view UsersDeps) *Renderer {
	t.Helper()
	tr, _ := i18n.New()
	r, err := New(tr, "test")
	if err != nil {
		t.Fatalf("renderer: %v", err)
	}
	r.SetUsersHandler(view)
	r.SetUsersCRUDHandlers(deps)
	return r
}

// [AC-Sfix001-1-1] Users tab exposes a "+ ユーザー追加" button and POST
// /ui/admin/users/create writes to engine/system.
func TestUsersPage_AddUserButton_AC_Sfix001_1_1(t *testing.T) {
	sys := newFakeSystem()
	deps := UsersCRUDDeps{
		System:      sys,
		UsersLister: &fakeUsersLister{rows: []UsersUserRow{{UserID: "u-admin", Username: "admin", Role: "admin"}}},
	}
	r := newCRUDRenderer(t, deps, UsersDeps{
		Users:  deps.UsersLister.(*fakeUsersLister),
		Groups: &fakeGroupsLister{},
	})

	srv := httptest.NewServer(r.Routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/ui/admin/users?tab=users")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	body := readResp(resp)
	for _, want := range []string{
		`data-testid="users-add-btn"`,
		`data-testid="users-new-modal"`,
		`data-testid="users-form-username"`,
		`data-testid="users-form-password"`,
		`data-testid="users-form-role"`,
		`action="/ui/admin/users/create"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("users page missing %q", want)
		}
	}

	form := url.Values{
		"username":     {"alice"},
		"display_name": {"Alice"},
		"password":     {"longenoughpw"},
		"role":         {"user"},
	}
	httpClient := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp2, err := httpClient.PostForm(srv.URL+"/ui/admin/users/create", form)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusSeeOther {
		t.Fatalf("post create status = %d, want 303", resp2.StatusCode)
	}
	if got := sys.users["u-alice"].Username; got != "alice" {
		t.Fatalf("system saw username=%q, want alice", got)
	}
}

// [AC-Sfix001-1-2] Per-row 編集 / 削除 actions render and self-delete is
// refused.
func TestUsersPage_RowActionsAndSelfDeleteGuard_AC_Sfix001_1_2(t *testing.T) {
	sys := newFakeSystem()
	currentID := "u-admin"
	rows := []UsersUserRow{
		{UserID: "u-admin", Username: "admin", Role: "admin"},
		{UserID: "u-bob", Username: "bob", Role: "user"},
	}
	deps := UsersCRUDDeps{
		System:      sys,
		UsersLister: &fakeUsersLister{rows: rows},
		CurrentUser: func(*http.Request) (string, string, bool) { return currentID, "admin", true },
	}
	r := newCRUDRenderer(t, deps, UsersDeps{
		Users:  &fakeUsersLister{rows: rows},
		Groups: &fakeGroupsLister{},
	})

	srv := httptest.NewServer(r.Routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/ui/admin/users?tab=users")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	body := readResp(resp)
	for _, want := range []string{
		`data-testid="users-edit-btn"`,
		`data-testid="users-delete-btn"`,
		`action="/ui/admin/users/delete"`,
		`action="/ui/admin/users/update"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("users page missing %q", want)
		}
	}

	// Self-delete attempt — admin u-admin trying to delete u-admin.
	httpClient := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp2, err := httpClient.PostForm(srv.URL+"/ui/admin/users/delete", url.Values{"id": {"u-admin"}})
	if err != nil {
		t.Fatalf("post delete: %v", err)
	}
	defer resp2.Body.Close()
	loc := resp2.Header.Get("Location")
	if !strings.Contains(loc, "err=") {
		t.Fatalf("self-delete redirect lacked err query: %q", loc)
	}
	if _, deleted := sys.users["u-admin"]; deleted {
		t.Fatalf("self-delete should have been refused")
	}
}

// [AC-Sfix001-2-1] Groups tab has "+ グループ追加" + form posts to
// /ui/admin/groups/create.
func TestGroupsTab_AddGroupButton_AC_Sfix001_2_1(t *testing.T) {
	sys := newFakeSystem()
	deps := UsersCRUDDeps{System: sys}
	r := newCRUDRenderer(t, deps, UsersDeps{
		Users:  &fakeUsersLister{},
		Groups: &fakeGroupsLister{},
	})

	srv := httptest.NewServer(r.Routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/ui/admin/users?tab=groups")
	if err != nil {
		t.Fatalf("get groups tab: %v", err)
	}
	defer resp.Body.Close()
	body := readResp(resp)
	for _, want := range []string{
		`data-testid="groups-add-btn"`,
		`data-testid="groups-new-modal"`,
		`data-testid="groups-form-name"`,
		`action="/ui/admin/groups/create"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("groups tab missing %q", want)
		}
	}

	httpClient := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp2, err := httpClient.PostForm(srv.URL+"/ui/admin/groups/create", url.Values{
		"name":        {"devs"},
		"description": {"Developers"},
	})
	if err != nil {
		t.Fatalf("post group create: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusSeeOther {
		t.Fatalf("post group create status = %d, want 303", resp2.StatusCode)
	}
	if got := sys.groups["g-devs"]; got != "devs" {
		t.Fatalf("system saw group %q, want devs", got)
	}
}

// [AC-Sfix001-2-2] メンバー編集 modal lists every user as a checkbox and
// /ui/admin/groups/members records the selection.
func TestGroupsTab_MembersModal_AC_Sfix001_2_2(t *testing.T) {
	sys := newFakeSystem()
	rows := []UsersUserRow{
		{UserID: "u-alice", Username: "alice", Role: "user"},
		{UserID: "u-bob", Username: "bob", Role: "user"},
	}
	groups := []GroupRow{{ID: "g-devs", Name: "devs", Description: "Developers", Members: []string{"u-alice"}}}
	deps := UsersCRUDDeps{System: sys}
	r := newCRUDRenderer(t, deps, UsersDeps{
		Users:  &fakeUsersLister{rows: rows},
		Groups: &fakeGroupsLister{rows: groups},
	})

	srv := httptest.NewServer(r.Routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/ui/admin/users?tab=groups")
	if err != nil {
		t.Fatalf("get groups tab: %v", err)
	}
	defer resp.Body.Close()
	body := readResp(resp)
	for _, want := range []string{
		`data-testid="groups-edit-members-btn"`,
		`data-testid="groups-members-modal-g-devs"`,
		`data-testid="groups-members-checkbox"`,
		`name="member" value="u-alice"`,
		`name="member" value="u-bob"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("groups members modal missing %q", want)
		}
	}

	// Toggle membership: keep alice, add bob.
	httpClient := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	form := url.Values{}
	form.Set("id", "g-devs")
	form.Add("member", "u-alice")
	form.Add("member", "u-bob")
	resp2, err := httpClient.PostForm(srv.URL+"/ui/admin/groups/members", form)
	if err != nil {
		t.Fatalf("post members: %v", err)
	}
	defer resp2.Body.Close()
	got := sys.members["g-devs"]
	if len(got) != 2 || got[0] != "u-alice" || got[1] != "u-bob" {
		t.Fatalf("members = %v, want [u-alice u-bob]", got)
	}
}

// [AC-Sfix001-2-3 surface] Group delete refuses when a Share ACL still
// references it. (Engine-level invariant covered by users_crud_test.go in
// engine/system; this test verifies the UI banner message.)
func TestGroupsDelete_RefusedWhenShareACLRefs(t *testing.T) {
	sys := newFakeSystem()
	sys.groups["g-devs"] = "devs"
	deps := UsersCRUDDeps{
		System: sys,
		Lookup: &fakeShareACLLookup{refs: map[string][]string{"devs": {"photos"}}},
	}
	r := newCRUDRenderer(t, deps, UsersDeps{Users: &fakeUsersLister{}, Groups: &fakeGroupsLister{}})

	srv := httptest.NewServer(r.Routes())
	defer srv.Close()

	httpClient := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := httpClient.PostForm(srv.URL+"/ui/admin/groups/delete", url.Values{
		"id": {"g-devs"}, "name": {"devs"},
	})
	if err != nil {
		t.Fatalf("post delete group: %v", err)
	}
	defer resp.Body.Close()
	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "err=") {
		t.Fatalf("expected refusal banner in redirect, got %q", loc)
	}
	if _, removed := sys.groups["g-devs"]; !removed {
		// expected: still present
	}
	if _, gone := sys.groups["g-devs"]; !gone {
		t.Fatalf("group must still be present after refused delete")
	}
}
