package storage

// Presets bake recordsize / compression / special_small_blocks into named
// bundles. DESIGN_PRINCIPLES priority #2 ("賢いデフォルト > 設定項目を増やす"):
// the operator picks `general` / `media` / `database`, never an SI suffix.
//
// Values are sourced from docs/initial-input/Kuraos-design.md §4.4 and the
// CLAUDE.md sprint scope:
//
//   general  : recordsize=128K, compression=zstd, special_small_blocks=0
//   media    : recordsize=1M,   compression=zstd, special_small_blocks=0
//   database : recordsize=16K,  compression=zstd, special_small_blocks=64K,
//              logbias=latency
//
// design.md table calls media compression `zstd`; CLAUDE.md hint mentions
// `lz4`. Going with the design.md value since that's the authoritative
// document — zstd compresses better and modern hardware absorbs the CPU cost.
// Logged to decisions.json with the rationale.

// Preset is the immutable parameter bundle a PresetID resolves to.
type Preset struct {
	RecordSize         string
	Compression        string
	SpecialSmallBlocks string
	LogBias            string // empty = ZFS default ("latency" only on database)
}

// presetTable is the SSOT for preset values. Adding a new preset means adding
// it here and to ja.json (page.storage.preset.<id>) — the test suite asserts
// every PresetID has a translation.
var presetTable = map[PresetID]Preset{
	PresetGeneral: {
		RecordSize:         "128K",
		Compression:        "zstd",
		SpecialSmallBlocks: "0",
	},
	PresetMedia: {
		RecordSize:         "1M",
		Compression:        "zstd",
		SpecialSmallBlocks: "0",
	},
	PresetDatabase: {
		RecordSize:         "16K",
		Compression:        "zstd",
		SpecialSmallBlocks: "64K",
		LogBias:            "latency",
	},
}

// LookupPreset returns the Preset for id. Unknown id returns ErrPresetUnknown.
func LookupPreset(id PresetID) (Preset, error) {
	p, ok := presetTable[id]
	if !ok {
		return Preset{}, ErrPresetUnknown
	}
	return p, nil
}

// AllPresets returns the registered preset IDs in declaration order. Used by
// the UI to render the picker without hard-coding the list in two places.
func AllPresets() []PresetID {
	return []PresetID{PresetGeneral, PresetMedia, PresetDatabase}
}

// applyPresetToOpts copies the preset values into VolumeOpts when Preset is
// set. Raw fields the caller already filled win — the preset only fills in
// the gaps. This lets the UI submit `Preset=media` while still letting the
// CLI override a single field if needed.
func applyPresetToOpts(opts *VolumeOpts) error {
	if opts.Preset == "" {
		return nil
	}
	p, err := LookupPreset(opts.Preset)
	if err != nil {
		return err
	}
	if opts.RecordSize == "" {
		opts.RecordSize = p.RecordSize
	}
	if opts.Compression == "" {
		opts.Compression = p.Compression
	}
	if opts.SpecialSmallBlocks == "" {
		opts.SpecialSmallBlocks = p.SpecialSmallBlocks
	}
	if opts.LogBias == "" && p.LogBias != "" {
		opts.LogBias = p.LogBias
	}
	return nil
}
