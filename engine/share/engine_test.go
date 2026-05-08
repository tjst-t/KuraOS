package share

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kuraos-org/kura/internal/cmdexec"
	"github.com/kuraos-org/kura/internal/store"
)

func newTestEngine(t *testing.T) (*Manager, *cmdexec.Fake, string, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	st, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	smbPath := filepath.Join(dir, "kura.conf")
	expPath := filepath.Join(dir, "kura.exports")
	fake := cmdexec.NewFake()

	// Standard reload calls succeed by default — individual tests register
	// failures via fake.Register to exercise the error paths.
	fake.RegisterStdout("testparm", []string{"-s", "--suppress-prompt", smbPath}, nil)
	fake.RegisterStdout("systemctl", []string{"reload", "smbd"}, nil)
	fake.RegisterStdout("systemctl", []string{"reload", "nfs-server"}, nil)
	fake.RegisterStdout("exportfs", []string{"-ra"}, nil)

	store := NewStore(st.DB())
	mgr := NewManager(store, fake, Options{
		SMBConfPath:  smbPath,
		ExportsPath:  expPath,
		SystemctlBin: "systemctl",
		TestparmBin:  "testparm",
		ExportfsBin:  "exportfs",
	})
	return mgr, fake, smbPath, expPath
}

// AC-Sd64f38-1-1: Share creation regenerates smb.conf / exports and reloads
// smbd / nfs-server. Verified end-to-end by listing fake.Calls() after
// Manager.Create.
func TestEngineCreateRegenAndReload(t *testing.T) {
	m, fake, smbPath, expPath := newTestEngine(t)
	ctx := context.Background()

	in := CreateInput{
		Name:       "photos",
		Path:       "/tank/photos",
		Protocol:   ProtocolBoth,
		Preset:     PresetGeneral,
		AccessMode: AccessReadWrite,
		ACL:        []ACLEntry{{Kind: PrincipalUser, Name: "alice", Mode: ACLModeReadWrite}},
	}
	if _, err := m.Create(ctx, in); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// File output assertions
	smbBody, err := os.ReadFile(smbPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(smbBody), "[photos]") {
		t.Errorf("smb.conf missing [photos] section:\n%s", string(smbBody))
	}
	if !strings.Contains(string(smbBody), "server multi channel support = yes") {
		t.Errorf("smb.conf missing performance default")
	}

	expBody, err := os.ReadFile(expPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(expBody), "/tank/photos") {
		t.Errorf("exports missing path:\n%s", string(expBody))
	}

	// Reload sequence
	calls := fake.Calls()
	gotPaths := make([]string, 0, len(calls))
	for _, c := range calls {
		gotPaths = append(gotPaths, c.Name+" "+strings.Join(c.Args, " "))
	}
	mustHave := []string{
		"testparm -s --suppress-prompt " + smbPath,
		"systemctl reload smbd",
		"exportfs -ra",
		"systemctl reload nfs-server",
	}
	for _, m := range mustHave {
		if !sliceContains(gotPaths, m) {
			t.Errorf("expected call %q\nactual calls:\n  %s", m, strings.Join(gotPaths, "\n  "))
		}
	}
}

func TestEngineCreateRollsBackOnTestparmFailure(t *testing.T) {
	m, fake, smbPath, _ := newTestEngine(t)
	ctx := context.Background()
	// Override testparm to fail
	fake.Register("testparm", []string{"-s", "--suppress-prompt", smbPath}, cmdexec.FakeResponse{
		Stderr: []byte("Bad config\n"),
		Err:    errors.New("exit 1"),
	})

	in := CreateInput{
		Name:       "photos",
		Path:       "/tank/photos",
		Protocol:   ProtocolSMB,
		Preset:     PresetGeneral,
		AccessMode: AccessReadWrite,
	}
	_, err := m.Create(ctx, in)
	if !errors.Is(err, ErrTestparmFailed) {
		t.Fatalf("Create returned %v, want ErrTestparmFailed", err)
	}
	// Row should not have stuck — list is empty.
	list, err := m.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("share row leaked despite testparm failure: %d rows", len(list))
	}
}

func TestEngineCreateRejectsDuplicateName(t *testing.T) {
	m, _, _, _ := newTestEngine(t)
	ctx := context.Background()
	in := CreateInput{
		Name:       "photos",
		Path:       "/tank/photos",
		Protocol:   ProtocolSMB,
		Preset:     PresetGeneral,
		AccessMode: AccessReadWrite,
	}
	if _, err := m.Create(ctx, in); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	in.Path = "/tank/photos2"
	_, err := m.Create(ctx, in)
	if !errors.Is(err, ErrNameTaken) {
		t.Fatalf("duplicate-name Create returned %v, want ErrNameTaken", err)
	}
}

func TestEngineCreateRejectsNestedPath(t *testing.T) {
	m, _, _, _ := newTestEngine(t)
	ctx := context.Background()
	first := CreateInput{
		Name:       "docs",
		Path:       "/tank/docs",
		Protocol:   ProtocolSMB,
		Preset:     PresetGeneral,
		AccessMode: AccessReadWrite,
	}
	if _, err := m.Create(ctx, first); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	nested := CreateInput{
		Name:       "reports",
		Path:       "/tank/docs/reports",
		Protocol:   ProtocolSMB,
		Preset:     PresetGeneral,
		AccessMode: AccessReadOnly,
	}
	_, err := m.Create(ctx, nested)
	if !errors.Is(err, ErrPathConflict) {
		t.Fatalf("nested-path Create returned %v, want ErrPathConflict", err)
	}
}

func TestEngineDeleteRegenerates(t *testing.T) {
	m, _, smbPath, _ := newTestEngine(t)
	ctx := context.Background()

	created, err := m.Create(ctx, CreateInput{
		Name:       "photos",
		Path:       "/tank/photos",
		Protocol:   ProtocolSMB,
		Preset:     PresetGeneral,
		AccessMode: AccessReadWrite,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// File should still exist but have no [photos] section
	body, err := os.ReadFile(smbPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "[photos]") {
		t.Errorf("[photos] still in smb.conf after delete:\n%s", string(body))
	}
}

func TestEngineApplyIsIdempotent(t *testing.T) {
	m, _, smbPath, _ := newTestEngine(t)
	ctx := context.Background()
	if _, err := m.Create(ctx, CreateInput{
		Name:       "photos",
		Path:       "/tank/photos",
		Protocol:   ProtocolSMB,
		Preset:     PresetGeneral,
		AccessMode: AccessReadWrite,
	}); err != nil {
		t.Fatal(err)
	}
	body1, _ := os.ReadFile(smbPath)
	if err := m.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	body2, _ := os.ReadFile(smbPath)
	if string(body1) != string(body2) {
		t.Errorf("Apply not idempotent\n--- first ---\n%s\n--- second ---\n%s", body1, body2)
	}
}

// fakeChecker accepts paths that begin with /tank/, rejects everything else.
type fakeChecker struct{}

func (fakeChecker) PathBelongsToVolume(_ context.Context, p string) (bool, error) {
	return strings.HasPrefix(p, "/tank/"), nil
}

func TestEngineCreateRejectsPathOutsideVolume(t *testing.T) {
	m, _, _, _ := newTestEngine(t)
	m.checker = fakeChecker{}
	ctx := context.Background()
	_, err := m.Create(ctx, CreateInput{
		Name:       "rogue",
		Path:       "/etc/passwd",
		Protocol:   ProtocolSMB,
		Preset:     PresetGeneral,
		AccessMode: AccessReadOnly,
	})
	if !errors.Is(err, ErrInvalidPath) && !errors.Is(err, ErrPathOutsideVolume) {
		// /etc/passwd is structurally valid but outside volume; we expect the
		// volume-check error specifically.
		t.Fatalf("Create returned %v, want ErrPathOutsideVolume", err)
	}
}

func sliceContains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
