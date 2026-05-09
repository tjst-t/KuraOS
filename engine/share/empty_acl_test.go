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

// Apply must restart (not reload) smbd so existing Windows SMB sessions
// are forced to re-do session setup + tree connect against the new ACL.
// SIGHUP-based reload alone is insufficient: empirically (VM test
// 2026-05-09) Windows kept using its existing session with cached share
// authority even after `smbcontrol smbd close-share`. Only a full smbd
// restart forces the fresh re-evaluation. Brief disconnect for unrelated
// shares is acceptable on a home NAS where Apply runs only on
// operator-driven config changes.
func TestApply_RestartsSmbdToFlushSessionState(t *testing.T) {
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
	for _, c := range fake.Calls() {
		if c.Name == "systemctl" && len(c.Args) == 2 &&
			c.Args[0] == "restart" && c.Args[1] == "smbd" {
			return
		}
		if c.Name == "systemctl" && len(c.Args) == 2 &&
			c.Args[0] == "reload" && c.Args[1] == "smbd" {
			t.Fatalf("Apply used reload smbd; restart is required so Windows clients see new ACL")
		}
	}
	t.Fatalf("Apply did not call `systemctl restart smbd`. Calls: %v", fake.Calls())
}
