// Package smb renders /etc/samba/conf.d/kura.conf from the share.Engine
// state. The template embedded here bakes in the v1 performance / compat
// defaults (server multi channel support, use sendfile, smb2+, vfs objects,
// fruit:*) so the UI never has to expose those knobs (DESIGN_PRINCIPLES
// priority #2: 賢いデフォルト > 設定項目を増やす).
//
// Inputs to RenderConfig come from the engine, not directly from SQLite —
// the engine pre-resolves preset → PresetParams and ACL → valid users /
// write list / read list strings so the template stays declarative.
package smb

import (
	"bytes"
	"embed"
	"fmt"
	"sort"
	"strings"
	"text/template"
)

//go:embed templates/smb.conf.tmpl
var templatesFS embed.FS

// DefaultWorkgroup is the smb.conf [global] workgroup. We pick "WORKGROUP"
// (Windows default) so unconfigured macOS / Windows clients see the share
// without extra setup. Configurable later if NTLM-domain integration ships.
const DefaultWorkgroup = "WORKGROUP"

// Input is the rendered-template payload. ShareView captures the per-share
// fields after preset resolution + ACL flattening.
type Input struct {
	Workgroup string
	Shares    []ShareView
}

// ShareView is one [share-name] section in the rendered conf. The engine
// transforms a share.Share into this view; the template iterates over the
// slice in stable (alphabetical) order.
type ShareView struct {
	Name         string
	Path         string
	Comment      string
	DefaultMode  string // "read_only" | "read_write"
	ValidUsers   string
	WriteList    string
	ReadList     string
	InvalidUsers string
	Params       PresetParams
}

// PresetParams duplicates the option fields of share.PresetParams in this
// package to avoid an import cycle (share → share/smb → share). The engine
// layer copies fields across before calling RenderConfig.
type PresetParams struct {
	VfsObjects       string
	Oplocks          string
	StrictLocking    string
	Sync             string
	FruitTimeMachine string
	FruitMetadata    string
	FruitPosixRename string
	VetoFiles        string
	MinProtocol      string
	AIOReadSize      string
	AIOWriteSize     string
	CaseSensitive    string
}

// RenderConfig assembles the smb.conf body. Pure function — easy to test with
// golden files. The engine writes the bytes to disk + reloads smbd.
func RenderConfig(in Input) ([]byte, error) {
	tmpl, err := template.ParseFS(templatesFS, "templates/smb.conf.tmpl")
	if err != nil {
		return nil, fmt.Errorf("smb render: parse template: %w", err)
	}
	if in.Workgroup == "" {
		in.Workgroup = DefaultWorkgroup
	}
	// Stable share order — list ordering matters because the resulting file
	// is what we compare in golden tests and what an operator sees on `cat`.
	sort.Slice(in.Shares, func(i, j int) bool { return in.Shares[i].Name < in.Shares[j].Name })
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, in); err != nil {
		return nil, fmt.Errorf("smb render: execute: %w", err)
	}
	// Normalise trailing whitespace per line — text/template can emit lone
	// spaces on conditional gaps which makes diffs noisy. We rstrip per line
	// without altering line count.
	out := normaliseLines(buf.Bytes())
	return out, nil
}

// normaliseLines strips trailing whitespace per line. Keeps total line count.
func normaliseLines(b []byte) []byte {
	lines := strings.Split(string(b), "\n")
	for i, ln := range lines {
		lines[i] = strings.TrimRight(ln, " \t")
	}
	return []byte(strings.Join(lines, "\n"))
}
