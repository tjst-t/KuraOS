package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HTTPDockerClient is the production DockerClient. It speaks Docker Engine
// API v1.43 over /var/run/docker.sock (or any custom socket / TCP host) using
// stdlib net/http. We deliberately do not depend on the docker/docker SDK:
//
//   - Phase 1 priority #6 (use libraries for differentiating things only) —
//     Docker Engine API is stable enough that a 200-line wrapper is a better
//     trade than the SDK's 50+ MB transitive dep tree (kubelet, gRPC, ...).
//   - Phase 2 NixOS migration may swap to podman; an HTTP wrapper is trivial
//     to point at podman's socket without re-pinning a SDK version.
//   - The wrapper still satisfies the DockerClient interface so tests keep
//     using FakeDockerClient — production swap is a single field change.
//
// Caller responsibilities:
//   - The kura process must have read+write access to the socket (typically
//     by being root or in the docker group).
//   - Image pulls block on the daemon side; PullImage waits for the stream to
//     close. Long pulls thus serialize through Install/Update — acceptable
//     because the install is a one-shot operator action with SSE feedback.
type HTTPDockerClient struct {
	HTTP *http.Client
	// Base is the API base URL fragment (e.g. "http://docker"). The Host
	// component is irrelevant for unix sockets but required for net/http
	// to construct a valid Request.
	Base string
}

// NewHTTPDockerClient returns a client wired to /var/run/docker.sock. host
// may be overridden via DOCKER_HOST env var ("unix:///path", "tcp://h:port").
func NewHTTPDockerClient(host string) *HTTPDockerClient {
	if host == "" {
		host = "unix:///var/run/docker.sock"
	}
	transport := &http.Transport{
		DisableCompression: true,
	}
	switch {
	case strings.HasPrefix(host, "unix://"):
		path := strings.TrimPrefix(host, "unix://")
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", path)
		}
	case strings.HasPrefix(host, "tcp://"):
		transport.DialContext = (&net.Dialer{Timeout: 5 * time.Second}).DialContext
	}
	return &HTTPDockerClient{
		HTTP: &http.Client{Transport: transport, Timeout: 0},
		Base: "http://docker",
	}
}

// Ping hits /_ping. 200 OK = daemon up.
func (c *HTTPDockerClient) Ping(ctx context.Context) error {
	resp, err := c.do(ctx, http.MethodGet, "/_ping", nil)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDockerUnavailable, err)
	}
	defer drainCloser(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: ping returned %d", ErrDockerUnavailable, resp.StatusCode)
	}
	return nil
}

// PullImage triggers POST /images/create?fromImage=<ref>. The response is a
// chunked JSON stream of progress events; we drain it without surfacing
// per-layer progress (the install pipeline emits a single "pulling" event).
func (c *HTTPDockerClient) PullImage(ctx context.Context, ref string) error {
	q := url.Values{"fromImage": {ref}}
	resp, err := c.do(ctx, http.MethodPost, "/images/create?"+q.Encode(), nil)
	if err != nil {
		return fmt.Errorf("docker: pull %s: %w", ref, err)
	}
	defer drainCloser(resp.Body)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("docker: pull %s: HTTP %d: %s", ref, resp.StatusCode, string(body))
	}
	// Drain the progress stream so the daemon does not back-pressure.
	dec := json.NewDecoder(resp.Body)
	for {
		var evt map[string]any
		if err := dec.Decode(&evt); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return fmt.Errorf("docker: pull %s: stream: %w", ref, err)
		}
		if errStr, _ := evt["error"].(string); errStr != "" {
			return fmt.Errorf("docker: pull %s: %s", ref, errStr)
		}
	}
	return nil
}

// CreateContainer POSTs /containers/create with the JSON-encoded config.
func (c *HTTPDockerClient) CreateContainer(ctx context.Context, spec ContainerSpec) (string, error) {
	body, err := json.Marshal(buildContainerCreatePayload(spec))
	if err != nil {
		return "", fmt.Errorf("docker: encode create: %w", err)
	}
	q := url.Values{"name": {spec.Name}}
	resp, err := c.do(ctx, http.MethodPost, "/containers/create?"+q.Encode(), bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer drainCloser(resp.Body)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("docker: create %s: HTTP %d: %s", spec.Name, resp.StatusCode, string(b))
	}
	var out struct {
		ID       string   `json:"Id"`
		Warnings []string `json:"Warnings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("docker: decode create response: %w", err)
	}
	return out.ID, nil
}

// StartContainer POSTs /containers/<id>/start.
func (c *HTTPDockerClient) StartContainer(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/start", nil)
	if err != nil {
		return err
	}
	defer drainCloser(resp.Body)
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotModified {
		return nil
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("docker: start %s: HTTP %d: %s", id, resp.StatusCode, string(b))
}

// StopContainer POSTs /containers/<id>/stop?t=<seconds>.
func (c *HTTPDockerClient) StopContainer(ctx context.Context, id string, timeout time.Duration) error {
	t := int(timeout.Seconds())
	if t < 1 {
		t = 10
	}
	q := url.Values{"t": {fmt.Sprintf("%d", t)}}
	resp, err := c.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/stop?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	defer drainCloser(resp.Body)
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotModified {
		return nil
	}
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: %s", ErrContainerNotFound, id)
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("docker: stop %s: HTTP %d: %s", id, resp.StatusCode, string(b))
}

// RemoveContainer DELETEs /containers/<id>?force=...
func (c *HTTPDockerClient) RemoveContainer(ctx context.Context, id string, force bool) error {
	q := url.Values{}
	if force {
		q.Set("force", "true")
	}
	q.Set("v", "true") // remove anonymous volumes
	resp, err := c.do(ctx, http.MethodDelete, "/containers/"+url.PathEscape(id)+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	defer drainCloser(resp.Body)
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK {
		return nil
	}
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: %s", ErrContainerNotFound, id)
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("docker: remove %s: HTTP %d: %s", id, resp.StatusCode, string(b))
}

// InspectContainer GETs /containers/<id>/json.
func (c *HTTPDockerClient) InspectContainer(ctx context.Context, id string) (ContainerInfo, error) {
	resp, err := c.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/json", nil)
	if err != nil {
		return ContainerInfo{}, err
	}
	defer drainCloser(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return ContainerInfo{}, fmt.Errorf("%w: %s", ErrContainerNotFound, id)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return ContainerInfo{}, fmt.Errorf("docker: inspect %s: HTTP %d: %s", id, resp.StatusCode, string(b))
	}
	var raw struct {
		ID    string `json:"Id"`
		Name  string `json:"Name"`
		Image string `json:"Image"`
		State struct {
			Status    string `json:"Status"`
			StartedAt string `json:"StartedAt"`
			Health    *struct {
				Status string `json:"Status"`
			} `json:"Health"`
		} `json:"State"`
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return ContainerInfo{}, fmt.Errorf("docker: decode inspect %s: %w", id, err)
	}
	info := ContainerInfo{
		ID:     raw.ID,
		Name:   strings.TrimPrefix(raw.Name, "/"),
		Image:  raw.Image,
		State:  raw.State.Status,
		Labels: raw.Config.Labels,
	}
	if raw.State.Health != nil {
		info.Health = strings.ToLower(raw.State.Health.Status)
	}
	if raw.State.StartedAt != "" {
		if t, err := time.Parse(time.RFC3339Nano, raw.State.StartedAt); err == nil {
			info.StartAt = t
		}
	}
	return info, nil
}

// ListContainersByLabel GETs /containers/json?filters=...
func (c *HTTPDockerClient) ListContainersByLabel(ctx context.Context, labels map[string]string) ([]ContainerInfo, error) {
	labelFilter := []string{}
	for k, v := range labels {
		labelFilter = append(labelFilter, k+"="+v)
	}
	filtersJSON, _ := json.Marshal(map[string][]string{"label": labelFilter})
	q := url.Values{
		"all":     {"true"},
		"filters": {string(filtersJSON)},
	}
	resp, err := c.do(ctx, http.MethodGet, "/containers/json?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	defer drainCloser(resp.Body)
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("docker: list: HTTP %d: %s", resp.StatusCode, string(b))
	}
	var raw []struct {
		ID     string            `json:"Id"`
		Names  []string          `json:"Names"`
		Image  string            `json:"Image"`
		State  string            `json:"State"`
		Status string            `json:"Status"`
		Labels map[string]string `json:"Labels"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("docker: decode list: %w", err)
	}
	out := make([]ContainerInfo, 0, len(raw))
	for _, r := range raw {
		info := ContainerInfo{
			ID:     r.ID,
			Image:  r.Image,
			State:  r.State,
			Labels: r.Labels,
		}
		if len(r.Names) > 0 {
			info.Name = strings.TrimPrefix(r.Names[0], "/")
		}
		out = append(out, info)
	}
	return out, nil
}

// EnsureNetwork POSTs /networks/create. A 409 (already exists) is treated as
// success.
func (c *HTTPDockerClient) EnsureNetwork(ctx context.Context, name string) error {
	body, _ := json.Marshal(map[string]any{
		"Name":   name,
		"Driver": "bridge",
	})
	resp, err := c.do(ctx, http.MethodPost, "/networks/create", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer drainCloser(resp.Body)
	switch resp.StatusCode {
	case http.StatusCreated, http.StatusOK, http.StatusConflict:
		return nil
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("docker: ensure network %s: HTTP %d: %s", name, resp.StatusCode, string(b))
}

// do is the shared request runner.
func (c *HTTPDockerClient) do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.HTTP.Do(req)
}

// buildContainerCreatePayload turns ContainerSpec into the docker engine API
// /containers/create JSON shape.
func buildContainerCreatePayload(spec ContainerSpec) map[string]any {
	envSlice := make([]string, 0, len(spec.Env))
	for k, v := range spec.Env {
		envSlice = append(envSlice, k+"="+v)
	}
	exposed := map[string]struct{}{}
	bindings := map[string][]map[string]string{}
	for _, p := range spec.PortMap {
		key := fmt.Sprintf("%d/%s", p.ContainerPort, p.Protocol)
		exposed[key] = struct{}{}
		bindings[key] = []map[string]string{{"HostPort": fmt.Sprintf("%d", p.HostPort)}}
	}
	mounts := []map[string]any{}
	for _, m := range spec.Mounts {
		entry := map[string]any{
			"Type":     m.Type,
			"Source":   m.Source,
			"Target":   m.Target,
			"ReadOnly": m.ReadOnly,
		}
		mounts = append(mounts, entry)
	}
	hostConfig := map[string]any{
		"PortBindings":  bindings,
		"NetworkMode":   spec.Network,
		"Mounts":        mounts,
		"RestartPolicy": map[string]any{"Name": spec.Restart},
	}
	cfg := map[string]any{
		"Image":        spec.Image,
		"Env":          envSlice,
		"Labels":       spec.Labels,
		"ExposedPorts": exposed,
		"Cmd":          spec.Cmd,
		"Entrypoint":   spec.Entrypoint,
		"WorkingDir":   spec.WorkingDir,
		"HostConfig":   hostConfig,
	}
	if spec.Healthcheck != nil {
		cfg["Healthcheck"] = map[string]any{
			"Test":     spec.Healthcheck.Test,
			"Interval": int64(spec.Healthcheck.Interval),
			"Timeout":  int64(spec.Healthcheck.Timeout),
			"Retries":  spec.Healthcheck.Retries,
		}
	}
	if spec.Network != "" {
		cfg["NetworkingConfig"] = map[string]any{
			"EndpointsConfig": map[string]any{
				spec.Network: map[string]any{},
			},
		}
	}
	return cfg
}

var _ DockerClient = (*HTTPDockerClient)(nil)
