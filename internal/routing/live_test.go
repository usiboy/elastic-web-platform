package routing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"elastic-web-platform/internal/bundle"
)

func TestLiveGatewayOnlySwapsValidatedSnapshotsAndKeepsLastGood(t *testing.T) {
	db := routeDatabase(t)
	ctx := context.Background()
	root := t.TempDir()
	control := NewDurableRegistry(db, bundle.NewDiskStore(root))
	a, err := control.RegisterOperatorFixture(ctx, static(t, "t_alpha", "p_alpha", "one"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := control.RegisterOperatorFixture(ctx, static(t, "t_alpha", "p_alpha", "two"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.SwapAlias(ctx, "site.example.test", "t_alpha", "p_alpha", 0, a, false); err != nil {
		t.Fatal(err)
	}
	live := NewLiveGateway(control, nil)
	if err := live.Reload(ctx); err != nil || !live.Ready() {
		t.Fatalf("first route load: %v", err)
	}
	get := func() string {
		w := httptest.NewRecorder()
		live.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "https://site.example.test/", nil))
		if w.Code != 200 {
			t.Fatalf("site HTTP: %d", w.Code)
		}
		return w.Body.String()
	}
	if get() != "one" {
		t.Fatal("wrong first route")
	}
	if _, err := control.SwapAlias(ctx, "site.example.test", "t_alpha", "p_alpha", 1, b, false); err != nil {
		t.Fatal(err)
	}
	if get() != "one" {
		t.Fatal("gateway applied desired change without reload")
	}
	if err := live.Reload(ctx); err != nil || get() != "two" {
		t.Fatalf("new route load: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "t_alpha", "p_alpha", oneDigest(t, db, b)+".json"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := live.Reload(ctx); err == nil || live.Ready() {
		t.Fatalf("invalid route snapshot declared ready: %v", err)
	}
	if get() != "two" {
		t.Fatal("last healthy route lost after reload failure")
	}
}
