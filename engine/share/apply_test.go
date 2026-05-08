package share

import (
	"context"
	"errors"
	"testing"

	"github.com/kuraos-org/kura/internal/config"
)

// stubEngine records calls and lets tests pre-fill the listed shares. Used by
// adapter_test to drive Plan/Apply without a real Manager.
type stubEngine struct {
	listed   []Share
	created  []CreateInput
	deleted  []string
	createOK bool
	listErr  error
}

func (s *stubEngine) List(ctx context.Context) ([]Share, error)         { return s.listed, s.listErr }
func (s *stubEngine) Get(ctx context.Context, id string) (Share, error) { return Share{ID: id}, nil }
func (s *stubEngine) Create(ctx context.Context, in CreateInput) (Share, error) {
	s.created = append(s.created, in)
	if !s.createOK {
		return Share{}, errors.New("stub: not enabled")
	}
	out := Share{ID: in.Name + "-id", Name: in.Name, Path: in.Path}
	s.listed = append(s.listed, out)
	return out, nil
}
func (s *stubEngine) Delete(ctx context.Context, id string) error {
	s.deleted = append(s.deleted, id)
	return nil
}
func (s *stubEngine) Apply(ctx context.Context) error { return nil }

func TestAdapterPlanCreatesNewShares(t *testing.T) {
	eng := &stubEngine{createOK: true}
	a := NewShareAdapter(eng)

	old := config.New()
	newCfg := config.New()
	newCfg.Shares = &config.SharesConfig{
		Shares: []config.ShareEntry{
			{Name: "photos", Path: "/tank/photos", Protocol: "smb", Preset: "general", AccessMode: "read_write"},
		},
	}

	steps, err := a.Plan(context.Background(), old, newCfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].Op != "create_share" {
		t.Fatalf("expected single create_share step, got %v", steps)
	}
	if err := a.Apply(context.Background(), steps); err != nil {
		t.Fatal(err)
	}
	if len(eng.created) != 1 || eng.created[0].Name != "photos" {
		t.Errorf("Create not called as expected: %v", eng.created)
	}
}

func TestAdapterPlanDeletesRemovedShares(t *testing.T) {
	eng := &stubEngine{
		createOK: true,
		listed: []Share{
			{ID: "p1", Name: "photos", Path: "/tank/photos"},
		},
	}
	a := NewShareAdapter(eng)

	old := config.New()
	old.Shares = &config.SharesConfig{
		Shares: []config.ShareEntry{
			{ID: "p1", Name: "photos", Path: "/tank/photos", Protocol: "smb", Preset: "general", AccessMode: "read_write"},
		},
	}
	newCfg := config.New()

	steps, err := a.Plan(context.Background(), old, newCfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].Op != "delete_share" {
		t.Fatalf("expected single delete_share step, got %v", steps)
	}
	if err := a.Apply(context.Background(), steps); err != nil {
		t.Fatal(err)
	}
	if len(eng.deleted) != 1 || eng.deleted[0] != "p1" {
		t.Errorf("Delete not called as expected: %v", eng.deleted)
	}
}

func TestAdapterApplyIdempotentOnNameTaken(t *testing.T) {
	// The engine returns ErrNameTaken — adapter treats it as a no-op so
	// re-running apply on a partially-applied config converges.
	eng := &nameTakenEngine{}
	a := NewShareAdapter(eng)

	steps := []config.Step{{
		Section: "shares",
		Op:      "create_share",
		Detail: CreateShareStep{Entry: config.ShareEntry{
			Name: "photos", Path: "/tank/photos",
			Protocol: "smb", Preset: "general", AccessMode: "read_write",
		}},
	}}
	if err := a.Apply(context.Background(), steps); err != nil {
		t.Fatalf("expected idempotent ok, got %v", err)
	}
}

type nameTakenEngine struct{ stubEngine }

func (n *nameTakenEngine) Create(_ context.Context, _ CreateInput) (Share, error) {
	return Share{}, ErrNameTaken
}
func (n *nameTakenEngine) List(_ context.Context) ([]Share, error) { return nil, nil }
func (n *nameTakenEngine) Get(_ context.Context, id string) (Share, error) {
	return Share{ID: id}, nil
}
func (n *nameTakenEngine) Delete(_ context.Context, id string) error { return nil }
func (n *nameTakenEngine) Apply(_ context.Context) error             { return nil }
