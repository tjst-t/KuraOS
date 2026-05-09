// Regression for the "remove last ACL entry but Windows still connects"
// foothold (2026-05-09). Without the EmptyACLSentinel, an empty ACL
// rendered no `valid users` line, which Samba treats as "any
// authenticated user" — fail-open semantics that contradict the UI's
// implicit contract. The fix renders `valid users = nobody` so the
// share denies everyone until at least one principal is added.
package share

import (
	"context"
	"strings"
	"testing"
)

func TestRender_EmptyACL_DeniesEveryoneViaSentinel(t *testing.T) {
	cases := []struct {
		name  string
		share Share
	}{
		{
			name: "create with no ACL: deny by default",
			share: Share{
				Name:       "vault",
				Path:       "/tank/vault",
				Protocol:   ProtocolSMB,
				Preset:     PresetGeneral,
				AccessMode: AccessReadWrite,
				ACL:        nil,
			},
		},
		{
			name: "edit removes last entry: deny again",
			share: Share{
				Name:       "vault",
				Path:       "/tank/vault",
				Protocol:   ProtocolSMB,
				Preset:     PresetGeneral,
				AccessMode: AccessReadWrite,
				ACL:        []ACLEntry{}, // explicit empty, not nil
			},
		},
		{
			name: "ACL with only deny entries: still no valid users",
			share: Share{
				Name:       "vault",
				Path:       "/tank/vault",
				Protocol:   ProtocolSMB,
				Preset:     PresetGeneral,
				AccessMode: AccessReadWrite,
				ACL: []ACLEntry{
					{Kind: PrincipalUser, Name: "alice", Mode: ACLModeNone},
				},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			view, err := buildSMBView(tc.share)
			if err != nil {
				t.Fatalf("buildSMBView: %v", err)
			}
			if view.ValidUsers != EmptyACLSentinel {
				t.Fatalf("ValidUsers = %q, want %q (deny-by-default)",
					view.ValidUsers, EmptyACLSentinel)
			}
		})
	}
}

func TestRender_NonEmptyACL_DoesNotApplySentinel(t *testing.T) {
	s := Share{
		Name:       "photos",
		Path:       "/tank/photos",
		Protocol:   ProtocolSMB,
		Preset:     PresetGeneral,
		AccessMode: AccessReadWrite,
		ACL: []ACLEntry{
			{Kind: PrincipalUser, Name: "alice", Mode: ACLModeReadWrite},
		},
	}
	view, err := buildSMBView(s)
	if err != nil {
		t.Fatalf("buildSMBView: %v", err)
	}
	if strings.Contains(view.ValidUsers, EmptyACLSentinel) {
		t.Fatalf("ValidUsers contains sentinel %q when ACL has entries: %q",
			EmptyACLSentinel, view.ValidUsers)
	}
	if !strings.Contains(view.ValidUsers, "alice") {
		t.Fatalf("ValidUsers missing alice: %q", view.ValidUsers)
	}
}

// Apply must invoke smbcontrol close-share for every active SMB share so
// that an existing connection (cached by smbd from before the ACL
// change) is severed and the next request re-checks the new ACL.
// Without this, removing a user from the ACL leaves them with full
// access on the connection they already had open.
func TestApply_KicksExistingConnections(t *testing.T) {
	mgr, fake, _, _ := newTestEngine(t)
	ctx := context.Background()
	if _, err := mgr.Create(ctx, CreateInput{
		Name:       "photos",
		Path:       "/tank/photos",
		Protocol:   ProtocolSMB,
		Preset:     PresetGeneral,
		AccessMode: AccessReadWrite,
		ACL: []ACLEntry{
			{Kind: PrincipalUser, Name: "alice", Mode: ACLModeReadWrite},
		},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	found := false
	for _, c := range fake.Calls() {
		if c.Name == "smbcontrol" && len(c.Args) >= 3 &&
			c.Args[0] == "smbd" && c.Args[1] == "close-share" && c.Args[2] == "photos" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Apply did not call `smbcontrol smbd close-share photos`. Calls: %v", fake.Calls())
	}
}
