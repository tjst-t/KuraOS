// Package nfs renders /etc/exports.d/kura.exports from the share.Engine
// state. Per design.md: NFS 4.2 defaults, no_subtree_check (modern default
// recommended by nfs-utils), sec=sys (Kerberos out of scope), async/sync
// from share AccessMode.
//
// v1 limits NFS to a coarse "anyone on the LAN" client spec — `*`. Tighter
// client filtering (subnet ACLs, per-host) lands when the network engine
// surfaces a subnet picker. The decision is logged in sprint Sd64f38 decisions.
package nfs

import (
	"bytes"
	"embed"
	"fmt"
	"sort"
	"text/template"
)

//go:embed templates/exports.tmpl
var templatesFS embed.FS

// DefaultClients is the host-spec applied to v1 exports. `*` means "any host
// reachable to the kura host"; tighter filtering becomes a per-share field
// once the UI exposes a subnet picker (v1.x).
const DefaultClients = "*"

// Input is the rendered-template payload.
type Input struct {
	Shares []ShareView
}

// ShareView is one exports line. Options is the parenthesised flag list.
type ShareView struct {
	Name    string
	Path    string
	Comment string
	Clients string
	Options string
}

// RenderExports assembles the exports body. Pure function — golden tests
// pin the byte output.
func RenderExports(in Input) ([]byte, error) {
	tmpl, err := template.ParseFS(templatesFS, "templates/exports.tmpl")
	if err != nil {
		return nil, fmt.Errorf("nfs render: parse template: %w", err)
	}
	sort.Slice(in.Shares, func(i, j int) bool { return in.Shares[i].Name < in.Shares[j].Name })
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, in); err != nil {
		return nil, fmt.Errorf("nfs render: execute: %w", err)
	}
	return buf.Bytes(), nil
}
