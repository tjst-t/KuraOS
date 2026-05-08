package storage

import (
	"bufio"
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// parseZpoolList consumes the tab-separated, header-less output of
//
//	zpool list -H -p -o name,size,allocated,free,frag,cap,dedup,health,guid
//
// Empty input returns nil, nil.
//
// -p forces parseable units (raw bytes for size/alloc/free, plain integers
// for frag/cap), so we never face the "1.23T" / "12%" round-trip ambiguity
// that the human-readable form has.
func parseZpoolList(raw []byte) ([]Pool, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var out []Pool
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 9 {
			return nil, fmt.Errorf("zpool list: line %d: want 9 fields, got %d", lineNo, len(fields))
		}
		size, err := parseInt64OrDash(fields[1])
		if err != nil {
			return nil, fmt.Errorf("zpool list: line %d size: %w", lineNo, err)
		}
		alloc, err := parseInt64OrDash(fields[2])
		if err != nil {
			return nil, fmt.Errorf("zpool list: line %d alloc: %w", lineNo, err)
		}
		free, err := parseInt64OrDash(fields[3])
		if err != nil {
			return nil, fmt.Errorf("zpool list: line %d free: %w", lineNo, err)
		}
		frag := parseIntOrZero(fields[4])
		capPct := parseIntOrZero(fields[5])
		dedup := parseDedup(fields[6])
		out = append(out, Pool{
			Name:           fields[0],
			GUID:           fields[8],
			Health:         normalizeHealth(fields[7]),
			SizeBytes:      size,
			AllocatedBytes: alloc,
			FreeBytes:      free,
			FragPercent:    frag,
			CapPercent:     capPct,
			Dedup:          dedup,
		})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("zpool list: scan: %w", err)
	}
	return out, nil
}

// parseZpoolStatus parses the indented topology block of `zpool status -P -L
// <pool>`. The second return value is the pool-level scan / errors line block
// joined into a string (kept so future sprints can extract scrub timestamps
// without re-running the CLI). The third is the parse error if any.
//
// Strategy: walk the lines after `config:` and indent to depth. Each
// indentation level deeper than the previous opens a new child; equal indent
// is a sibling; shallower closes back up to the matching level. ZFS uses tabs
// + leading spaces of variable count; we count any leading whitespace as one
// "indent unit" per tab/4-space block but we mostly key off section headings
// (`special`, `cache`, `logs`, `spares`) so the tree is built by section
// rather than purely by indent.
func parseZpoolStatus(raw []byte) ([]Vdev, string, error) {
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	inConfig := false
	headerSeen := false
	currentSection := VdevTypeData
	var topology []Vdev
	var rootIndent = -1

	// stack[i] = pointer-target index path into the topology slice. Top of
	// stack is the parent we currently append children into.
	type frame struct {
		path   []int
		indent int
	}
	var stack []frame

	getRef := func(p []int) *Vdev {
		if len(p) == 0 {
			return nil
		}
		v := &topology[p[0]]
		for _, idx := range p[1:] {
			v = &v.Children[idx]
		}
		return v
	}

	addChild := func(p []int, child Vdev) []int {
		if len(p) == 0 {
			topology = append(topology, child)
			return []int{len(topology) - 1}
		}
		parent := getRef(p)
		parent.Children = append(parent.Children, child)
		return append(append([]int(nil), p...), len(parent.Children)-1)
	}

	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if !inConfig {
			if strings.HasPrefix(trimmed, "config:") {
				inConfig = true
			}
			continue
		}
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "errors:") {
			break
		}
		if !headerSeen {
			if strings.HasPrefix(trimmed, "NAME") {
				headerSeen = true
			}
			continue
		}

		// Section keywords reset the parsing context. They are flush-left in
		// `zpool status` output ("special" / "cache" / "logs" / "spares").
		// data/special carry their own root row (mirror-N / raidz2-0); cache /
		// logs / spares list leaves directly with no synthetic root, so we
		// pre-fill rootIndent for those sections.
		switch trimmed {
		case "special":
			currentSection = VdevTypeSpecial
			stack = nil
			rootIndent = -1
			continue
		case "cache":
			currentSection = VdevTypeCache
			stack = nil
			rootIndent = 0
			continue
		case "logs":
			currentSection = VdevTypeLog
			stack = nil
			rootIndent = 0
			continue
		case "spares":
			currentSection = VdevTypeSpare
			stack = nil
			rootIndent = 0
			continue
		}

		indent := leadingWhitespace(line)
		fields := strings.Fields(trimmed)
		if len(fields) == 0 {
			continue
		}
		name := fields[0]
		// state column may be missing for spares (just "AVAIL"); be tolerant.
		health := HealthUnknown
		if len(fields) >= 2 {
			health = normalizeHealth(fields[1])
		}
		read, write, cksum := int64(0), int64(0), int64(0)
		if len(fields) >= 5 {
			read = parseInt64OrZero(fields[2])
			write = parseInt64OrZero(fields[3])
			cksum = parseInt64OrZero(fields[4])
		}

		// The first indented line under data is the pool name itself
		// (e.g. "tank ONLINE 0 0 0"). We treat that as the root and don't
		// emit a Vdev for it — children will be appended directly.
		if rootIndent == -1 {
			rootIndent = indent
			// The pool root is just an alias for the pool name; skip it.
			continue
		}

		// Decide depth relative to current stack.
		for len(stack) > 0 && indent <= stack[len(stack)-1].indent {
			stack = stack[:len(stack)-1]
		}

		layout := LayoutUnknown
		isLeaf := true
		typ := currentSection
		if currentSection == VdevTypeData || currentSection == VdevTypeSpecial || currentSection == VdevTypeLog {
			// Only data/special/log can have nested mirror/raidz containers.
			if l, ok := detectLayout(name); ok {
				layout = l
				isLeaf = false
			}
		}

		v := Vdev{
			Type:   typ,
			Layout: layout,
			Name:   name,
			Health: health,
			Read:   read,
			Write:  write,
			Cksum:  cksum,
		}
		if isLeaf {
			v.Path = name
		}

		var parent []int
		if len(stack) > 0 {
			parent = stack[len(stack)-1].path
		}
		newPath := addChild(parent, v)
		if !isLeaf {
			stack = append(stack, frame{path: newPath, indent: indent})
		}
	}
	if err := sc.Err(); err != nil {
		return nil, "", fmt.Errorf("zpool status: scan: %w", err)
	}
	// We don't currently extract the scan line into a structured field, but
	// returning the whole input as a debug string lets ListPools attach it
	// to logs without a re-parse later.
	return topology, "", nil
}

// parseZpoolImportScan reads the `zpool import` (no args) output and emits
// one ImportablePool per "pool: <name>" block. Topology is captured at a
// shallower fidelity than parseZpoolStatus (no error counters in this view).
func parseZpoolImportScan(raw []byte) ([]ImportablePool, error) {
	if isZpoolImportEmpty(raw) {
		return nil, nil
	}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var out []ImportablePool
	var current *ImportablePool
	inConfig := false
	headerSeen := false
	rootIndent := -1
	currentSection := VdevTypeData

	type frame struct {
		path   []int
		indent int
	}
	var stack []frame
	getRef := func(p []*Vdev, root *[]Vdev) *Vdev {
		_ = p
		_ = root
		return nil
	}
	_ = getRef

	addChild := func(p []int, child Vdev) []int {
		if current == nil {
			return p
		}
		if len(p) == 0 {
			current.Topology = append(current.Topology, child)
			return []int{len(current.Topology) - 1}
		}
		parent := &current.Topology[p[0]]
		for _, idx := range p[1:] {
			parent = &parent.Children[idx]
		}
		parent.Children = append(parent.Children, child)
		return append(append([]int(nil), p...), len(parent.Children)-1)
	}

	flush := func() {
		if current != nil {
			out = append(out, *current)
			current = nil
		}
		inConfig = false
		headerSeen = false
		rootIndent = -1
		stack = nil
		currentSection = VdevTypeData
	}

	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if strings.HasPrefix(trimmed, "pool:") {
			flush()
			current = &ImportablePool{Name: strings.TrimSpace(strings.TrimPrefix(trimmed, "pool:"))}
			continue
		}
		if current == nil {
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, "id:"):
			current.GUID = strings.TrimSpace(strings.TrimPrefix(trimmed, "id:"))
		case strings.HasPrefix(trimmed, "state:"):
			current.State = normalizeHealth(strings.TrimSpace(strings.TrimPrefix(trimmed, "state:")))
		case strings.HasPrefix(trimmed, "status:"):
			current.StatusMsg = strings.TrimSpace(strings.TrimPrefix(trimmed, "status:"))
		case strings.HasPrefix(trimmed, "action:"):
			current.Action = strings.TrimSpace(strings.TrimPrefix(trimmed, "action:"))
		case strings.HasPrefix(trimmed, "config:"):
			inConfig = true
			headerSeen = true // import-scan output has no NAME header line
		default:
			if !inConfig {
				// Continuation line (action: spans multiple lines). Append
				// to whichever scalar field we last touched — only Action
				// is multi-line in practice.
				if current.Action != "" {
					current.Action = current.Action + " " + trimmed
				}
				continue
			}

			// Section keywords ("cache", "logs", "spares") flush-left.
			switch trimmed {
			case "cache":
				currentSection = VdevTypeCache
				stack = nil
				rootIndent = -1
				continue
			case "logs":
				currentSection = VdevTypeLog
				stack = nil
				rootIndent = -1
				continue
			case "spares":
				currentSection = VdevTypeSpare
				stack = nil
				rootIndent = -1
				continue
			case "special":
				currentSection = VdevTypeSpecial
				stack = nil
				rootIndent = -1
				continue
			}

			indent := leadingWhitespace(line)
			fields := strings.Fields(trimmed)
			if len(fields) == 0 {
				continue
			}
			name := fields[0]
			health := HealthUnknown
			if len(fields) >= 2 {
				health = normalizeHealth(fields[1])
			}

			if rootIndent == -1 {
				rootIndent = indent
				continue
			}
			for len(stack) > 0 && indent <= stack[len(stack)-1].indent {
				stack = stack[:len(stack)-1]
			}

			layout := LayoutUnknown
			isLeaf := true
			if currentSection == VdevTypeData || currentSection == VdevTypeSpecial || currentSection == VdevTypeLog {
				if l, ok := detectLayout(name); ok {
					layout = l
					isLeaf = false
				}
			}

			v := Vdev{
				Type:   currentSection,
				Layout: layout,
				Name:   name,
				Health: health,
			}
			if isLeaf {
				v.Path = name
			}

			var parent []int
			if len(stack) > 0 {
				parent = stack[len(stack)-1].path
			}
			newPath := addChild(parent, v)
			if !isLeaf {
				stack = append(stack, frame{path: newPath, indent: indent})
			}
			_ = headerSeen
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("zpool import: scan: %w", err)
	}
	flush()
	return out, nil
}

// detectLayout maps "raidz1-3", "mirror-7", "stripe", etc. to a VdevLayout.
// Returns (LayoutUnknown, false) when name is plainly a leaf disk.
func detectLayout(name string) (VdevLayout, bool) {
	// Strip trailing -N suffix the kernel uses to disambiguate vdevs.
	// We compare on the prefix.
	prefix := name
	if i := strings.IndexByte(name, '-'); i > 0 {
		prefix = name[:i]
	}
	switch prefix {
	case "mirror":
		return LayoutMirror, true
	case "raidz", "raidz1":
		return LayoutRaidZ1, true
	case "raidz2":
		return LayoutRaidZ2, true
	case "raidz3":
		return LayoutRaidZ3, true
	case "stripe":
		return LayoutSingle, true
	}
	return LayoutUnknown, false
}

// normalizeHealth coerces ZFS state strings into the Health enum.
// AVAIL (spare row) collapses to HealthOnline so the badge color matches.
func normalizeHealth(raw string) Health {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "ONLINE":
		return HealthOnline
	case "DEGRADED":
		return HealthDegraded
	case "FAULTED":
		return HealthFaulted
	case "OFFLINE":
		return HealthOffline
	case "UNAVAIL":
		return HealthUnavail
	case "REMOVED":
		return HealthRemoved
	case "SUSPENDED":
		return HealthSuspended
	case "AVAIL":
		return HealthOnline
	default:
		return HealthUnknown
	}
}

func leadingWhitespace(s string) int {
	n := 0
	for _, r := range s {
		switch r {
		case '\t':
			n += 4
		case ' ':
			n++
		default:
			return n
		}
	}
	return n
}

func parseInt64OrDash(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "-" || s == "" {
		return 0, nil
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %q: %w", s, err)
	}
	return v, nil
}

func parseInt64OrZero(s string) int64 {
	v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return v
}

func parseIntOrZero(s string) int {
	v, err := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(s, "%")))
	if err != nil {
		return 0
	}
	return v
}

// parseDedup turns the "1.00x" form into 1.0. Missing/invalid -> 1.0 (the
// neutral "no dedup" value).
func parseDedup(s string) float64 {
	s = strings.TrimSpace(strings.TrimSuffix(s, "x"))
	if s == "" || s == "-" {
		return 1.0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 1.0
	}
	return v
}

func isZpoolImportEmpty(raw []byte) bool {
	return strings.Contains(string(raw), "no pools available for import")
}
