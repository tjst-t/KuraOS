package ui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/kuraos-org/kura/engine/share"
	"github.com/kuraos-org/kura/i18n"
)

// stubSharesEngine is the in-memory ShareEngine used by the UI tests.
// It records Create / Delete calls and returns canned errors when set.
type stubSharesEngine struct {
	listed    []share.Share
	created   []share.CreateInput
	updated   []struct {
		ID    string
		Input share.UpdateInput
	}
	deleted   []string
	createErr error
	updateErr error
}

func (s *stubSharesEngine) List(_ context.Context) ([]share.Share, error) { return s.listed, nil }
func (s *stubSharesEngine) Get(_ context.Context, id string) (share.Share, error) {
	for _, sh := range s.listed {
		if sh.ID == id {
			return sh, nil
		}
	}
	return share.Share{}, share.ErrShareNotFound
}
func (s *stubSharesEngine) Create(_ context.Context, in share.CreateInput) (share.Share, error) {
	s.created = append(s.created, in)
	if s.createErr != nil {
		return share.Share{}, s.createErr
	}
	out := share.Share{
		ID: "id-" + in.Name, Name: in.Name, Path: in.Path,
		Protocol: in.Protocol, Preset: in.Preset, AccessMode: in.AccessMode,
		ACL: in.ACL,
	}
	s.listed = append(s.listed, out)
	return out, nil
}
func (s *stubSharesEngine) Update(_ context.Context, id string, in share.UpdateInput) (share.Share, error) {
	s.updated = append(s.updated, struct {
		ID    string
		Input share.UpdateInput
	}{id, in})
	if s.updateErr != nil {
		return share.Share{}, s.updateErr
	}
	for i, sh := range s.listed {
		if sh.ID == id {
			s.listed[i].Protocol = in.Protocol
			s.listed[i].Preset = in.Preset
			s.listed[i].AccessMode = in.AccessMode
			s.listed[i].Description = in.Description
			s.listed[i].Disabled = in.Disabled
			s.listed[i].ACL = append([]share.ACLEntry(nil), in.ACL...)
			return s.listed[i], nil
		}
	}
	return share.Share{}, share.ErrShareNotFound
}
func (s *stubSharesEngine) Delete(_ context.Context, id string) error {
	s.deleted = append(s.deleted, id)
	return nil
}

func newSharesRenderer(t *testing.T) *Renderer {
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

func TestSharesGetEmpty(t *testing.T) {
	r := newSharesRenderer(t)
	eng := &stubSharesEngine{}
	h := r.SharesHandler(SharesDeps{Engine: eng})

	req := httptest.NewRequest(http.MethodGet, "/ui/admin/shares", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", w.Code)
	}
	body := w.Body.String()
	must := []string{
		`data-testid="shares-empty"`,
		`data-testid="shares-defaults"`,
		"server multi channel support",
		"use sendfile",
	}
	for _, s := range must {
		if !strings.Contains(body, s) {
			t.Errorf("body missing %q\n--- body ---\n%s", s, body)
		}
	}
}

// AC-Sd64f38-2-1: Share creation form shows preset choices that map to
// internal Volume / SMB defaults — verified by checking the rendered HTML.
func TestSharesGetRendersPresetSelector(t *testing.T) {
	r := newSharesRenderer(t)
	eng := &stubSharesEngine{}
	h := r.SharesHandler(SharesDeps{Engine: eng})

	req := httptest.NewRequest(http.MethodGet, "/ui/admin/shares", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	body := w.Body.String()
	for _, p := range []string{"general", "media", "time_machine", "database"} {
		if !strings.Contains(body, `data-testid="shares-form-preset-`+p+`"`) {
			t.Errorf("preset %q not rendered as radio option in form", p)
		}
	}
	// Description / hint must be present so user picks based on use case
	// without seeing raw smb.conf options.
	for _, hint := range []string{"写真", "Time Machine", "オフィス向け"} {
		if !strings.Contains(body, hint) {
			t.Errorf("preset hint %q missing from form", hint)
		}
	}
}

func TestSharesPostCreatesAndRedirects(t *testing.T) {
	r := newSharesRenderer(t)
	eng := &stubSharesEngine{}
	h := r.SharesHandler(SharesDeps{Engine: eng})

	form := url.Values{
		"name":        {"photos"},
		"path":        {"/tank/photos"},
		"protocol":    {"smb"},
		"preset":      {"general"},
		"access_mode": {"read_write"},
		"acl":         {"user:alice:rw, group:family:r"},
		"description": {"family photos"},
	}
	req := httptest.NewRequest(http.MethodPost, "/ui/admin/shares", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status %d, want 303", w.Code)
	}
	if got := w.Header().Get("Location"); got != "/ui/admin/shares" {
		t.Fatalf("Location = %q, want /ui/admin/shares", got)
	}
	if len(eng.created) != 1 {
		t.Fatalf("expected 1 Create, got %d", len(eng.created))
	}
	got := eng.created[0]
	if got.Name != "photos" || got.Path != "/tank/photos" || got.Protocol != share.ProtocolSMB || got.Preset != share.PresetGeneral {
		t.Errorf("Create input wrong: %+v", got)
	}
	if len(got.ACL) != 2 {
		t.Errorf("expected 2 ACL entries, got %v", got.ACL)
	}
	if got.ACL[0].Mode != share.ACLModeReadWrite {
		t.Errorf("first ACL mode = %q, want rw", got.ACL[0].Mode)
	}
}

func TestSharesPostShowsTranslatedError(t *testing.T) {
	r := newSharesRenderer(t)
	eng := &stubSharesEngine{createErr: share.ErrNameTaken}
	h := r.SharesHandler(SharesDeps{Engine: eng})

	form := url.Values{
		"name": {"photos"}, "path": {"/tank/photos"},
		"protocol": {"smb"}, "preset": {"general"}, "access_mode": {"read_write"},
	}
	req := httptest.NewRequest(http.MethodPost, "/ui/admin/shares", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 (form re-rendered)", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="shares-error"`) {
		t.Errorf("error banner missing\n--- body ---\n%s", body)
	}
	// Translated copy from ja.json: "その共有名はすでに使われています"
	if !strings.Contains(body, "すでに使われて") {
		t.Errorf("translated error message missing\n--- body ---\n%s", body)
	}
}

func TestSharesDeleteRedirects(t *testing.T) {
	r := newSharesRenderer(t)
	eng := &stubSharesEngine{
		listed: []share.Share{{ID: "abc", Name: "photos", Path: "/tank/photos"}},
	}
	h := r.SharesDeleteHandler(SharesDeps{Engine: eng})

	form := url.Values{"id": {"abc"}}
	req := httptest.NewRequest(http.MethodPost, "/ui/admin/shares/delete", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status %d, want 303", w.Code)
	}
	if len(eng.deleted) != 1 || eng.deleted[0] != "abc" {
		t.Errorf("Delete not invoked: %v", eng.deleted)
	}
}

// AC-Sd64f38-1-2 indirectly: page renders the read-only "best defaults"
// table — proves the UI does NOT expose raw SMB knobs as form fields.
func TestSharesPageDoesNotExposeSMBKnobsAsFormFields(t *testing.T) {
	r := newSharesRenderer(t)
	eng := &stubSharesEngine{}
	h := r.SharesHandler(SharesDeps{Engine: eng})

	req := httptest.NewRequest(http.MethodGet, "/ui/admin/shares", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	body := w.Body.String()
	// The form must NOT contain editable inputs for these — they should only
	// appear inside the read-only "shares-defaults" card.
	mustNotBeFormFields := []string{
		`name="vfs_objects"`, `name="oplocks"`, `name="fruit_metadata"`,
		`name="server_multi_channel_support"`, `name="use_sendfile"`,
	}
	for _, s := range mustNotBeFormFields {
		if strings.Contains(body, s) {
			t.Errorf("form must not expose SMB knob: found %q", s)
		}
	}
}

func TestParseACLCSV(t *testing.T) {
	cases := []struct {
		in   string
		want []share.ACLEntry
	}{
		{"", nil},
		{"user:alice:rw", []share.ACLEntry{{Kind: share.PrincipalUser, Name: "alice", Mode: share.ACLModeReadWrite}}},
		{"user:alice:rw, group:family:r", []share.ACLEntry{
			{Kind: share.PrincipalUser, Name: "alice", Mode: share.ACLModeReadWrite},
			{Kind: share.PrincipalGroup, Name: "family", Mode: share.ACLModeRead},
		}},
		{"badtoken, user:bob:r", []share.ACLEntry{
			{Kind: share.PrincipalUser, Name: "bob", Mode: share.ACLModeRead},
		}},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := parseACLCSV(c.in)
			if !sameACL(got, c.want) {
				t.Errorf("parseACLCSV(%q) = %+v, want %+v", c.in, got, c.want)
			}
		})
	}
}

func sameACL(a, b []share.ACLEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// silence unused-error import when the file structure changes.
var _ = errors.Is
