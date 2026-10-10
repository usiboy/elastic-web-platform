package routing

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"elastic-web-platform/internal/bundle"
)

func static(t *testing.T, team, project, text string) *bundle.Static {
	t.Helper()
	b, err := bundle.CaptureStatic(team, project, "source-hash", "operator-reviewed-static:v1", map[string][]byte{"/index.html": []byte(text), "/app.js": []byte("console.log(1)")})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAliasRollbackAndFixedDeploymentNeverMove(t *testing.T) {
	r := NewRegistry()
	one, err := r.Register(static(t, "t_a", "p_a", "one"))
	if err != nil {
		t.Fatal(err)
	}
	two, err := r.Register(static(t, "t_a", "p_a", "two"))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.BindFixed("d-one.preview.test", one); err != nil {
		t.Fatal(err)
	}
	if err := r.BindFixed("d-one.preview.test", two); err == nil {
		t.Fatal("fixed deployment URL rebound")
	}
	if _, err := r.SwapAlias("site.example.test", "t_a", "p_a", 0, one, false); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SwapAlias("site.example.test", "t_a", "p_a", 0, two, false); err == nil {
		t.Fatal("stale page overwrote generation")
	}
	if _, err := r.SwapAlias("site.example.test", "t_other", "p_other", 1, two, false); err == nil {
		t.Fatal("cross-team host takeover")
	}
	if gen, err := r.SwapAlias("site.example.test", "t_a", "p_a", 1, two, false); err != nil || gen != 2 {
		t.Fatalf("promote: %d %v", gen, err)
	}
	if gen, err := r.SwapAlias("site.example.test", "t_a", "p_a", 2, one, false); err != nil || gen != 3 {
		t.Fatalf("rollback: %d %v", gen, err)
	}
	if got, ok := r.Resolve("d-one.preview.test"); !ok || got.DeploymentID != one {
		t.Fatalf("fixed url drifted: %+v", got)
	}
	if got, ok := r.Resolve("site.example.test"); !ok || got.DeploymentID != one || got.Generation != 3 {
		t.Fatalf("rollback target: %+v", got)
	}
}

func TestGatewayRequiresTLSAndGuardsAllPreviewResources(t *testing.T) {
	r := NewRegistry()
	id, err := r.Register(static(t, "t_a", "p_a", "tenant A"))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.BindFixed("d-one.preview.test", id); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SwapAlias("site.example.test", "t_a", "p_a", 0, id, false); err != nil {
		t.Fatal(err)
	}
	allow := false
	h := NewGateway(r, func(_ *http.Request, route Route) bool { return allow && route.TeamID == "t_a" })
	request := func(host, path string, https bool) *httptest.ResponseRecorder {
		t.Helper()
		scheme := "http"
		if https {
			scheme = "https"
		}
		req := httptest.NewRequest(http.MethodGet, scheme+"://"+host+path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	if w := request("d-one.preview.test", "/index.html", true); w.Code != 401 {
		t.Fatalf("anonymous preview HTML: %d", w.Code)
	}
	if w := request("d-one.preview.test", "/app.js", true); w.Code != 401 {
		t.Fatalf("anonymous preview JS: %d", w.Code)
	}
	allow = true
	if w := request("d-one.preview.test", "/index.html", true); w.Code != 200 || !strings.Contains(w.Body.String(), "tenant A") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("protected preview: %d %q", w.Code, w.Body.String())
	}
	if w := request("site.example.test", "/", true); w.Code != 200 || !strings.Contains(w.Body.String(), "tenant A") {
		t.Fatalf("production: %d %q", w.Code, w.Body.String())
	}
	if w := request("site.example.test", "/app.js", false); w.Code != 421 {
		t.Fatalf("plaintext site: %d", w.Code)
	}
	if w := request("other.example.test", "/", true); w.Code != 404 {
		t.Fatalf("unbound Host: %d", w.Code)
	}
	if w := request("site.example.test", "/missing.js", true); w.Code != 404 {
		t.Fatalf("missing JS fell through to HTML: %d", w.Code)
	}
}
