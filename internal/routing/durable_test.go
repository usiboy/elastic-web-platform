package routing

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"elastic-web-platform/db/migrations"
	"elastic-web-platform/internal/bundle"

	"github.com/jackc/pgx/v5/pgxpool"
)

func routeDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to a dedicated PostgreSQL test instance")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := fmt.Sprintf("route_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := migrations.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO teams(id,slug,name) VALUES('t_alpha','alpha','Alpha'),('t_other','other','Other')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO projects(id,team_id,slug,name) VALUES('p_alpha','t_alpha','site','Site'),('p_other','t_other','site','Site')`); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestDurableRoutesRecoverFixedURLAndRollbackAcrossRegistries(t *testing.T) {
	db := routeDatabase(t)
	root := t.TempDir()
	store := bundle.NewDiskStore(root)
	ctx := context.Background()
	control := NewDurableRegistry(db, store)
	one, err := control.RegisterOperatorFixture(ctx, static(t, "t_alpha", "p_alpha", "one"))
	if err != nil {
		t.Fatal(err)
	}
	two, err := control.RegisterOperatorFixture(ctx, static(t, "t_alpha", "p_alpha", "two"))
	if err != nil {
		t.Fatal(err)
	}
	if err := control.BindFixed(ctx, "d-one.preview.test", one); err != nil {
		t.Fatal(err)
	}
	var desiredHost string
	if err := db.QueryRow(ctx, `SELECT desired_host FROM site_deployments WHERE id=$1`, one).Scan(&desiredHost); err != nil || desiredHost != "d-one.preview.test" {
		t.Fatalf("fixed Host missing from deployment metadata: %q %v", desiredHost, err)
	}
	if err := control.BindFixed(ctx, "another.preview.test", one); err == nil {
		t.Fatal("deployment acquired a second fixed Host")
	}
	var orphan int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM site_hosts WHERE hostname='another.preview.test'`).Scan(&orphan); err != nil || orphan != 0 {
		t.Fatalf("conflicting fixed Host was retained: count=%d err=%v", orphan, err)
	}
	if err := control.BindFixed(ctx, "d-one.preview.test", two); err == nil {
		t.Fatal("fixed URL changed after restart")
	}
	if gen, err := control.SwapAlias(ctx, "site.example.test", "t_alpha", "p_alpha", 0, one, false); err != nil || gen != 1 {
		t.Fatalf("production v1: %d %v", gen, err)
	}
	if _, err := control.SwapAlias(ctx, "site.example.test", "t_alpha", "p_alpha", 0, two, false); err == nil {
		t.Fatal("stale generation changed route")
	}
	if _, err := control.SwapAlias(ctx, "site.example.test", "t_other", "p_other", 1, two, false); err == nil {
		t.Fatal("other tenant took Host")
	}
	read := func(host string) string {
		t.Helper()
		loaded, err := NewDurableRegistry(db, bundle.NewDiskStore(root)).LoadSnapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		h := NewGateway(loaded, func(_ *http.Request, route Route) bool { return route.TeamID == "t_alpha" })
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "https://"+host+"/", nil))
		if w.Code != 200 {
			t.Fatalf("%s returned %d", host, w.Code)
		}
		return w.Body.String()
	}
	if got := read("site.example.test"); got != "one" {
		t.Fatal(got)
	}
	if gen, err := control.SwapAlias(ctx, "site.example.test", "t_alpha", "p_alpha", 1, two, false); err != nil || gen != 2 {
		t.Fatalf("promote v2: %d %v", gen, err)
	}
	if got := read("site.example.test"); got != "two" {
		t.Fatal(got)
	}
	if got := read("d-one.preview.test"); got != "one" {
		t.Fatal("fixed URL drifted")
	}
	if gen, err := control.SwapAlias(ctx, "site.example.test", "t_alpha", "p_alpha", 2, one, false); err != nil || gen != 3 {
		t.Fatalf("rollback: %d %v", gen, err)
	}
	if got := read("site.example.test"); got != "one" {
		t.Fatal(got)
	}
	var events int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM site_route_events WHERE hostname='site.example.test'`).Scan(&events); err != nil || events != 3 {
		t.Fatalf("release history: %d %v", events, err)
	}
	if err := os.WriteFile(filepath.Join(root, "t_alpha", "p_alpha", oneDigest(t, db, one)+".json"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := control.LoadSnapshot(ctx); err == nil {
		t.Fatal("damaged active/rollback artifact recovered as healthy")
	}
}

func oneDigest(t *testing.T, db *pgxpool.Pool, id string) string {
	t.Helper()
	var digest string
	if err := db.QueryRow(context.Background(), `SELECT bundle_digest FROM site_deployments WHERE id=$1`, id).Scan(&digest); err != nil {
		t.Fatal(err)
	}
	return digest
}

func TestDurableAliasConcurrentGenerationHasSingleWinner(t *testing.T) {
	db := routeDatabase(t)
	ctx := context.Background()
	control := NewDurableRegistry(db, bundle.NewDiskStore(t.TempDir()))
	one, err := control.RegisterOperatorFixture(ctx, static(t, "t_alpha", "p_alpha", "one"))
	if err != nil {
		t.Fatal(err)
	}
	two, err := control.RegisterOperatorFixture(ctx, static(t, "t_alpha", "p_alpha", "two"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.SwapAlias(ctx, "site.example.test", "t_alpha", "p_alpha", 0, one, false); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := control.SwapAlias(ctx, "site.example.test", "t_alpha", "p_alpha", 1, two, false)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("CAS promotion winners: %d", success)
	}
	var generation int64
	if err := db.QueryRow(ctx, `SELECT generation FROM site_hosts WHERE hostname='site.example.test'`).Scan(&generation); err != nil || generation != 2 {
		t.Fatalf("final generation: %d %v", generation, err)
	}
}

func TestMigrationBackfillsPriorOperatorFixtureHostWithoutTouchingOtherRoutes(t *testing.T) {
	db := routeDatabase(t)
	ctx := context.Background()
	registry := NewDurableRegistry(db, bundle.NewDiskStore(t.TempDir()))
	id, err := registry.RegisterOperatorFixture(ctx, static(t, "t_alpha", "p_alpha", "old fixture"))
	if err != nil {
		t.Fatal(err)
	}
	aliasID, err := registry.RegisterOperatorFixture(ctx, static(t, "t_alpha", "p_alpha", "alias fixture"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO site_hosts(hostname,team_id,project_id,deployment_id,generation,is_fixed,is_private) VALUES('old.preview.test','t_alpha','p_alpha',$1,1,true,true)`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.SwapAlias(ctx, "alias.sites.test", "t_alpha", "p_alpha", 0, aliasID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `DELETE FROM schema_migrations WHERE version='014_fixture_host_readback.sql'`); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	var host string
	if err := db.QueryRow(ctx, `SELECT desired_host FROM site_deployments WHERE id=$1`, id).Scan(&host); err != nil || host != "old.preview.test" {
		t.Fatalf("old fixed Host still missing: %q %v", host, err)
	}
	var aliasHost *string
	if err := db.QueryRow(ctx, `SELECT desired_host FROM site_deployments WHERE id=$1`, aliasID).Scan(&aliasHost); err != nil || aliasHost != nil {
		t.Fatalf("migration must not rewrite alias deployments: host=%v err=%v", aliasHost, err)
	}
}
