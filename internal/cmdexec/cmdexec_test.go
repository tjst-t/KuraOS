package cmdexec

import (
	"context"
	"errors"
	"testing"
)

func TestFake_Run(t *testing.T) {
	cases := []struct {
		name       string
		setup      func(*Fake)
		callName   string
		callArgs   []string
		wantStdout string
		wantErr    bool
	}{
		{
			name: "registered call returns canned stdout",
			setup: func(f *Fake) {
				f.RegisterStdout("zpool", []string{"list", "-H", "-p"}, []byte("tank\n"))
			},
			callName:   "zpool",
			callArgs:   []string{"list", "-H", "-p"},
			wantStdout: "tank\n",
		},
		{
			name:     "unregistered call returns error",
			setup:    func(*Fake) {},
			callName: "zpool",
			callArgs: []string{"status"},
			wantErr:  true,
		},
		{
			name: "registered error is propagated",
			setup: func(f *Fake) {
				f.Register("zpool", []string{"import"}, FakeResponse{Err: errors.New("no pools")})
			},
			callName: "zpool",
			callArgs: []string{"import"},
			wantErr:  true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := NewFake()
			tc.setup(f)
			stdout, _, err := f.Run(context.Background(), tc.callName, tc.callArgs...)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(stdout) != tc.wantStdout {
				t.Fatalf("stdout = %q, want %q", stdout, tc.wantStdout)
			}
		})
	}
}

func TestFake_Calls(t *testing.T) {
	f := NewFake()
	f.RegisterStdout("zpool", []string{"list"}, []byte(""))
	f.RegisterStdout("zfs", []string{"list", "-H"}, []byte(""))
	_, _, _ = f.Run(context.Background(), "zpool", "list")
	_, _, _ = f.Run(context.Background(), "zfs", "list", "-H")
	got := f.Calls()
	if len(got) != 2 {
		t.Fatalf("calls len = %d, want 2", len(got))
	}
	if got[0].Name != "zpool" || got[0].Args[0] != "list" {
		t.Fatalf("call[0] = %+v", got[0])
	}
	if got[1].Name != "zfs" || got[1].Args[1] != "-H" {
		t.Fatalf("call[1] = %+v", got[1])
	}
}
