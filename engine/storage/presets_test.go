package storage

import (
	"errors"
	"testing"
)

func TestLookupPreset(t *testing.T) {
	cases := []struct {
		id   PresetID
		want Preset
	}{
		{PresetGeneral, Preset{RecordSize: "128K", Compression: "zstd", SpecialSmallBlocks: "0"}},
		{PresetMedia, Preset{RecordSize: "1M", Compression: "zstd", SpecialSmallBlocks: "0"}},
		{PresetDatabase, Preset{RecordSize: "16K", Compression: "zstd", SpecialSmallBlocks: "64K", LogBias: "latency"}},
	}
	for _, tc := range cases {
		t.Run(string(tc.id), func(t *testing.T) {
			got, err := LookupPreset(tc.id)
			if err != nil {
				t.Fatalf("LookupPreset(%q): %v", tc.id, err)
			}
			if got != tc.want {
				t.Errorf("LookupPreset(%q) = %+v, want %+v", tc.id, got, tc.want)
			}
		})
	}
}

func TestLookupPreset_unknown(t *testing.T) {
	if _, err := LookupPreset(PresetID("nope")); !errors.Is(err, ErrPresetUnknown) {
		t.Errorf("want ErrPresetUnknown, got %v", err)
	}
}

func TestApplyPresetToOpts_idempotentWithExplicitOverride(t *testing.T) {
	opts := VolumeOpts{Preset: PresetMedia, RecordSize: "256K"}
	if err := applyPresetToOpts(&opts); err != nil {
		t.Fatalf("applyPresetToOpts: %v", err)
	}
	if opts.RecordSize != "256K" {
		t.Errorf("explicit RecordSize lost: %q", opts.RecordSize)
	}
	if opts.Compression != "zstd" {
		t.Errorf("preset Compression not applied: %q", opts.Compression)
	}
}
