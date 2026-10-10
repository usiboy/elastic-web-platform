package routing

import (
	"context"
	"net"
	"net/http"
	"path"
	"strconv"
	"strings"
)

type PreviewAuthorizer func(*http.Request, Route) bool

type Gateway struct {
	registry  *Registry
	authorize PreviewAuthorizer
	sessions  *PreviewSessions
	observer  RouteObserver
}

type RouteObserver interface {
	MarkServed(context.Context, Route, string, string) error
}

func NewGateway(registry *Registry, authorize PreviewAuthorizer) http.Handler {
	return &Gateway{registry: registry, authorize: authorize}
}

func NewGatewayWithSessions(registry *Registry, sessions *PreviewSessions, observers ...RouteObserver) http.Handler {
	gateway := &Gateway{registry: registry, sessions: sessions}
	if sessions != nil {
		gateway.authorize = sessions.Authorize
	}
	if len(observers) > 0 {
		gateway.observer = observers[0]
	}
	return gateway
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.TLS == nil {
		http.Error(w, "HTTPS required", http.StatusMisdirectedRequest)
		return
	}
	host := strings.ToLower(r.Host)
	if strings.Contains(host, ":") {
		var err error
		host, _, err = net.SplitHostPort(host)
		if err != nil {
			http.Error(w, "invalid host", http.StatusBadRequest)
			return
		}
	}
	if _, err := normalizedHost(host); err != nil || (r.TLS.ServerName != "" && !strings.EqualFold(r.TLS.ServerName, host)) {
		http.Error(w, "host mismatch", http.StatusMisdirectedRequest)
		return
	}
	route, b, runtime, ok := g.registry.resolveBundle(host)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if r.URL.Path == "/session/exchange" && r.Method == http.MethodPost {
		if !route.Private || g.sessions == nil {
			http.NotFound(w, r)
			return
		}
		g.sessions.Exchange(w, r, route)
		return
	}
	if route.Private {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Robots-Tag", "noindex")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if g.authorize == nil || !g.authorize(r, route) {
			http.Error(w, "preview access required", http.StatusUnauthorized)
			return
		}
	}
	if runtime != nil {
		g.serveRuntime(w, r, host, route, runtime)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := b.Verify(); err != nil {
		http.Error(w, "artifact unavailable", http.StatusServiceUnavailable)
		return
	}
	name := r.URL.Path
	if name == "/" {
		name = "/index.html"
	}
	if name == "" || !strings.HasPrefix(name, "/") || path.Clean(name) != name || strings.ContainsAny(name, "\\\x00") {
		http.NotFound(w, r)
		return
	}
	data, ok := b.Read(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	for _, asset := range b.Manifest.Files {
		if asset.Path == name {
			w.Header().Set("Content-Type", asset.ContentType)
			break
		}
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if !route.Private {
		if name == "/index.html" {
			w.Header().Set("Cache-Control", "no-cache")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=60")
		}
	}
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusOK)
	n, err := w.Write(data)
	if err == nil && n == len(data) && name == "/index.html" && g.observer != nil {
		_ = g.observer.MarkServed(r.Context(), route, host, b.Manifest.Digest)
	}
}
