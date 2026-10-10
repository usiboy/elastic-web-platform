package routing

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"elastic-web-platform/internal/bundle"
)

func TestDurableRuntimeToRuntimeRollbackAndSecondFailureFallsBackToStatic(t *testing.T) {
	ctx := context.Background()
	serve := func(label string) (*httptest.Server, int) {
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
				w.Write([]byte(label))
			}
		}))
		_, portText, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
		if err != nil {
			t.Fatal(err)
		}
		port, err := strconv.Atoi(portText)
		if err != nil {
			t.Fatal(err)
		}
		return server, port
	}
	oldServer, oldPort := serve("dynamic-v1")
	defer oldServer.Close()
	newServer, newPort := serve("dynamic-v2")
	defer newServer.Close()
	db := routeDatabase(t)
	control := NewDurableRegistry(db, bundle.NewDiskStore(t.TempDir()))
	baseline, err := control.RegisterOperatorFixture(ctx, static(t, "t_alpha", "p_alpha", "static-v0"))
	if err != nil {
		t.Fatal(err)
	}
	if gen, err := control.SwapAlias(ctx, "site.example.test", "t_alpha", "p_alpha", 0, baseline, false); err != nil || gen != 1 {
		t.Fatalf("baseline: %d %v", gen, err)
	}
	v1, err := control.RegisterRuntimePilot(ctx, "t_alpha", "p_alpha", strings.Repeat("1", 64), oldPort, 1, 150*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if gen, err := control.SwapAlias(ctx, "site.example.test", "t_alpha", "p_alpha", 1, v1, false); err != nil || gen != 2 {
		t.Fatalf("v1: %d %v", gen, err)
	}
	v2, err := control.RegisterRuntimePilot(ctx, "t_alpha", "p_alpha", strings.Repeat("2", 64), newPort, 1, 150*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if err := control.BindFixed(ctx, "d-v2.preview.test", v2); err != nil {
		t.Fatal(err)
	}
	if gen, err := control.SwapAlias(ctx, "site.example.test", "t_alpha", "p_alpha", 2, v2, false); err != nil || gen != 3 {
		t.Fatalf("v2: %d %v", gen, err)
	}
	snapshot, err := control.LoadSnapshot(ctx)
	if err != nil {
		t.Fatalf("new runtime blocked by prior runtime: %v", err)
	}
	h := NewGatewayWithSessions(snapshot, nil, control)
	request := func(path string) (int, string) {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "https://site.example.test"+path, nil))
		return w.Code, w.Body.String()
	}
	if status, body := request("/"); status != 200 || body != "dynamic-v2" {
		t.Fatalf("v2 not active: %d %s", status, body)
	}
	if status, _ := request("/hang"); status != 504 {
		t.Fatalf("v2 timeout: %d", status)
	}
	if status, body := request("/"); status != 200 || body != "dynamic-v1" {
		t.Fatalf("v1 not restored immediately: %d %s", status, body)
	}
	if snapshot, err = control.LoadSnapshot(ctx); err != nil {
		t.Fatal(err)
	}
	if route, ok := snapshot.Resolve("site.example.test"); !ok || route.DeploymentID != v1 || route.Generation != 4 {
		t.Fatalf("v1 not durable: %+v", route)
	}
	oldServer.Close()
	if snapshot, err = control.LoadSnapshot(ctx); err != nil {
		t.Fatalf("static recovery after two runtime failures: %v", err)
	}
	if route, ok := snapshot.Resolve("site.example.test"); !ok || route.DeploymentID != baseline || route.Generation != 5 {
		t.Fatalf("static fallback missing: %+v", route)
	}
	var failed int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM site_runtime_pilots WHERE state='failed'`).Scan(&failed); err != nil || failed != 2 {
		t.Fatalf("failed runtimes still admitted: %d %v", failed, err)
	}
}
