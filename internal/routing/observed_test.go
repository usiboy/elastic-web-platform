package routing

import (
	"context"
	"testing"

	"elastic-web-platform/internal/bundle"
)

func TestObservedRouteGenerationRequiresMatchingServedVersion(t *testing.T) {
	db := routeDatabase(t)
	ctx := context.Background()
	r := NewDurableRegistry(db, bundle.NewDiskStore(t.TempDir()))
	first := static(t, "t_alpha", "p_alpha", "first")
	second := static(t, "t_alpha", "p_alpha", "second")
	a, err := r.RegisterOperatorFixture(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.RegisterOperatorFixture(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.BindFixed(ctx, "a.preview.test", a); err != nil {
		t.Fatal(err)
	}
	if err := r.BindFixed(ctx, "b.preview.test", b); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := db.QueryRow(ctx, `SELECT observed_state FROM site_deployments WHERE id=$1`, a).Scan(&state); err != nil || state != "not_deployed" {
		t.Fatalf("premature preview: %s %v", state, err)
	}
	previewA := Route{TeamID: "t_alpha", ProjectID: "p_alpha", DeploymentID: a, Generation: 1, Private: true}
	if err := r.MarkServed(ctx, previewA, "wrong.preview.test", first.Manifest.Digest); err == nil {
		t.Fatal("wrong Host marked preview ready")
	}
	if err := r.MarkServed(ctx, previewA, "a.preview.test", second.Manifest.Digest); err == nil {
		t.Fatal("wrong digest marked preview ready")
	}
	if err := r.MarkServed(ctx, previewA, "a.preview.test", first.Manifest.Digest); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `SELECT observed_state FROM site_deployments WHERE id=$1`, a).Scan(&state); err != nil || state != "preview_ready" {
		t.Fatalf("real preview not observed: %s %v", state, err)
	}
	if _, err := r.SwapAlias(ctx, "site.example.test", "t_alpha", "p_alpha", 0, a, false); err != nil {
		t.Fatal(err)
	}
	var observed int64
	if err := db.QueryRow(ctx, `SELECT observed_generation FROM site_hosts WHERE hostname='site.example.test'`).Scan(&observed); err != nil || observed != 0 {
		t.Fatalf("unprobed production active: %d %v", observed, err)
	}
	prodA := Route{TeamID: "t_alpha", ProjectID: "p_alpha", DeploymentID: a, Generation: 1, Private: false}
	if err := r.MarkServed(ctx, prodA, "site.example.test", first.Manifest.Digest); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SwapAlias(ctx, "site.example.test", "t_alpha", "p_alpha", 1, b, false); err != nil {
		t.Fatal(err)
	}
	if err := r.MarkServed(ctx, prodA, "site.example.test", first.Manifest.Digest); err == nil {
		t.Fatal("stale gateway ACK accepted after newer desired generation")
	}
	if err := db.QueryRow(ctx, `SELECT observed_generation FROM site_hosts WHERE hostname='site.example.test'`).Scan(&observed); err != nil || observed != 1 {
		t.Fatalf("stale route advanced observation: %d %v", observed, err)
	}
	previewB := Route{TeamID: "t_alpha", ProjectID: "p_alpha", DeploymentID: b, Generation: 1, Private: true}
	if err := r.MarkServed(ctx, previewB, "b.preview.test", second.Manifest.Digest); err != nil {
		t.Fatal(err)
	}
	if err := r.MarkServed(ctx, Route{TeamID: "t_alpha", ProjectID: "p_alpha", DeploymentID: b, Generation: 2, Private: false}, "site.example.test", second.Manifest.Digest); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `SELECT observed_generation FROM site_hosts WHERE hostname='site.example.test'`).Scan(&observed); err != nil || observed != 2 {
		t.Fatalf("v2 not observed: %d %v", observed, err)
	}
}
