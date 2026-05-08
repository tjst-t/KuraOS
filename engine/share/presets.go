package share

// PresetParams is the SMB option bundle a Preset resolves to. NFS does not
// branch on preset in v1 — every NFS export uses the same options (rw/ro
// already covered by AccessMode + ACL).
//
// Field naming uses the smb.conf option name verbatim so a developer reading
// the table can correlate with samba documentation without an extra mental
// hop. The values are sourced from docs/initial-input/Kuraos-design.md and
// the sprint hint in CLAUDE.md.
type PresetParams struct {
	// VfsObjects is the comma-or-space-joined list of vfs modules. The
	// template renders it as `vfs objects = catia fruit streams_xattr` etc.
	VfsObjects string

	// Oplocks toggles smbd's opportunistic locking. "no" for database to
	// avoid silent corruption when two clients race.
	Oplocks string // "yes" | "no"

	// StrictLocking forces byte-range lock semantics through to the
	// underlying filesystem — needed by sqlite-style apps.
	StrictLocking string // "" (default) | "auto" | "yes" | "no"

	// Sync controls write durability. Empty leaves smbd's default; "always"
	// forces sync on every write (database).
	Sync string // "" | "always"

	// FruitTimeMachine, FruitMetadata, FruitPosixRename are the macOS / netatalk
	// shims. Only set when fruit is in VfsObjects.
	FruitTimeMachine string // "" | "yes"
	FruitMetadata    string // "" | "stream"
	FruitPosixRename string // "" | "yes"

	// VetoFiles silences common macOS / Windows litter (`.DS_Store`, `Thumbs.db`).
	VetoFiles string

	// MinProtocol is the lowest SMB dialect the server will accept. We pin
	// SMB2 — SMB1 is deprecated and disabled in modern Samba builds.
	MinProtocol string // "SMB2"

	// AIOReadSize / AIOWriteSize enable async I/O. "1" means "anything bigger
	// than 1 byte uses aio", which is effectively always-on.
	AIOReadSize  string // "1"
	AIOWriteSize string // "1"

	// CaseSensitive — most preset stays "auto"; database uses "yes" so we
	// don't fold names. Time Machine clients prefer "yes" too.
	CaseSensitive string // "" | "auto" | "yes" | "no"
}

// PresetTable is the SSOT mapping. Adding a preset means adding a row here
// AND adding the corresponding i18n string in ja.json — tests assert the
// translation exists.
var presetTable = map[Preset]PresetParams{
	PresetGeneral: {
		VfsObjects:    "catia streams_xattr",
		Oplocks:       "yes",
		MinProtocol:   "SMB2",
		AIOReadSize:   "1",
		AIOWriteSize:  "1",
		VetoFiles:     "/.DS_Store/.AppleDouble/.AppleDB/.AppleDesktop/Thumbs.db/",
		CaseSensitive: "auto",
	},
	PresetMedia: {
		VfsObjects:       "catia fruit streams_xattr",
		Oplocks:          "yes",
		MinProtocol:      "SMB2",
		AIOReadSize:      "1",
		AIOWriteSize:     "1",
		FruitMetadata:    "stream",
		FruitPosixRename: "yes",
		VetoFiles:        "/.DS_Store/.AppleDouble/Thumbs.db/",
		CaseSensitive:    "auto",
	},
	PresetTimeMachine: {
		VfsObjects:       "catia fruit streams_xattr",
		Oplocks:          "yes",
		MinProtocol:      "SMB2",
		AIOReadSize:      "1",
		AIOWriteSize:     "1",
		FruitTimeMachine: "yes",
		FruitMetadata:    "stream",
		FruitPosixRename: "yes",
		CaseSensitive:    "yes",
	},
	PresetDatabase: {
		VfsObjects:    "catia",
		Oplocks:       "no",
		StrictLocking: "auto",
		Sync:          "always",
		MinProtocol:   "SMB2",
		AIOReadSize:   "1",
		AIOWriteSize:  "1",
		CaseSensitive: "yes",
	},
}

// LookupPreset returns the parameters for p or ErrInvalidPreset.
func LookupPreset(p Preset) (PresetParams, error) {
	v, ok := presetTable[p]
	if !ok {
		return PresetParams{}, ErrInvalidPreset
	}
	return v, nil
}

// AllPresets returns the v1 preset IDs in the canonical order the UI renders
// them in the form. Order matters for the prototype's preset selector.
func AllPresets() []Preset {
	return []Preset{PresetGeneral, PresetMedia, PresetTimeMachine, PresetDatabase}
}
