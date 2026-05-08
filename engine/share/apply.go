// apply.go is the ShareEngine's config.json ApplyAdapter. It diffs the
// `shares` section between old / new Config, then drives the Manager to
// reach the target state idempotently.
//
// DESIGN_PRINCIPLES priority #1: SSOT. The Apply pipeline is the bridge that
// makes config.json the source of truth — Plan → Apply must always converge
// SQLite + the rendered smb.conf / exports to whatever the document says,
// regardless of whether SQLite started empty or already had partial state.
package share

import (
	"context"
	"fmt"

	"github.com/kuraos-org/kura/internal/config"
)

// RegisterApplyAdapter installs the share adapter against the global
// config.registry. cmd/kura wiring calls this once per process.
func RegisterApplyAdapter(eng Engine) {
	config.Register(NewShareAdapter(eng))
}

// NewShareAdapter returns a config.ApplyAdapter that targets the `shares`
// section. Tests that want to drive Plan/Apply without the global registry
// call this directly.
func NewShareAdapter(eng Engine) config.ApplyAdapter {
	return &shareAdapter{eng: eng}
}

type shareAdapter struct {
	eng Engine
}

func (a *shareAdapter) Section() string { return "shares" }

// Plan compares the two config snapshots and emits a Step per share that is
// new or removed. Updates (e.g. preset change on an existing share) are
// modelled as remove+create for v1 — simpler engine code, and the cost of an
// extra reload is negligible. Refinement (in-place update) is a v1.x polish
// item logged to the backlog.
func (a *shareAdapter) Plan(ctx context.Context, old, newCfg *config.Config) ([]config.Step, error) {
	oldShares := indexShares(extractShares(old))
	newShares := indexShares(extractShares(newCfg))

	var steps []config.Step
	for name, e := range newShares {
		if _, exists := oldShares[name]; !exists {
			steps = append(steps, config.Step{
				Section: "shares",
				Op:      "create_share",
				Detail:  CreateShareStep{Entry: e},
			})
		}
	}
	for name, e := range oldShares {
		if _, exists := newShares[name]; !exists {
			steps = append(steps, config.Step{
				Section: "shares",
				Op:      "delete_share",
				Detail:  DeleteShareStep{Name: name, ID: e.ID},
			})
		}
	}
	return steps, nil
}

// Apply executes the planned Steps via the Engine. Idempotency: if Create
// fails with ErrNameTaken (the share already exists), the step is treated as
// a no-op so re-running apply on a partially-applied config converges
// cleanly.
func (a *shareAdapter) Apply(ctx context.Context, steps []config.Step) error {
	for _, s := range steps {
		switch s.Op {
		case "create_share":
			st, ok := s.Detail.(CreateShareStep)
			if !ok {
				return fmt.Errorf("share adapter: bad detail for create_share")
			}
			in, err := entryToCreateInput(st.Entry)
			if err != nil {
				return fmt.Errorf("share adapter: entry %q: %w", st.Entry.Name, err)
			}
			if _, err := a.eng.Create(ctx, in); err != nil {
				// Treat ErrNameTaken as success — apply is idempotent.
				if isAlreadyExists(err) {
					continue
				}
				return fmt.Errorf("share adapter: create %q: %w", st.Entry.Name, err)
			}
		case "delete_share":
			st, ok := s.Detail.(DeleteShareStep)
			if !ok {
				return fmt.Errorf("share adapter: bad detail for delete_share")
			}
			if st.ID == "" {
				// Look up by name path: the engine doesn't expose a "by name"
				// getter, so we list and search. List is small in v1.
				list, err := a.eng.List(ctx)
				if err != nil {
					return err
				}
				for _, sh := range list {
					if sh.Name == st.Name {
						st.ID = sh.ID
						break
					}
				}
			}
			if st.ID == "" {
				continue // already gone
			}
			if err := a.eng.Delete(ctx, st.ID); err != nil {
				if isNotFound(err) {
					continue
				}
				return fmt.Errorf("share adapter: delete %q: %w", st.Name, err)
			}
		default:
			return fmt.Errorf("share adapter: unknown op %q", s.Op)
		}
	}
	return nil
}

// CreateShareStep / DeleteShareStep are the typed payloads for config.Step.
// Exported so tests can assert on the planned steps.
type CreateShareStep struct{ Entry config.ShareEntry }
type DeleteShareStep struct {
	Name string
	ID   string
}

func extractShares(cfg *config.Config) []config.ShareEntry {
	if cfg == nil || cfg.Shares == nil {
		return nil
	}
	return cfg.Shares.Shares
}

func indexShares(es []config.ShareEntry) map[string]config.ShareEntry {
	m := make(map[string]config.ShareEntry, len(es))
	for _, e := range es {
		m[e.Name] = e
	}
	return m
}

func entryToCreateInput(e config.ShareEntry) (CreateInput, error) {
	in := CreateInput{
		Name:        e.Name,
		Path:        e.Path,
		Protocol:    Protocol(e.Protocol),
		Preset:      Preset(e.Preset),
		AccessMode:  AccessMode(e.AccessMode),
		Description: e.Description,
	}
	for _, a := range e.ACL {
		in.ACL = append(in.ACL, ACLEntry{
			Kind: PrincipalKind(a.Kind),
			Name: a.Name,
			Mode: ACLMode(a.Mode),
		})
	}
	if err := in.Validate(); err != nil {
		return CreateInput{}, err
	}
	return in, nil
}

// isAlreadyExists / isNotFound abstract sentinel-error matching so apply.go
// stays single-purpose.
func isAlreadyExists(err error) bool {
	return err != nil && (errIs(err, ErrNameTaken) || errIs(err, ErrPathConflict))
}

func isNotFound(err error) bool {
	return err != nil && errIs(err, ErrShareNotFound)
}

// errIs is a thin alias around errors.Is. Inlined to avoid the extra import
// at the top of apply.go (we only need it twice).
func errIs(err, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
