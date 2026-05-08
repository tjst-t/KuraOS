package config

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
)

// DiffEntry describes a single change between two Config documents at the
// section granularity. Engines turn these into concrete plan items
// (e.g. "create dataset tank/photos") in their ApplyAdapter implementations.
type DiffEntry struct {
	Section string          `json:"section"`
	Op      string          `json:"op"` // "add", "remove", "update", "noop"
	Old     json.RawMessage `json:"old,omitempty"`
	New     json.RawMessage `json:"new,omitempty"`
}

// Plan is the ordered list of diff entries produced by Diff. Order matches
// the field order of Config so engines see a deterministic sequence on every
// run — a property the apply pipeline relies on for idempotency.
type Plan struct {
	Entries []DiffEntry `json:"entries"`
}

// Empty reports whether the plan contains no actionable entries (only noops
// or no entries at all).
func (p Plan) Empty() bool {
	for _, e := range p.Entries {
		if e.Op != "noop" {
			return false
		}
	}
	return true
}

// Diff produces a section-level Plan from old to new. A nil old or new is
// treated as an empty Config, which is the natural state when a brand-new
// kura instance imports its first config.json.
//
// At this sprint's scope, sections are compared by JSON equality. Engine
// sprints will replace this with field-level comparison once the sub-structs
// have content to inspect.
func Diff(old, new *Config) (Plan, error) {
	if old == nil {
		old = New()
	}
	if new == nil {
		new = New()
	}

	oldSections, err := sectionMap(old)
	if err != nil {
		return Plan{}, err
	}
	newSections, err := sectionMap(new)
	if err != nil {
		return Plan{}, err
	}

	// Iterate in the canonical Config field order.
	var plan Plan
	for _, name := range sectionOrder() {
		o, oOK := oldSections[name]
		n, nOK := newSections[name]
		entry := DiffEntry{Section: name}
		switch {
		case !oOK && !nOK:
			continue
		case !oOK && nOK:
			entry.Op = "add"
			entry.New = n
		case oOK && !nOK:
			entry.Op = "remove"
			entry.Old = o
		default:
			if jsonEqual(o, n) {
				entry.Op = "noop"
			} else {
				entry.Op = "update"
				entry.Old = o
				entry.New = n
			}
		}
		plan.Entries = append(plan.Entries, entry)
	}
	return plan, nil
}

// sectionOrder returns the JSON tag names in the order they're declared on
// Config so Diff output is stable.
func sectionOrder() []string {
	t := reflect.TypeOf(Config{})
	var names []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "" {
			continue
		}
		// Strip ",omitempty" etc.
		for j := 0; j < len(tag); j++ {
			if tag[j] == ',' {
				tag = tag[:j]
				break
			}
		}
		if tag == "" || tag == "-" || tag == "schema_version" {
			continue
		}
		names = append(names, tag)
	}
	return names
}

// sectionMap turns a Config into {section -> raw JSON} so Diff can compare
// without per-field reflection. Empty pointer fields are omitted, matching
// the behavior of `omitempty` on the struct.
func sectionMap(cfg *Config) (map[string]json.RawMessage, error) {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("config: section marshal: %w", err)
	}
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, fmt.Errorf("config: section unmarshal: %w", err)
	}
	out := map[string]json.RawMessage{}
	known := map[string]bool{}
	for _, n := range sectionOrder() {
		known[n] = true
	}
	for k, v := range generic {
		if known[k] {
			out[k] = v
		}
	}
	return out, nil
}

func jsonEqual(a, b json.RawMessage) bool {
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		return false
	}
	return reflect.DeepEqual(av, bv)
}

// FormatPlan renders a Plan as a human-readable summary. Used by
// `kura config apply --dry-run`. Lines are deterministic so golden tests
// can match exactly.
func FormatPlan(p Plan) string {
	if len(p.Entries) == 0 {
		return "no plan entries\n"
	}
	keys := make([]string, 0, len(p.Entries))
	byKey := map[string]DiffEntry{}
	for _, e := range p.Entries {
		keys = append(keys, e.Section)
		byKey[e.Section] = e
	}
	sort.Strings(keys)
	var out string
	for _, k := range keys {
		e := byKey[k]
		out += fmt.Sprintf("%-7s %s\n", e.Op, e.Section)
	}
	return out
}

// ApplyAdapter is the contract every engine implements to participate in
// `kura config apply`. Plan inspects the diff and returns engine-specific
// steps; Apply executes them. Splitting them lets `--dry-run` show what
// would change without committing.
//
// Engines register their adapter in init() via Register so the binary stays
// statically linked — no plugin loading (DESIGN_PRINCIPLES forbidden #9).
type ApplyAdapter interface {
	Section() string
	Plan(ctx context.Context, old, new *Config) ([]Step, error)
	Apply(ctx context.Context, steps []Step) error
}

// Step is one engine-specific operation. Section gives provenance, Op is a
// short verb, Detail holds any structured payload the engine needs.
type Step struct {
	Section string
	Op      string
	Detail  any
}

// registry is the central ApplyAdapter store. New engines call Register()
// from their package init() so the apply pipeline picks them up without a
// global import graph.
var registry = map[string]ApplyAdapter{}

// Register adds an adapter to the registry. Duplicate registration panics —
// it indicates two packages claiming the same section, which is always a
// bug.
func Register(a ApplyAdapter) {
	if a == nil {
		panic("config.Register: nil adapter")
	}
	s := a.Section()
	if s == "" {
		panic("config.Register: empty Section()")
	}
	if _, dup := registry[s]; dup {
		panic("config.Register: duplicate section " + s)
	}
	registry[s] = a
}

// Adapters returns a snapshot of registered adapters, in section order.
func Adapters() []ApplyAdapter {
	names := make([]string, 0, len(registry))
	for k := range registry {
		names = append(names, k)
	}
	sort.Strings(names)
	out := make([]ApplyAdapter, 0, len(names))
	for _, n := range names {
		out = append(out, registry[n])
	}
	return out
}

// resetRegistryForTest is a test-only escape hatch — *never* call it from
// production code. Exported under a deliberately ugly name so misuse is
// obvious.
func resetRegistryForTest() { registry = map[string]ApplyAdapter{} }
