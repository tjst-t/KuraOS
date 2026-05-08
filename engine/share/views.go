package share

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kuraos-org/kura/engine/share/nfs"
	"github.com/kuraos-org/kura/engine/share/smb"
)

// buildSMBView resolves preset + ACL into the smb sub-package's render shape.
// Lives in package share (not in share/smb) so the smb sub-package stays
// dependency-free relative to the parent — required to dodge an import cycle.
func buildSMBView(s Share) (smb.ShareView, error) {
	params, err := LookupPreset(s.Preset)
	if err != nil {
		return smb.ShareView{}, fmt.Errorf("share render: %q: %w", s.Name, err)
	}
	v := smb.ShareView{
		Name:        s.Name,
		Path:        s.Path,
		Comment:     s.Description,
		DefaultMode: string(s.AccessMode),
		Params: smb.PresetParams{
			VfsObjects:       params.VfsObjects,
			Oplocks:          params.Oplocks,
			StrictLocking:    params.StrictLocking,
			Sync:             params.Sync,
			FruitTimeMachine: params.FruitTimeMachine,
			FruitMetadata:    params.FruitMetadata,
			FruitPosixRename: params.FruitPosixRename,
			VetoFiles:        params.VetoFiles,
			MinProtocol:      params.MinProtocol,
			AIOReadSize:      params.AIOReadSize,
			AIOWriteSize:     params.AIOWriteSize,
			CaseSensitive:    params.CaseSensitive,
		},
	}
	if v.Comment == "" {
		v.Comment = "KuraOS share " + s.Name
	}
	v.ValidUsers, v.WriteList, v.ReadList, v.InvalidUsers = flattenACL(s)
	return v, nil
}

// buildNFSView reduces a Share into the nfs sub-package's exports-line view.
// AccessMode drives rw/ro; the rest of the option list is fixed v1 defaults.
func buildNFSView(s Share) nfs.ShareView {
	rw := "ro"
	if s.AccessMode == AccessReadWrite {
		rw = "rw"
	}
	opts := []string{
		rw,
		"sync",
		"no_subtree_check",
		"root_squash",
		"sec=sys",
	}
	comment := s.Description
	if comment == "" {
		comment = "KuraOS share " + s.Name
	}
	return nfs.ShareView{
		Name:    s.Name,
		Path:    s.Path,
		Comment: comment,
		Clients: nfs.DefaultClients,
		Options: strings.Join(opts, ","),
	}
}

// flattenACL reduces the ACL slice into the four smb.conf principal lists.
//   - valid users / invalid users uses Samba's `@group` syntax for groups.
//   - write list grants rw regardless of `read only = yes` global.
//   - read list grants r when `read only = no`.
//
// Deterministic — entries are sorted by kind+name so rendered configs are
// stable byte-for-byte (golden tests rely on this).
func flattenACL(s Share) (valid, write, read, invalid string) {
	es := append([]ACLEntry(nil), s.ACL...)
	sort.Slice(es, func(i, j int) bool {
		if es[i].Kind != es[j].Kind {
			return es[i].Kind < es[j].Kind
		}
		return es[i].Name < es[j].Name
	})
	var validList, writeList, readList, invalidList []string
	for _, e := range es {
		token := principalToken(e.Kind, e.Name)
		switch e.Mode {
		case ACLModeNone:
			invalidList = append(invalidList, token)
			continue
		case ACLModeReadWrite:
			validList = append(validList, token)
			writeList = append(writeList, token)
		case ACLModeRead:
			validList = append(validList, token)
			readList = append(readList, token)
		}
	}
	return joinPrincipals(validList), joinPrincipals(writeList), joinPrincipals(readList), joinPrincipals(invalidList)
}

func principalToken(kind PrincipalKind, name string) string {
	if kind == PrincipalGroup {
		return "@" + name
	}
	return name
}

func joinPrincipals(xs []string) string {
	if len(xs) == 0 {
		return ""
	}
	return strings.Join(xs, ", ")
}
