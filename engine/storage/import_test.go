package storage

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/kuraos-org/kura/internal/cmdexec"
)

func TestBuildImportArgs(t *testing.T) {
	cases := []struct {
		name   string
		target string
		opts   ImportOpts
		dryRun bool
		want   []string
	}{
		{
			name:   "default (no flags)",
			target: "archive",
			want:   []string{"import", "-d", "/dev/disk/by-id", "archive"},
		},
		{
			name:   "dry-run -N",
			target: "archive",
			dryRun: true,
			want:   []string{"import", "-d", "/dev/disk/by-id", "-N", "archive"},
		},
		{
			name:   "force",
			target: "archive",
			opts:   ImportOpts{Force: true},
			want:   []string{"import", "-d", "/dev/disk/by-id", "-f", "archive"},
		},
		{
			name:   "readonly + altroot",
			target: "archive",
			opts:   ImportOpts{ReadOnly: true, AltRoot: "/mnt/inspect"},
			want:   []string{"import", "-d", "/dev/disk/by-id", "-o", "readonly=on", "-R", "/mnt/inspect", "archive"},
		},
		{
			name:   "by GUID",
			target: "9999",
			want:   []string{"import", "-d", "/dev/disk/by-id", "9999"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildImportArgs(tc.target, tc.opts, tc.dryRun)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("argv mismatch:\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

func TestImportPool_runsDryRunBeforeActual(t *testing.T) {
	fake := cmdexec.NewFake()
	dryRun := []string{"import", "-d", "/dev/disk/by-id", "-N", "archive"}
	actual := []string{"import", "-d", "/dev/disk/by-id", "archive"}
	fake.RegisterStdout("zpool", dryRun, []byte(""))
	fake.RegisterStdout("zpool", actual, []byte(""))

	c := NewCLI(fake)
	if err := c.ImportPool(context.Background(), "archive", ImportOpts{}); err != nil {
		t.Fatalf("ImportPool: %v", err)
	}
	calls := fake.Calls()
	if len(calls) != 2 {
		t.Fatalf("want 2 calls (dry-run + actual), got %d", len(calls))
	}
	if !reflect.DeepEqual(calls[0].Args, dryRun) {
		t.Errorf("first call should be dry-run, got %q", calls[0].Args)
	}
	if !reflect.DeepEqual(calls[1].Args, actual) {
		t.Errorf("second call should be actual import, got %q", calls[1].Args)
	}
}

func TestImportPool_invalidName(t *testing.T) {
	fake := cmdexec.NewFake()
	c := NewCLI(fake)
	if err := c.ImportPool(context.Background(), "1bad", ImportOpts{}); !errors.Is(err, ErrPoolNameInvalid) {
		t.Errorf("want ErrPoolNameInvalid, got %v", err)
	}
	if calls := fake.Calls(); len(calls) != 0 {
		t.Errorf("want 0 calls when name invalid, got %d", len(calls))
	}
}
