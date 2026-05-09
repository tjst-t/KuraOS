package system

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
)

const (
	pathPasswd     = "/etc/passwd"
	pathGroup      = "/etc/group"
	pathShadow     = "/etc/shadow"
	pathSmbConf    = "/etc/samba/smb.conf"
	smbIncludeLine = "include = /etc/samba/conf.d/kura.conf"
)

// renderPasswdBlock formats the KuraOS-managed segment of /etc/passwd.
// Format is the standard 7-field colon-delimited line:
//
//	username:x:uid:gid:gecos:home:shell
//
// Password field is "x" — actual auth lives in the vault and Samba tdbsam.
// Linux PAM uses argon2id via /etc/shadow only if explicitly added (out of
// v1 scope; KuraOS users do not get shell logins).
func renderPasswdBlock(users []ProjectedUser, primaryGID int) string {
	var b strings.Builder
	b.WriteString(BeginMarker)
	b.WriteString("\n")
	for _, u := range users {
		gecos := u.DisplayName
		if gecos == "" {
			gecos = u.Username
		}
		home := u.HomeDir
		if home == "" {
			home = "/var/empty"
		}
		shell := u.Shell
		if shell == "" {
			shell = "/usr/sbin/nologin"
		}
		gid := u.GID
		if gid == 0 {
			gid = primaryGID
		}
		fmt.Fprintf(&b, "%s:x:%d:%d:%s:%s:%s\n",
			u.Username, u.UID, gid, sanitizeGecos(gecos), home, shell)
	}
	b.WriteString(EndMarker)
	b.WriteString("\n")
	return b.String()
}

// renderGroupBlock formats the KuraOS-managed segment of /etc/group:
//
//	groupname:x:gid:member1,member2
func renderGroupBlock(groups []ProjectedGroup) string {
	var b strings.Builder
	b.WriteString(BeginMarker)
	b.WriteString("\n")
	for _, g := range groups {
		fmt.Fprintf(&b, "%s:x:%d:%s\n",
			g.Name, g.GID, strings.Join(g.Members, ","))
	}
	b.WriteString(EndMarker)
	b.WriteString("\n")
	return b.String()
}

// mergeManagedBlock replaces the KuraOS-managed block (BeginMarker..EndMarker)
// in original with newBlock. If no markers are present, newBlock is appended
// to the file. Outside-marker lines are preserved verbatim.
//
// If userManagedConflict is non-empty after the merge, the corresponding
// distro/sysadmin entries are dropped from the original to avoid uid
// collisions — a passwd row outside our managed block with the same uid
// would shadow ours through nsswitch. Conflict resolution lets the
// KuraOS-managed entry win on every reconcile.
func mergeManagedBlock(original []byte, newBlock string, userConflictKeys []string) []byte {
	lines := strings.Split(string(original), "\n")
	out := make([]string, 0, len(lines)+8)
	inBlock := false
	blockReplaced := false

	conflictSet := map[string]struct{}{}
	for _, k := range userConflictKeys {
		conflictSet[k] = struct{}{}
	}

	for _, line := range lines {
		switch {
		case strings.TrimSpace(line) == BeginMarker:
			inBlock = true
			out = append(out, strings.TrimRight(newBlock, "\n"))
			blockReplaced = true
		case strings.TrimSpace(line) == EndMarker:
			inBlock = false
			// EndMarker (and its newline) consumed — newBlock already
			// contains its own end marker.
		case inBlock:
			// Drop lines inside the old managed block.
		default:
			// Drop conflicting lines (same username/groupname as one
			// we're about to write) so the managed block stays the
			// authoritative source.
			if dropConflict(line, conflictSet) {
				continue
			}
			out = append(out, line)
		}
	}
	if !blockReplaced {
		// File had no marker at all — append the new block.
		out = append(out, strings.TrimRight(newBlock, "\n"))
	}
	// Re-append a trailing newline so the file ends cleanly.
	joined := strings.Join(out, "\n")
	if !strings.HasSuffix(joined, "\n") {
		joined += "\n"
	}
	return []byte(joined)
}

// dropConflict returns true when line's first colon-delimited field (the
// username for passwd, the groupname for group) is in conflict.
func dropConflict(line string, conflicts map[string]struct{}) bool {
	if line == "" || line[0] == '#' {
		return false
	}
	colon := strings.IndexByte(line, ':')
	if colon < 0 {
		return false
	}
	name := line[:colon]
	_, hit := conflicts[name]
	return hit
}

// ensureSmbInclude makes sure the include directive is present somewhere
// in /etc/samba/smb.conf. The file is only mutated when the line is missing
// (idempotent re-runs don't touch the file's mtime).
//
// Returns (changed, error). changed=true means the file was rewritten.
func ensureSmbInclude(fs FileSystem) (bool, error) {
	raw, err := fs.ReadFile(pathSmbConf)
	if err != nil {
		if errors.Is(err, errFileNotExist(err)) || isNotExist(err) {
			// No smbd installed — print a fragment that other code (or a
			// future install) can pick up by appending the include line.
			data := "# Created by KuraOS engine/system\n[global]\n    " + smbIncludeLine + "\n"
			if werr := fs.WriteAtomic(pathSmbConf, []byte(data), 0o644); werr != nil {
				return false, fmt.Errorf("system: write smb.conf: %w", werr)
			}
			return true, nil
		}
		return false, fmt.Errorf("system: read smb.conf: %w", err)
	}
	if smbHasInclude(raw) {
		return false, nil
	}
	updated := appendSmbInclude(raw)
	if werr := fs.WriteAtomic(pathSmbConf, updated, 0o644); werr != nil {
		return false, fmt.Errorf("system: write smb.conf: %w", werr)
	}
	return true, nil
}

func smbHasInclude(raw []byte) bool {
	// Match either "include = /etc/samba/conf.d/kura.conf" or any
	// indentation variant. Compare against the literal directive.
	for _, line := range bytes.Split(raw, []byte("\n")) {
		trimmed := strings.TrimSpace(string(line))
		if trimmed == smbIncludeLine {
			return true
		}
	}
	return false
}

// appendSmbInclude inserts the include line into the [global] section if
// one exists, otherwise appends a [global] section with the include line.
func appendSmbInclude(raw []byte) []byte {
	lines := strings.Split(string(raw), "\n")
	out := make([]string, 0, len(lines)+3)
	inserted := false
	inGlobal := false
	for i, line := range lines {
		out = append(out, line)
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if inGlobal && !inserted {
				// We're transitioning out of [global] without inserting.
				// Insert just before this section header.
				out = out[:len(out)-1]
				out = append(out, "    "+smbIncludeLine)
				out = append(out, line)
				inserted = true
			}
			inGlobal = trimmed == "[global]"
			continue
		}
		// At end of file with [global] still open?
		if inGlobal && i == len(lines)-1 && !inserted {
			out = append(out, "    "+smbIncludeLine)
			inserted = true
		}
	}
	if !inserted {
		// No [global] section anywhere — append one.
		out = append(out, "[global]")
		out = append(out, "    "+smbIncludeLine)
	}
	joined := strings.Join(out, "\n")
	if !strings.HasSuffix(joined, "\n") {
		joined += "\n"
	}
	return []byte(joined)
}

// sanitizeGecos strips characters that would corrupt the colon-delimited
// passwd line: ':' itself plus newlines.
func sanitizeGecos(s string) string {
	repl := strings.NewReplacer(":", "_", "\n", " ", "\r", " ")
	return repl.Replace(s)
}

// parseExistingUIDs scans /etc/passwd for uids in the KuraOS range so the
// allocator avoids collisions with distro entries (e.g. an existing
// 'samba' user at uid 30001 from a previous handcrafted setup).
func parseExistingUIDs(raw []byte) map[int]bool {
	out := map[int]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, ":")
		if len(fields) < 4 {
			continue
		}
		uid, err := strconv.Atoi(fields[2])
		if err != nil {
			continue
		}
		if uid >= UIDMin && uid <= UIDMax {
			out[uid] = true
		}
	}
	return out
}

// errFileNotExist allows callers to use errors.Is(err, errFileNotExist(err))
// regardless of the platform's underlying error type. Defensive helper.
func errFileNotExist(err error) error { return os.ErrNotExist }

func isNotExist(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, os.ErrNotExist)
}
