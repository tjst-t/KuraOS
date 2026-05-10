// shares_acl_picker_test.go covers Sfix001-3: the row-form ACL picker
// that replaces the plaintext `acl` input in the Share create / edit
// modals.
//
//   AC-Sfix001-3-3 — user / group dropdowns are populated from
//                    PrincipalSource (= GET /api/users + /api/groups).
//
// E2E coverage of AC-3-1 / AC-3-2 (visual row layout, prefill from
// existing ACL) lives in tests/e2e/shares-acl-picker.e2e.spec.ts.
package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/kuraos-org/kura/engine/share"
)

type fakePrincipalSource struct {
	users, groups []string
}

func (f *fakePrincipalSource) ListUsernames(_ context.Context) ([]string, error) {
	return f.users, nil
}
func (f *fakePrincipalSource) ListGroupNames(_ context.Context) ([]string, error) {
	return f.groups, nil
}

// [AC-Sfix001-3-3] PrincipalSource feeds the picker dropdowns.
func TestSharesACLPicker_RendersDropdownsFromPrincipalSource(t *testing.T) {
	r := newSharesRenderer(t)
	eng := &stubSharesEngine{}
	src := &fakePrincipalSource{users: []string{"alice", "bob"}, groups: []string{"family", "devs"}}
	deps := SharesDeps{Engine: eng, PrincipalSource: src}
	h := r.SharesHandler(deps)

	req := httptest.NewRequest(http.MethodGet, "/ui/admin/shares", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	body := w.Body.String()
	for _, want := range []string{
		`data-testid="shares-acl-rows"`,
		`data-testid="shares-acl-add-btn"`,
		`hx-get="/ui/admin/shares/acl-row"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("shares page missing %q", want)
		}
	}

	// Default row (no acl_kind query) → kind defaults to "user", so the
	// name dropdown lists only users. Groups must be hidden (the kind
	// toggle re-fetches with acl_kind=group to surface them). This is
	// the refine fix from 2026-05-10: the middle dropdown was confusing
	// when it showed both users and groups regardless of left selector.
	rowReq := httptest.NewRequest(http.MethodGet, "/ui/admin/shares/acl-row", nil)
	rowW := httptest.NewRecorder()
	r.SharesACLRowHandler(deps).ServeHTTP(rowW, rowReq)
	if rowW.Code != http.StatusOK {
		t.Fatalf("acl-row status = %d", rowW.Code)
	}
	rowBody := rowW.Body.String()
	for _, want := range []string{
		`data-testid="shares-acl-row"`,
		`data-testid="shares-acl-kind"`,
		`data-testid="shares-acl-name"`,
		`data-testid="shares-acl-mode"`,
		`value="alice"`,
		`value="bob"`,
		`value="rw"`,
		`value="r"`,
		// kind toggle wires htmx so the name list refreshes on change
		`hx-get="/ui/admin/shares/acl-row"`,
	} {
		if !strings.Contains(rowBody, want) {
			t.Fatalf("acl-row missing %q\nbody: %s", want, rowBody)
		}
	}
	// Groups must NOT appear when the row is rendered with kind=user
	// (default). Otherwise we're back to the confusing both-kinds list.
	for _, unwanted := range []string{
		`value="family"`,
		`value="devs"`,
	} {
		if strings.Contains(rowBody, unwanted) {
			t.Fatalf("acl-row default (kind=user) leaked group option %q\nbody: %s", unwanted, rowBody)
		}
	}

	// Toggle to kind=group — only groups should appear, no users.
	rowReq = httptest.NewRequest(http.MethodGet, "/ui/admin/shares/acl-row?acl_kind=group", nil)
	rowW = httptest.NewRecorder()
	r.SharesACLRowHandler(deps).ServeHTTP(rowW, rowReq)
	rowBody = rowW.Body.String()
	for _, want := range []string{`value="family"`, `value="devs"`} {
		if !strings.Contains(rowBody, want) {
			t.Fatalf("acl-row(kind=group) missing %q\nbody: %s", want, rowBody)
		}
	}
	for _, unwanted := range []string{`value="alice"`, `value="bob"`} {
		if strings.Contains(rowBody, unwanted) {
			t.Fatalf("acl-row(kind=group) leaked user option %q\nbody: %s", unwanted, rowBody)
		}
	}
}

// parseACLForm reads the row-form fields and produces ACLEntry slices.
// Empty rows are dropped, and the legacy CSV input still works as a
// fallback for older callers.
func TestParseACLForm_RowFieldsAndLegacyCSV(t *testing.T) {
	cases := []struct {
		name string
		form url.Values
		want []share.ACLEntry
	}{
		{
			name: "row form three entries",
			form: url.Values{
				"acl_kind": {"user", "group", "user"},
				"acl_name": {"alice", "family", "bob"},
				"acl_mode": {"rw", "r", "rw"},
			},
			want: []share.ACLEntry{
				{Kind: share.PrincipalUser, Name: "alice", Mode: share.ACLModeReadWrite},
				{Kind: share.PrincipalGroup, Name: "family", Mode: share.ACLModeRead},
				{Kind: share.PrincipalUser, Name: "bob", Mode: share.ACLModeReadWrite},
			},
		},
		{
			name: "row form drops empty name rows",
			form: url.Values{
				"acl_kind": {"user", "user"},
				"acl_name": {"", "alice"},
				"acl_mode": {"rw", "rw"},
			},
			want: []share.ACLEntry{
				{Kind: share.PrincipalUser, Name: "alice", Mode: share.ACLModeReadWrite},
			},
		},
		{
			name: "legacy csv fallback when no row fields present",
			form: url.Values{"acl": {"user:alice:rw, group:family:r"}},
			want: []share.ACLEntry{
				{Kind: share.PrincipalUser, Name: "alice", Mode: share.ACLModeReadWrite},
				{Kind: share.PrincipalGroup, Name: "family", Mode: share.ACLModeRead},
			},
		},
		{
			name: "empty form returns nil",
			form: url.Values{},
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseACLForm(tc.form)
			if len(got) != len(tc.want) {
				t.Fatalf("parseACLForm length = %d, want %d (%v)", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("entry %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// Edit modal pre-fills the picker from the existing share's ACL.
func TestSharesACLPicker_EditModalPreFillsExistingACL(t *testing.T) {
	r := newSharesRenderer(t)
	eng := &stubSharesEngine{
		listed: []share.Share{{
			ID: "id-photos", Name: "photos", Path: "/tank/photos",
			Protocol: share.ProtocolSMB, Preset: share.PresetGeneral, AccessMode: share.AccessReadWrite,
			ACL: []share.ACLEntry{
				{Kind: share.PrincipalUser, Name: "alice", Mode: share.ACLModeReadWrite},
				{Kind: share.PrincipalGroup, Name: "family", Mode: share.ACLModeRead},
			},
		}},
	}
	src := &fakePrincipalSource{users: []string{"alice", "bob"}, groups: []string{"family", "devs"}}
	deps := SharesDeps{Engine: eng, PrincipalSource: src}
	h := r.SharesHandler(deps)

	req := httptest.NewRequest(http.MethodGet, "/ui/admin/shares?selected=id-photos", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	body := w.Body.String()
	for _, want := range []string{
		`data-testid="shares-edit-acl-rows"`,
		`data-testid="shares-edit-acl-add-btn"`,
		// Pre-filled rows must show the selected name as <option ... selected>.
		`<option value="alice" selected>alice</option>`,
		`<option value="family" selected>family</option>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("edit modal missing %q\n--snippet--\n%s", want, snippet(body, want))
		}
	}
}

// snippet returns 200 chars of body around the closest match of `marker`.
// When marker is absent it returns the start of body. Test-helper only.
func snippet(body, marker string) string {
	probe := marker
	if len(probe) > 20 {
		probe = probe[:20]
	}
	idx := strings.Index(body, probe)
	if idx < 0 {
		if len(body) < 200 {
			return body
		}
		return body[:200]
	}
	from := idx - 50
	if from < 0 {
		from = 0
	}
	to := idx + 200
	if to > len(body) {
		to = len(body)
	}
	return body[from:to]
}
