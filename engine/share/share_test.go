package share

import (
	"errors"
	"strings"
	"testing"
)

func TestProtocolValid(t *testing.T) {
	cases := []struct {
		p    Protocol
		want bool
	}{
		{ProtocolSMB, true},
		{ProtocolNFS, true},
		{ProtocolBoth, true},
		{Protocol("ftp"), false},
		{Protocol(""), false},
	}
	for _, c := range cases {
		t.Run(string(c.p), func(t *testing.T) {
			if got := c.p.Valid(); got != c.want {
				t.Fatalf("Protocol(%q).Valid() = %v, want %v", c.p, got, c.want)
			}
		})
	}
}

func TestProtocolHasSMBNFS(t *testing.T) {
	cases := []struct {
		p        Protocol
		smb, nfs bool
	}{
		{ProtocolSMB, true, false},
		{ProtocolNFS, false, true},
		{ProtocolBoth, true, true},
	}
	for _, c := range cases {
		t.Run(string(c.p), func(t *testing.T) {
			if c.p.HasSMB() != c.smb {
				t.Errorf("HasSMB = %v want %v", c.p.HasSMB(), c.smb)
			}
			if c.p.HasNFS() != c.nfs {
				t.Errorf("HasNFS = %v want %v", c.p.HasNFS(), c.nfs)
			}
		})
	}
}

func TestPresetValid(t *testing.T) {
	for _, p := range AllPresets() {
		if !p.Valid() {
			t.Errorf("%q in AllPresets but Valid() = false", p)
		}
	}
	if Preset("custom").Valid() {
		t.Error(`Preset("custom").Valid() = true, want false`)
	}
}

func TestLookupPreset(t *testing.T) {
	for _, id := range AllPresets() {
		t.Run(string(id), func(t *testing.T) {
			p, err := LookupPreset(id)
			if err != nil {
				t.Fatalf("LookupPreset(%q): %v", id, err)
			}
			if p.MinProtocol != "SMB2" {
				t.Errorf("preset %q MinProtocol = %q, want SMB2", id, p.MinProtocol)
			}
			if p.AIOReadSize != "1" || p.AIOWriteSize != "1" {
				t.Errorf("preset %q aio defaults missing: read=%q write=%q", id, p.AIOReadSize, p.AIOWriteSize)
			}
		})
	}
	if _, err := LookupPreset(Preset("xyz")); !errors.Is(err, ErrInvalidPreset) {
		t.Errorf("LookupPreset(unknown) returned %v, want ErrInvalidPreset", err)
	}
}

func TestPresetParamsTimeMachine(t *testing.T) {
	p, err := LookupPreset(PresetTimeMachine)
	if err != nil {
		t.Fatal(err)
	}
	if p.FruitTimeMachine != "yes" {
		t.Errorf("time_machine preset must enable fruit:time machine, got %q", p.FruitTimeMachine)
	}
	if !strings.Contains(p.VfsObjects, "fruit") {
		t.Errorf("time_machine preset must include fruit vfs object, got %q", p.VfsObjects)
	}
}

func TestPresetParamsDatabase(t *testing.T) {
	p, err := LookupPreset(PresetDatabase)
	if err != nil {
		t.Fatal(err)
	}
	if p.Oplocks != "no" {
		t.Errorf("database preset must disable oplocks, got %q", p.Oplocks)
	}
	if p.Sync != "always" {
		t.Errorf("database preset must force sync=always, got %q", p.Sync)
	}
}

func TestCreateInputValidate(t *testing.T) {
	base := CreateInput{
		Name:       "photos",
		Path:       "/tank/photos",
		Protocol:   ProtocolSMB,
		Preset:     PresetGeneral,
		AccessMode: AccessReadWrite,
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("base valid input rejected: %v", err)
	}

	cases := []struct {
		name  string
		mut   func(*CreateInput)
		errIs error
	}{
		{"empty name", func(in *CreateInput) { in.Name = "" }, ErrInvalidName},
		{"name with space", func(in *CreateInput) { in.Name = "my share" }, ErrInvalidName},
		{"name with quote", func(in *CreateInput) { in.Name = `bad"name` }, ErrInvalidName},
		{"relative path", func(in *CreateInput) { in.Path = "tank/photos" }, ErrInvalidPath},
		{"path with traversal", func(in *CreateInput) { in.Path = "/tank/../etc" }, ErrInvalidPath},
		{"unknown protocol", func(in *CreateInput) { in.Protocol = "ftp" }, ErrInvalidProtocol},
		{"unknown preset", func(in *CreateInput) { in.Preset = "custom" }, ErrInvalidPreset},
		{"unknown access mode", func(in *CreateInput) { in.AccessMode = "exec" }, ErrInvalidAccessMode},
		{"acl bad mode", func(in *CreateInput) {
			in.ACL = []ACLEntry{{Kind: PrincipalUser, Name: "alice", Mode: "exec"}}
		}, ErrInvalidACL},
		{"acl bad kind", func(in *CreateInput) {
			in.ACL = []ACLEntry{{Kind: "service", Name: "alice", Mode: ACLModeReadWrite}}
		}, ErrInvalidACL},
		{"acl empty name", func(in *CreateInput) {
			in.ACL = []ACLEntry{{Kind: PrincipalUser, Name: "", Mode: ACLModeReadWrite}}
		}, ErrInvalidACL},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := base
			c.mut(&in)
			err := in.Validate()
			if !errors.Is(err, c.errIs) {
				t.Fatalf("got %v, want errors.Is(_, %v)", err, c.errIs)
			}
		})
	}
}

func TestCheckPathConflict(t *testing.T) {
	existing := []Share{
		{Name: "photos", Path: "/tank/photos"},
		{Name: "docs", Path: "/tank/docs"},
	}
	cases := []struct {
		name  string
		path  string
		errIs error
	}{
		{"unique", "/tank/media", nil},
		{"exact dup", "/tank/photos", ErrPathConflict},
		{"nested under existing", "/tank/photos/2024", ErrPathConflict},
		{"would contain existing", "/tank", ErrPathConflict},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkPathConflict(c.path, existing)
			if c.errIs == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, c.errIs) {
				t.Fatalf("got %v, want errors.Is(_, %v)", err, c.errIs)
			}
		})
	}
}
