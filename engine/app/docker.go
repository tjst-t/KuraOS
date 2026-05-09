package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

// DockerClient is the slim surface engine/app needs from a container runtime.
// Production wraps the docker/docker SDK (engine API over /var/run/docker.sock).
// Tests inject FakeDockerClient. Keeping the surface tiny makes the seam easy
// to fake and limits the blast radius if we swap runtimes (podman, containerd)
// later — DESIGN_PRINCIPLES priority #9.
//
// All methods take a context.Context per CLAUDE.md coding_conventions.
type DockerClient interface {
	// Ping checks the daemon is reachable; install fails fast if not.
	Ping(ctx context.Context) error

	// PullImage pulls ref into the daemon's local store. Idempotent —
	// callers may invoke it on every install/update and rely on the
	// daemon's image cache. Errors wrap so callers can detect 404s.
	PullImage(ctx context.Context, ref string) error

	// CreateContainer creates (but does not start) a container from spec.
	// Returns the daemon-assigned container ID.
	CreateContainer(ctx context.Context, spec ContainerSpec) (string, error)

	// StartContainer starts a previously-created container.
	StartContainer(ctx context.Context, id string) error

	// StopContainer stops the container with the given timeout. ID may be
	// the container name (we keep names stable per app).
	StopContainer(ctx context.Context, id string, timeout time.Duration) error

	// RemoveContainer removes a stopped container (and any anonymous volumes
	// it owned). Bind mounts and named ZFS dataset mounts are *not* removed —
	// see Uninstall semantics in design.md §7.5.
	RemoveContainer(ctx context.Context, id string, force bool) error

	// InspectContainer returns runtime info (state, health). Used by the
	// healthcheck poller.
	InspectContainer(ctx context.Context, id string) (ContainerInfo, error)

	// ListContainersByLabel lists containers carrying every (k,v) in
	// labels. Used to enumerate containers belonging to an app instance
	// during update / uninstall.
	ListContainersByLabel(ctx context.Context, labels map[string]string) ([]ContainerInfo, error)

	// EnsureNetwork creates the named bridge network if absent. Per-app
	// network keeps DNS-by-service-name working between containers.
	EnsureNetwork(ctx context.Context, name string) error
}

// ContainerSpec is the inputs CreateContainer needs. Mirrors what compose's
// `services:` entry boils down to after the install pipeline has resolved
// shares / dataset paths / port reservations / secrets.
type ContainerSpec struct {
	// Name is the container's externally-visible name. We pick deterministic
	// names ("kura-<app>-<container>") so subsequent installs of the same
	// instance pick up the same containers and update can stop the right ones.
	Name        string
	Image       string
	Env         map[string]string
	Cmd         []string
	Entrypoint  []string
	WorkingDir  string
	Mounts      []MountSpec
	PortMap     []PortBinding
	Network     string
	Labels      map[string]string
	Restart     string
	DependsOn   []string
	Healthcheck *HealthcheckSpec
}

// MountSpec is one bind/named mount. Type follows docker semantics ('bind' or
// 'volume'). For ZFS-backed app datasets we use bind with the host path the
// dataset is mounted at.
type MountSpec struct {
	Type     string // "bind" | "volume"
	Source   string
	Target   string
	ReadOnly bool
}

// PortBinding maps a container port to a host port. Protocol is "tcp" or
// "udp"; v1 only emits tcp.
type PortBinding struct {
	HostPort      int
	ContainerPort int
	Protocol      string
}

// HealthcheckSpec is the docker-side healthcheck config. Test is a /bin/sh
// command list (CMD-SHELL form: first entry is "CMD-SHELL", second is the
// shell line). Empty Test means inherit the image's HEALTHCHECK.
type HealthcheckSpec struct {
	Test        []string
	Interval    time.Duration
	Timeout     time.Duration
	Retries     int
	StartPeriod time.Duration
}

// ContainerInfo is what InspectContainer returns. Keeps only the fields
// engine/app actually needs so mocking stays cheap.
type ContainerInfo struct {
	ID      string
	Name    string
	Image   string
	State   string // "running" | "created" | "exited" | "paused" | "restarting" | "dead"
	Health  string // "starting" | "healthy" | "unhealthy" | "" (none)
	Labels  map[string]string
	StartAt time.Time
}

// ErrDockerUnavailable signals the daemon is not reachable. Install pipelines
// surface this as a translated UI banner.
var ErrDockerUnavailable = errors.New("app: docker daemon unreachable")

// ErrContainerNotFound signals InspectContainer found nothing. Used by
// uninstall to tolerate already-removed containers.
var ErrContainerNotFound = errors.New("app: container not found")

// FakeDockerClient is the test/dev DockerClient. It records every call so
// tests can assert on the sequence and emits Inspect data the install pipeline
// expects. Containers transition through 'created' -> 'running' on Start, and
// the simulated healthcheck reports 'healthy' after FakeHealthDelay elapses.
type FakeDockerClient struct {
	mu sync.Mutex

	// Pings counts the Ping invocations.
	Pings int

	// Pulled records every PullImage ref, in order.
	Pulled []string

	// Networks is the set of EnsureNetwork names ever requested.
	Networks map[string]bool

	// Containers indexes containers by name.
	Containers map[string]*FakeContainer

	// FakeHealthDelay controls how long after Start the container reports
	// healthy. Zero means immediately.
	FakeHealthDelay time.Duration

	// FailPullRefs is a set of image refs PullImage should reject. Used to
	// exercise the install-rollback path without a real registry.
	FailPullRefs map[string]bool

	// FailHealthNames is the set of container names that should never go
	// healthy (Inspect keeps reporting 'unhealthy'). Used to test
	// healthcheck-failure rollback (AC-S65b510-1-2).
	FailHealthNames map[string]bool

	// Now overrides time.Now for deterministic StartAt comparisons.
	Now func() time.Time
}

// FakeContainer is one in-memory container.
type FakeContainer struct {
	ID      string
	Name    string
	Image   string
	State   string
	Health  string
	Labels  map[string]string
	StartAt time.Time
	// startedAtReal is the wall-clock time Start was called; FakeHealthDelay
	// is measured against it. Distinct from StartAt so tests can override
	// the displayed StartAt without affecting the health-delay clock.
	startedAtReal time.Time
}

// NewFakeDockerClient returns an empty FakeDockerClient ready for use.
func NewFakeDockerClient() *FakeDockerClient {
	return &FakeDockerClient{
		Networks:        map[string]bool{},
		Containers:      map[string]*FakeContainer{},
		FailPullRefs:    map[string]bool{},
		FailHealthNames: map[string]bool{},
	}
}

func (f *FakeDockerClient) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}

// Ping is always successful for the Fake; tests that want to exercise the
// "daemon unreachable" path use a wrapping client that returns
// ErrDockerUnavailable.
func (f *FakeDockerClient) Ping(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Pings++
	return nil
}

// PullImage records the pull and respects FailPullRefs.
func (f *FakeDockerClient) PullImage(_ context.Context, ref string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Pulled = append(f.Pulled, ref)
	if f.FailPullRefs[ref] {
		return fmt.Errorf("fake: pull %s: simulated failure", ref)
	}
	return nil
}

// CreateContainer registers a container in the 'created' state.
func (f *FakeDockerClient) CreateContainer(_ context.Context, spec ContainerSpec) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, exists := f.Containers[spec.Name]; exists {
		return "", fmt.Errorf("fake: container %s already exists", spec.Name)
	}
	id := "fake-" + spec.Name
	f.Containers[spec.Name] = &FakeContainer{
		ID:     id,
		Name:   spec.Name,
		Image:  spec.Image,
		State:  "created",
		Health: "",
		Labels: copyLabels(spec.Labels),
	}
	return id, nil
}

// StartContainer flips the container to 'running' and stamps StartAt.
func (f *FakeDockerClient) StartContainer(_ context.Context, idOrName string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.findLocked(idOrName)
	if c == nil {
		return fmt.Errorf("%w: %s", ErrContainerNotFound, idOrName)
	}
	c.State = "running"
	c.startedAtReal = f.now()
	c.StartAt = c.startedAtReal
	if f.FailHealthNames[c.Name] {
		c.Health = "unhealthy"
	} else {
		c.Health = "starting"
	}
	return nil
}

// StopContainer flips state to 'exited'.
func (f *FakeDockerClient) StopContainer(_ context.Context, idOrName string, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.findLocked(idOrName)
	if c == nil {
		return fmt.Errorf("%w: %s", ErrContainerNotFound, idOrName)
	}
	c.State = "exited"
	c.Health = ""
	return nil
}

// RemoveContainer drops the entry. force=true is required when state=running
// or RemoveContainer returns an error mirroring docker's behavior.
func (f *FakeDockerClient) RemoveContainer(_ context.Context, idOrName string, force bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.findLocked(idOrName)
	if c == nil {
		return fmt.Errorf("%w: %s", ErrContainerNotFound, idOrName)
	}
	if c.State == "running" && !force {
		return fmt.Errorf("fake: container %s is running (use force)", c.Name)
	}
	delete(f.Containers, c.Name)
	return nil
}

// InspectContainer reports current state + simulated health.
func (f *FakeDockerClient) InspectContainer(_ context.Context, idOrName string) (ContainerInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.findLocked(idOrName)
	if c == nil {
		return ContainerInfo{}, fmt.Errorf("%w: %s", ErrContainerNotFound, idOrName)
	}
	if c.State == "running" && c.Health == "starting" {
		if f.FakeHealthDelay == 0 || f.now().Sub(c.startedAtReal) >= f.FakeHealthDelay {
			c.Health = "healthy"
		}
	}
	return ContainerInfo{
		ID:      c.ID,
		Name:    c.Name,
		Image:   c.Image,
		State:   c.State,
		Health:  c.Health,
		Labels:  copyLabels(c.Labels),
		StartAt: c.StartAt,
	}, nil
}

// ListContainersByLabel returns every container that carries every k=v.
func (f *FakeDockerClient) ListContainersByLabel(_ context.Context, labels map[string]string) ([]ContainerInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []ContainerInfo{}
	for _, c := range f.Containers {
		match := true
		for k, v := range labels {
			if c.Labels[k] != v {
				match = false
				break
			}
		}
		if match {
			out = append(out, ContainerInfo{
				ID:     c.ID,
				Name:   c.Name,
				Image:  c.Image,
				State:  c.State,
				Health: c.Health,
				Labels: copyLabels(c.Labels),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// EnsureNetwork records the request.
func (f *FakeDockerClient) EnsureNetwork(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Networks[name] = true
	return nil
}

// findLocked returns the container with name == idOrName or whose ID
// equals idOrName. Caller must hold f.mu.
func (f *FakeDockerClient) findLocked(idOrName string) *FakeContainer {
	if c, ok := f.Containers[idOrName]; ok {
		return c
	}
	for _, c := range f.Containers {
		if c.ID == idOrName {
			return c
		}
	}
	return nil
}

// MarkHealthy is a test helper: synchronously flips the container's health
// to 'healthy'. Used when a test wants to skip the FakeHealthDelay clock.
func (f *FakeDockerClient) MarkHealthy(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.findLocked(name)
	if c == nil {
		return ErrContainerNotFound
	}
	c.Health = "healthy"
	return nil
}

func copyLabels(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// formatPortMap prints a port binding list in a stable form so error
// messages and audit logs read deterministically.
func formatPortMap(pm []PortBinding) string {
	parts := make([]string, 0, len(pm))
	for _, p := range pm {
		parts = append(parts, fmt.Sprintf("%d:%d/%s", p.HostPort, p.ContainerPort, p.Protocol))
	}
	return strings.Join(parts, ",")
}

// drainCloser drains and closes r. Used by the docker SDK pull stream.
func drainCloser(r io.ReadCloser) {
	if r == nil {
		return
	}
	_, _ = io.Copy(io.Discard, r)
	_ = r.Close()
}

var _ DockerClient = (*FakeDockerClient)(nil)
