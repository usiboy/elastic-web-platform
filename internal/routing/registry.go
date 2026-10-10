package routing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"elastic-web-platform/internal/bundle"
)

type Route struct {
	TeamID       string
	ProjectID    string
	DeploymentID string
	Generation   int64
	Private      bool
}

type routeEntry struct {
	Route
	fixed     bool
	previous  string
	fallbacks []string // 最近的健康版本优先；不向租户暴露。
}

type runtimeEntry struct {
	team, project string
	upstream      *url.URL
	slots         chan struct{}
	timeout       time.Duration
	client        *http.Client
}

type Registry struct {
	mu          sync.RWMutex
	deployments map[string]*bundle.Static
	runtimes    map[string]*runtimeEntry
	hosts       map[string]routeEntry
}

func NewRegistry() *Registry {
	return &Registry{deployments: make(map[string]*bundle.Static), runtimes: make(map[string]*runtimeEntry), hosts: make(map[string]routeEntry)}
}

var runtimeID = regexp.MustCompile(`^dep_[0-9a-f]{32}$`)

func ValidRuntimeDeploymentID(id string) bool { return runtimeID.MatchString(id) }

// RegisterRuntime is an internal, single-node probe contract. No tenant API
// can select a target; durable admission must separately prove sandbox origin.
func (r *Registry) RegisterRuntime(team, project, id, endpoint string, concurrency int, timeout time.Duration) error {
	u, err := url.Parse(endpoint)
	if err != nil {
		return errors.New("runtime target is not an admitted local endpoint")
	}
	unixSocket := u.Scheme == "unix" && strings.HasPrefix(u.Path, "/run/ewp-r2-") && strings.HasSuffix(u.Path, ".sock") && u.Host == "" && !strings.Contains(u.Path, "..")
	port, portErr := strconv.Atoi(u.Port())
	loopback := u.Scheme == "http" && portErr == nil && port >= 1024 && port <= 65535 && u.Hostname() == "127.0.0.1" && u.Path == ""
	if (!unixSocket && !loopback) || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		team == "" || project == "" || !runtimeID.MatchString(id) || concurrency < 1 || concurrency > 32 || timeout < 100*time.Millisecond || timeout > 60*time.Second {
		return errors.New("runtime target is not an admitted local endpoint")
	}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	if unixSocket {
		path := u.Path
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", path)
		}
		u = &url.URL{Scheme: "http", Host: "runtime.internal"}
	}
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.runtimes[id]; exists || r.deployments[id] != nil {
		return errors.New("runtime deployment already exists")
	}
	r.runtimes[id] = &runtimeEntry{team: team, project: project, upstream: u, slots: make(chan struct{}, concurrency), timeout: timeout, client: client}
	return nil
}

func normalizedHost(host string) (string, error) {
	if host == "" || host != strings.ToLower(host) || strings.ContainsAny(host, "/:\\ ") || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") || strings.Contains(host, "..") {
		return "", errors.New("invalid host")
	}
	return host, nil
}

// Register records already captured static output. It does not build tenant
// code, grant a public domain or prove that an origin is healthy.
func (r *Registry) Register(b *bundle.Static) (string, error) {
	if b == nil || b.Verify() != nil {
		return "", errors.New("invalid static bundle")
	}
	copy, err := bundle.CaptureStatic(b.Manifest.TeamID, b.Manifest.ProjectID, b.Manifest.SourceSHA256, b.Manifest.BuilderDigest, b.Files())
	if err != nil || copy.Manifest.Digest != b.Manifest.Digest {
		return "", errors.New("static bundle changed while registering")
	}
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	id := "dep_" + hex.EncodeToString(bytes)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deployments[id] = copy
	return id, nil
}

func (r *Registry) BindFixed(host, deploymentID string) error {
	host, err := normalizedHost(host)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	b := r.deployments[deploymentID]
	runtime := r.runtimes[deploymentID]
	if (b == nil || b.Verify() != nil) && runtime == nil {
		return errors.New("deployment is not available")
	}
	if old, occupied := r.hosts[host]; occupied {
		if old.fixed && old.DeploymentID == deploymentID {
			return nil
		}
		return errors.New("fixed host cannot be reassigned")
	}
	team, project := runtimeScope(b, runtime)
	r.hosts[host] = routeEntry{Route: Route{TeamID: team, ProjectID: project, DeploymentID: deploymentID, Generation: 1, Private: true}, fixed: true}
	return nil
}

func (r *Registry) SwapAlias(host, teamID, projectID string, expected int64, deploymentID string, private bool) (int64, error) {
	host, err := normalizedHost(host)
	if err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	b := r.deployments[deploymentID]
	runtime := r.runtimes[deploymentID]
	if (b == nil || b.Verify() != nil) && runtime == nil {
		return 0, errors.New("target deployment is not owned and verified")
	}
	targetTeam, targetProject := runtimeScope(b, runtime)
	if targetTeam != teamID || targetProject != projectID {
		return 0, errors.New("target deployment is not owned and verified")
	}
	old, occupied := r.hosts[host]
	if occupied && (old.fixed || old.TeamID != teamID || old.ProjectID != projectID || old.Private != private) {
		return 0, errors.New("host is bound to another scope")
	}
	if (occupied && old.Generation != expected) || (!occupied && expected != 0) {
		return 0, errors.New("alias generation conflict")
	}
	next := expected + 1
	previous := ""
	var fallbacks []string
	if occupied {
		previous = old.DeploymentID
		fallbacks = append(fallbacks, old.DeploymentID)
		fallbacks = append(fallbacks, old.fallbacks...)
	}
	r.hosts[host] = routeEntry{Route: Route{TeamID: teamID, ProjectID: projectID, DeploymentID: deploymentID, Generation: next, Private: private}, previous: previous, fallbacks: fallbacks}
	return next, nil
}

func runtimeScope(b *bundle.Static, runtime *runtimeEntry) (string, string) {
	if runtime != nil {
		return runtime.team, runtime.project
	}
	return b.Manifest.TeamID, b.Manifest.ProjectID
}

func (r *Registry) rollbackRuntime(host string, route Route, targetID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.hosts[host]
	if !ok || entry.fixed || entry.Route != route {
		return
	}
	selected := -1
	for i, id := range entry.fallbacks {
		if targetID != "" && id != targetID {
			continue
		}
		previous := r.deployments[id]
		live := r.runtimes[id]
		if previous != nil && previous.Verify() == nil && previous.Manifest.TeamID == route.TeamID && previous.Manifest.ProjectID == route.ProjectID {
			selected = i
			break
		}
		if live != nil && live.team == route.TeamID && live.project == route.ProjectID {
			selected = i
			break
		}
	}
	if selected < 0 {
		return
	}
	chosen := entry.fallbacks[selected]
	r.hosts[host] = routeEntry{Route: Route{TeamID: route.TeamID, ProjectID: route.ProjectID, DeploymentID: chosen, Generation: route.Generation + 1, Private: route.Private}, fallbacks: append([]string(nil), entry.fallbacks[selected+1:]...)}
	for hostname, bound := range r.hosts {
		if bound.fixed && bound.DeploymentID == route.DeploymentID {
			delete(r.hosts, hostname)
		}
	}
}

func (r *Registry) Resolve(host string) (Route, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.hosts[host]
	return entry.Route, ok
}

func (r *Registry) resolveBundle(host string) (Route, *bundle.Static, *runtimeEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.hosts[host]
	if !ok {
		return Route{}, nil, nil, false
	}
	b := r.deployments[entry.DeploymentID]
	runtime := r.runtimes[entry.DeploymentID]
	return entry.Route, b, runtime, b != nil || runtime != nil
}
