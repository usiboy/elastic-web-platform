package routing

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

const maxRuntimeRequest = 1 << 20

func stripHopHeaders(headers http.Header) {
	for _, value := range headers.Values("Connection") {
		for _, name := range strings.Split(value, ",") {
			headers.Del(strings.TrimSpace(name))
		}
	}
	for _, name := range []string{"Connection", "Keep-Alive", "Proxy-Connection", "Proxy-Authenticate", "Proxy-Authorization", "TE", "Trailer", "Transfer-Encoding", "Upgrade"} {
		headers.Del(name)
	}
}

func (g *Gateway) serveRuntime(w http.ResponseWriter, r *http.Request, host string, route Route, runtime *runtimeEntry) {
	if r.Method == http.MethodConnect || r.Method == http.MethodTrace || r.URL.Path == "" || path.Clean(r.URL.Path) != r.URL.Path || strings.ContainsAny(r.URL.Path, "\\\x00") {
		http.Error(w, "unsupported runtime request", http.StatusBadRequest)
		return
	}
	select {
	case runtime.slots <- struct{}{}:
		defer func() { <-runtime.slots }()
	default:
		http.Error(w, "runtime concurrency limit", http.StatusServiceUnavailable)
		return
	}
	var body []byte
	if r.Body != nil {
		var err error
		body, err = io.ReadAll(io.LimitReader(r.Body, maxRuntimeRequest+1))
		if err != nil {
			http.Error(w, "invalid runtime body", http.StatusBadRequest)
			return
		}
		if len(body) > maxRuntimeRequest {
			http.Error(w, "runtime request too large", http.StatusRequestEntityTooLarge)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), runtime.timeout)
	defer cancel()
	target := &url.URL{Scheme: runtime.upstream.Scheme, Host: runtime.upstream.Host, Path: r.URL.Path, RawQuery: r.URL.RawQuery}
	outbound, err := http.NewRequestWithContext(ctx, r.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		http.Error(w, "runtime request unavailable", http.StatusBadRequest)
		return
	}
	outbound.Header = r.Header.Clone()
	stripHopHeaders(outbound.Header)
	outbound.Header.Del("Authorization")
	outbound.Header.Del("Forwarded")
	outbound.Header.Del("X-Forwarded-For")
	outbound.Header.Del("X-Forwarded-Host")
	outbound.Header.Del("X-Forwarded-Proto")
	outbound.Header.Del("X-Real-Ip")
	outbound.Header.Del("Cookie")
	for _, cookie := range r.Cookies() {
		if cookie.Name != previewCookie {
			outbound.AddCookie(cookie)
		}
	}
	outbound.Host = runtime.upstream.Host
	response, err := runtime.client.Do(outbound)
	if err != nil {
		g.failRuntime(host, route)
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			http.Error(w, "runtime timeout", http.StatusGatewayTimeout)
		} else {
			http.Error(w, "runtime unavailable", http.StatusServiceUnavailable)
		}
		return
	}
	defer response.Body.Close()
	stripHopHeaders(response.Header)
	for name, values := range response.Header {
		for _, value := range values {
			if strings.EqualFold(name, "Set-Cookie") && strings.HasPrefix(strings.ToLower(value), strings.ToLower(previewCookie)+"=") {
				continue
			}
			w.Header().Add(name, value)
		}
	}
	w.Header().Del("Content-Length")
	if route.Private {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Robots-Tag", "noindex")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(response.StatusCode)
	if r.Method == http.MethodHead {
		return
	}
	buffer := make([]byte, 16<<10)
	for {
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			if _, writeErr := w.Write(buffer[:n]); writeErr != nil {
				return
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				g.failRuntime(host, route)
			}
			return
		}
	}
}

func (g *Gateway) failRuntime(host string, route Route) {
	target := ""
	if durable, ok := g.observer.(interface {
		RollbackRuntime(context.Context, string, Route) (string, error)
	}); ok {
		ctx, done := context.WithTimeout(context.Background(), 2*time.Second)
		defer done()
		selected, err := durable.RollbackRuntime(ctx, host, route)
		if err != nil {
			return
		}
		target = selected
	}
	g.registry.rollbackRuntime(host, route, target)
}
