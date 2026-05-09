// Regression for the "Windows credential cache hits orphan tdbsam user"
// foothold (2026-05-09). KuraOS-managed users live in state.db; tdbsam
// is a downstream projection. Without pruning, distro users like
// `ubuntu` or stale entries from a botched DeleteUser stay
// authenticatable at the SMB layer — Windows clients with a cached
// credential then session-setup as that user and get
// NT_STATUS_ACCESS_DENIED at the share check, which looks like the
// KuraOS user's password is wrong even though it isn't.
package system

import (
	"context"
	"strings"
	"testing"

	"github.com/kuraos-org/kura/internal/cmdexec"
)

// scriptedExec returns a fixed stdout for `pdbedit -L` and records every
// other call. Lets us assert the exact set of `pdbedit -x` invocations
// without booting a real samba.
type scriptedExec struct {
	tdbsamUsers []string
	calls       []cmdexec.FakeCall
}

func (s *scriptedExec) Run(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
	s.calls = append(s.calls, cmdexec.FakeCall{Name: name, Args: append([]string(nil), args...)})
	if name == "pdbedit" && len(args) == 1 && args[0] == "-L" {
		// pdbedit -L format: `username:uid:fullname`
		var b strings.Builder
		for _, u := range s.tdbsamUsers {
			b.WriteString(u)
			b.WriteString(":1000:")
			b.WriteString(u)
			b.WriteString("\n")
		}
		return []byte(b.String()), nil, nil
	}
	return nil, nil, nil
}

func TestPruneTdbsamOrphans_DeletesOnlyOrphans(t *testing.T) {
	cases := []struct {
		name     string
		inTdbsam []string
		keep     []string
		wantDel  []string
	}{
		{
			name:     "Windows-cred-cache scenario: distro ubuntu user must go",
			inTdbsam: []string{"admin", "takumi", "ubuntu"},
			keep:     []string{"admin", "takumi"},
			wantDel:  []string{"ubuntu"},
		},
		{
			name:     "Stale entry from botched DeleteUser is removed",
			inTdbsam: []string{"admin", "alice"},
			keep:     []string{"admin"},
			wantDel:  []string{"alice"},
		},
		{
			name:     "All tdbsam users are KuraOS-managed: no deletes",
			inTdbsam: []string{"admin", "takumi"},
			keep:     []string{"admin", "takumi"},
			wantDel:  nil,
		},
		{
			name:     "Empty SSOT scrubs everything (e.g. fresh restore)",
			inTdbsam: []string{"ubuntu", "leftover"},
			keep:     nil,
			wantDel:  []string{"ubuntu", "leftover"},
		},
		{
			name:     "Empty tdbsam: no calls at all (no-op)",
			inTdbsam: nil,
			keep:     []string{"admin"},
			wantDel:  nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ex := &scriptedExec{tdbsamUsers: tc.inTdbsam}
			pruneTdbsamOrphans(context.Background(), ex, tc.keep)

			var got []string
			for _, c := range ex.calls {
				if c.Name == "pdbedit" && len(c.Args) == 2 && c.Args[0] == "-x" {
					got = append(got, c.Args[1])
				}
			}
			if !equalStringSet(got, tc.wantDel) {
				t.Fatalf("pdbedit -x calls = %v, want %v", got, tc.wantDel)
			}
		})
	}
}

// pdbedit not installed (or returns error) must be soft-failed — the
// caller is Reconcile which already tolerates samba absence in test envs.
func TestPruneTdbsamOrphans_PdbeditMissing_NoPanic(t *testing.T) {
	ex := &errorExec{}
	pruneTdbsamOrphans(context.Background(), ex, []string{"admin"})
	// no assertion needed — the function must just return cleanly.
}

type errorExec struct{}

func (errorExec) Run(_ context.Context, _ string, _ ...string) ([]byte, []byte, error) {
	return nil, nil, context.Canceled
}

func equalStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]int{}
	for _, s := range a {
		m[s]++
	}
	for _, s := range b {
		m[s]--
	}
	for _, v := range m {
		if v != 0 {
			return false
		}
	}
	return true
}
