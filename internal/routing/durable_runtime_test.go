package routing

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"elastic-web-platform/internal/bundle"
)

func TestDurableRuntimeAliasRollsBackToStaticAfterTimeout(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/health":
			w.WriteHeader(http.StatusOK)
		case "/hang":
			select {
			case <-r.Context().Done():
			case <-time.After(500 * time.Millisecond):
			}
		default:
			w.Write([]byte("runtime:" + r.URL.Path))
		}
	}))
	defer server.Close()
	_, portString, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portString)
	if err != nil {
		t.Fatal(err)
	}
	db := routeDatabase(t)
	control := NewDurableRegistry(db, bundle.NewDiskStore(t.TempDir()))
	old, err := control.RegisterOperatorFixture(ctx, static(t, "t_alpha", "p_alpha", "known-good"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = control.SwapAlias(ctx, "site.example.test", "t_alpha", "p_alpha", 0, old, false); err != nil {
		t.Fatal(err)
	}
	id, err := control.RegisterRuntimePilot(ctx, "t_alpha", "p_alpha", strings.Repeat("c", 64), port, 2, 150*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = control.SwapAlias(ctx, "site.example.test", "t_other", "p_other", 1, id, false); err == nil {
		t.Fatal("different Team claimed runtime deployment")
	}
	if err = control.BindFixed(ctx, "d-runtime.preview.test", id); err != nil {
		t.Fatal(err)
	}
	if _, err = control.SwapAlias(ctx, "site.example.test", "t_alpha", "p_alpha", 1, id, false); err != nil {
		t.Fatal(err)
	}
	snapshot, err := control.LoadSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	h := NewGatewayWithSessions(snapshot, nil, control)
	request := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "https://site.example.test"+path, nil))
		return w
	}
	if w := request("/api/run"); w.Code != 200 || w.Body.String() != "runtime:/api/run" {
		t.Fatalf("runtime route not read back: %d %s", w.Code, w.Body.String())
	}
	if w := request("/hang"); w.Code != 504 {
		t.Fatalf("hung runtime did not time out: %d", w.Code)
	}
	var activeID string
	var activeGeneration int64
	if err := db.QueryRow(ctx, `SELECT deployment_id,generation FROM site_hosts WHERE hostname='site.example.test'`).Scan(&activeID, &activeGeneration); err != nil {
		t.Fatal(err)
	}
	if w := request("/"); w.Code != 200 || w.Body.String() != "known-good" {
		t.Fatalf("old deployment not served after runtime failure: %d %s active=%s gen=%d", w.Code, w.Body.String(), activeID, activeGeneration)
	}
	loaded, err := control.LoadSnapshot(ctx)
	if err != nil {
		t.Fatalf("rollback did not survive reload: %v", err)
	}
	if route, ok := loaded.Resolve("site.example.test"); !ok || route.DeploymentID != old || route.Generation != 3 {
		t.Fatalf("durable alias did not roll back: %+v", route)
	}
	if _, ok := loaded.Resolve("d-runtime.preview.test"); ok {
		t.Fatal("failed dynamic Preview remained public after reload")
	}
	var state string
	if err := db.QueryRow(ctx, `SELECT state FROM site_runtime_pilots WHERE deployment_id=$1`, id).Scan(&state); err != nil || state != "failed" {
		t.Fatalf("runtime failure not persisted: %q %v", state, err)
	}
}

func TestGatewayRestartRecoversWhenRuntimeDisappears(t *testing.T) {
	ctx := context.Background()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	_, portString, err := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portString)
	if err != nil {
		t.Fatal(err)
	}
	db := routeDatabase(t)
	control := NewDurableRegistry(db, bundle.NewDiskStore(t.TempDir()))
	old, err := control.RegisterOperatorFixture(ctx, static(t, "t_alpha", "p_alpha", "old-site"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.SwapAlias(ctx, "site.example.test", "t_alpha", "p_alpha", 0, old, false); err != nil {
		t.Fatal(err)
	}
	id, err := control.RegisterRuntimePilot(ctx, "t_alpha", "p_alpha", strings.Repeat("d", 64), port, 1, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := control.BindFixed(ctx, "d-runtime.preview.test", id); err != nil {
		t.Fatal(err)
	}
	if _, err := control.SwapAlias(ctx, "site.example.test", "t_alpha", "p_alpha", 1, id, false); err != nil {
		t.Fatal(err)
	}
	upstream.Close()
	snapshot, err := control.LoadSnapshot(ctx)
	if err != nil {
		t.Fatalf("old static site blocked by missing runtime: %v", err)
	}
	if route, ok := snapshot.Resolve("site.example.test"); !ok || route.DeploymentID != old || route.Generation != 3 {
		t.Fatalf("runtime outage did not recover static alias: %+v", route)
	}
	if _, ok := snapshot.Resolve("d-runtime.preview.test"); ok {
		t.Fatal("failed runtime Preview exposed on restart")
	}
}

func TestRuntimeHealthUsesUnixSocketWithoutHostNetwork(t *testing.T) {
	temporary, err := os.CreateTemp("", "r2-*.sock")
	if err != nil {
		t.Fatal(err)
	}
	socket := temporary.Name()
	temporary.Close()
	if err := os.Remove(socket); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(socket) })
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/health" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	})}
	go server.Serve(listener)
	defer server.Close()
	if err := probeRuntime(context.Background(), 0, socket); err != nil {
		t.Fatalf("Unix runtime health failed: %v", err)
	}
	if err := probeRuntime(context.Background(), 0, socket+"-missing"); err == nil {
		t.Fatal("missing runtime socket accepted")
	}
}
