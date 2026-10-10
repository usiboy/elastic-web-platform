package pipeline

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"elastic-web-platform/db/migrations"
	"elastic-web-platform/internal/bundle"
	"elastic-web-platform/internal/routing"
	"elastic-web-platform/internal/source/github"
	"elastic-web-platform/internal/source/uploads"

	"github.com/jackc/pgx/v5/pgxpool"
)

func pipelineDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to an isolated PostgreSQL test instance")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := fmt.Sprintf("pipeline_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := migrations.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO teams(id,slug,name) VALUES('t_alpha','alpha','Alpha'); INSERT INTO projects(id,team_id,slug,name) VALUES('p_alpha','t_alpha','site','Site')`); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedPush(t *testing.T, db *pgxpool.Pool, delivery, ref string) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.Exec(ctx, `INSERT INTO github_bindings(team_id,project_id,installation_id,repository_id,full_name,default_branch,status) VALUES('t_alpha','p_alpha',42,1001,'org/site','main','verified') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("a", 40)
	if _, err := db.Exec(ctx, `INSERT INTO github_deliveries(delivery_id,payload_hash,installation_id,repository_id,ref,commit_sha,status) VALUES($1,$2,42,1001,$3,$4,'pending_commit_verification')`, delivery, []byte("hash"), ref, sha); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO github_pushes(delivery_id,team_id,project_id,commit_sha,ref,status) VALUES($1,'t_alpha','p_alpha',$2,$3,'pending_commit_verification')`, delivery, sha, ref); err != nil {
		t.Fatal(err)
	}
}

func fixtureArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var data bytes.Buffer
	gz := gzip.NewWriter(&data)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

type fakeFetcher struct {
	archive []byte
	corrupt bool
	head    string
}

func (f fakeFetcher) FetchPinned(_ context.Context, installationID, repositoryID int64, sha string) (github.PinnedSource, error) {
	if installationID != 42 || repositoryID != 1001 || sha != strings.Repeat("a", 40) {
		return github.PinnedSource{}, fmt.Errorf("unexpected Git scope")
	}
	h := sha256.Sum256(f.archive)
	digest := hex.EncodeToString(h[:])
	if f.corrupt {
		digest = strings.Repeat("0", 64)
	}
	return github.PinnedSource{Archive: f.archive, ArchiveSHA256: digest, CommitSHA: sha, TreeSHA: strings.Repeat("b", 40), Repository: github.Repository{ID: 1001, FullName: "org/site", DefaultBranch: "main"}}, nil
}

func (f fakeFetcher) CurrentBranchHead(_ context.Context, installationID, repositoryID int64, branch string) (string, error) {
	if installationID != 42 || repositoryID != 1001 || branch != "main" {
		return "", fmt.Errorf("unexpected branch scope")
	}
	if f.head != "" {
		return f.head, nil
	}
	return strings.Repeat("a", 40), nil
}

func TestWorkerTurnsVerifiedStaticGitPushIntoPrivatePreviewCandidate(t *testing.T) {
	db := pipelineDB(t)
	seedPush(t, db, "delivery-one", "refs/heads/feature")
	sources := uploads.NewDiskStore(t.TempDir())
	root := t.TempDir()
	artifacts := bundle.NewDiskStore(root)
	processor := NewProcessor(db, sources, artifacts, fakeFetcher{archive: fixtureArchive(t, map[string]string{"index.html": "<h1>Git site</h1>"})}, "preview.test")
	didWork, err := processor.ProcessOne(context.Background())
	if err != nil || !didWork {
		t.Fatalf("process: %v %v", didWork, err)
	}
	var status, sourceID, depID, host string
	err = db.QueryRow(context.Background(), `SELECT p.status,s.id,d.id,d.desired_host FROM github_pushes p
		JOIN github_verified_sources s ON s.delivery_id=p.delivery_id AND s.team_id=p.team_id AND s.project_id=p.project_id
		JOIN site_deployments d ON d.team_id=s.team_id AND d.project_id=s.project_id AND d.source_id=s.id
		WHERE p.delivery_id='delivery-one'`).Scan(&status, &sourceID, &depID, &host)
	if err != nil || status != "preview_candidate" || sourceID == "" || depID == "" || !strings.HasSuffix(host, ".preview.test") {
		t.Fatalf("candidate state: %s %s %s %s %v", status, sourceID, depID, host, err)
	}
	var private bool
	if err := db.QueryRow(context.Background(), `SELECT is_private FROM site_hosts WHERE hostname=$1`, host).Scan(&private); err != nil || !private {
		t.Fatalf("Git candidate was not private: %v %v", private, err)
	}
	if again, err := processor.ProcessOne(context.Background()); err != nil || again {
		t.Fatalf("duplicate worker attempt: %v %v", again, err)
	}
	var count int
	if err := db.QueryRow(context.Background(), `SELECT count(*) FROM site_deployments WHERE source_kind='github'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate deployments: %d %v", count, err)
	}
	var digest string
	if err := db.QueryRow(context.Background(), `SELECT bundle_digest FROM site_deployments WHERE id=$1`, depID).Scan(&digest); err != nil {
		t.Fatal(err)
	}
	if b, err := artifacts.Get(context.Background(), "t_alpha", "p_alpha", digest); err != nil || b.Verify() != nil {
		t.Fatalf("artifact cannot be read from %s: %v", filepath.Base(root), err)
	}
}

func TestWorkerKeepsViteSourceAwayFromUnisolatedBuilder(t *testing.T) {
	db := pipelineDB(t)
	seedPush(t, db, "delivery-vite", "refs/heads/main")
	data := fixtureArchive(t, map[string]string{
		"index.html":        "<div id='app'></div>",
		"package.json":      `{"scripts":{"build":"vite build"},"dependencies":{"vue":"3.5.0","vite":"6.0.0"}}`,
		"package-lock.json": `{"lockfileVersion":3,"packages":{}}`,
	})
	p := NewProcessor(db, uploads.NewDiskStore(t.TempDir()), bundle.NewDiskStore(t.TempDir()), fakeFetcher{archive: data}, "preview.test")
	if did, err := p.ProcessOne(context.Background()); err != nil || !did {
		t.Fatalf("Vite source ingestion: %v %v", did, err)
	}
	var status string
	if err := db.QueryRow(context.Background(), `SELECT status FROM github_pushes WHERE delivery_id='delivery-vite'`).Scan(&status); err != nil || status != "awaiting_isolated_build" {
		t.Fatalf("Vite entered static runner: %s %v", status, err)
	}
	var deployments int
	if err := db.QueryRow(context.Background(), `SELECT count(*) FROM site_deployments`).Scan(&deployments); err != nil || deployments != 0 {
		t.Fatalf("Vite source produced fake deployed artifact: %d %v", deployments, err)
	}
}

func TestWorkerFailureKeepsPendingEventForBoundedRetry(t *testing.T) {
	db := pipelineDB(t)
	seedPush(t, db, "delivery-failure", "refs/heads/main")
	p := NewProcessor(db, uploads.NewDiskStore(t.TempDir()), bundle.NewDiskStore(t.TempDir()), fakeFetcher{archive: fixtureArchive(t, map[string]string{"index.html": "<p>hello</p>"}), corrupt: true}, "preview.test")
	if did, err := p.ProcessOne(context.Background()); !did || err == nil {
		t.Fatalf("corrupt source marked successful: %v %v", did, err)
	}
	var status, code string
	var attempts int
	if err := db.QueryRow(context.Background(), `SELECT status,attempts,last_error_code FROM github_pushes WHERE delivery_id='delivery-failure'`).Scan(&status, &attempts, &code); err != nil || status != "pending_commit_verification" || attempts != 1 || code == "" {
		t.Fatalf("failure tracking: %s %d %s %v", status, attempts, code, err)
	}
	var count int
	if err := db.QueryRow(context.Background(), `SELECT count(*) FROM github_verified_sources`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("wrong source persisted: %d %v", count, err)
	}
}

func TestWorkerStopsAfterFiveFailedSourceAttempts(t *testing.T) {
	db := pipelineDB(t)
	seedPush(t, db, "delivery-bounded", "refs/heads/main")
	p := NewProcessor(db, uploads.NewDiskStore(t.TempDir()), bundle.NewDiskStore(t.TempDir()), fakeFetcher{archive: fixtureArchive(t, map[string]string{"index.html": "<p>hello</p>"}), corrupt: true}, "preview.test")
	for attempt := 1; attempt <= 5; attempt++ {
		if worked, err := p.ProcessOne(context.Background()); !worked || err == nil {
			t.Fatalf("attempt %d: worked=%v err=%v", attempt, worked, err)
		}
		if _, err := db.Exec(context.Background(), `UPDATE github_pushes SET next_attempt_at=now()-interval '1 second' WHERE delivery_id='delivery-bounded'`); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	var state string
	if err := db.QueryRow(context.Background(), `SELECT attempts,status FROM github_pushes WHERE delivery_id='delivery-bounded'`).Scan(&count, &state); err != nil || count != 5 || state != "failed" {
		t.Fatalf("unbounded retry: %d %s %v", count, state, err)
	}
	if worked, err := p.ProcessOne(context.Background()); worked || err != nil {
		t.Fatalf("failed job unexpectedly retried: %v %v", worked, err)
	}
}

func TestAutomaticProductionWaitsForPreviewAndCurrentGitBranch(t *testing.T) {
	db := pipelineDB(t)
	seedPush(t, db, "delivery-production", "refs/heads/main")
	artifacts := bundle.NewDiskStore(t.TempDir())
	p := NewProcessor(db, uploads.NewDiskStore(t.TempDir()), artifacts, fakeFetcher{archive: fixtureArchive(t, map[string]string{"index.html": "<h1>Production</h1>"})}, "preview.test", "sites.test")
	if did, err := p.ProcessOne(context.Background()); err != nil || !did {
		t.Fatalf("Git source: %v %v", did, err)
	}
	if did, err := p.PromoteOne(context.Background()); err != nil || did {
		t.Fatalf("unobserved Preview promoted: %v %v", did, err)
	}
	var depID, host, digest string
	if err := db.QueryRow(context.Background(), `SELECT id,desired_host,bundle_digest FROM site_deployments WHERE source_kind='github'`).Scan(&depID, &host, &digest); err != nil {
		t.Fatal(err)
	}
	if err := routing.NewDurableRegistry(db, artifacts).MarkServed(context.Background(), routing.Route{TeamID: "t_alpha", ProjectID: "p_alpha", DeploymentID: depID, Generation: 1, Private: true}, host, digest); err != nil {
		t.Fatal(err)
	}
	if did, err := p.PromoteOne(context.Background()); err != nil || !did {
		t.Fatalf("latest main push not promoted: %v %v", did, err)
	}
	var generation, observed int64
	if err := db.QueryRow(context.Background(), `SELECT generation,observed_generation FROM site_hosts WHERE hostname='p-alpha.sites.test'`).Scan(&generation, &observed); err != nil { // Actual project IDs use the p_alpha suffix.
		t.Fatal(err)
	}
	if generation != 1 || observed != 0 {
		t.Fatalf("automatic production was reported active without route readback: %d/%d", generation, observed)
	}
	var status string
	if err := db.QueryRow(context.Background(), `SELECT status FROM github_pushes WHERE delivery_id='delivery-production'`).Scan(&status); err != nil || status != "production_requested" {
		t.Fatalf("Git push state: %s %v", status, err)
	}
	if did, err := p.PromoteOne(context.Background()); err != nil || did {
		t.Fatalf("same commit promoted twice: %v %v", did, err)
	}
}

func TestAutomaticProductionSkipsCommitNoLongerAtBranchHead(t *testing.T) {
	db := pipelineDB(t)
	seedPush(t, db, "delivery-stale", "refs/heads/main")
	artifacts := bundle.NewDiskStore(t.TempDir())
	p := NewProcessor(db, uploads.NewDiskStore(t.TempDir()), artifacts, fakeFetcher{archive: fixtureArchive(t, map[string]string{"index.html": "<h1>Stale</h1>"}), head: strings.Repeat("c", 40)}, "preview.test", "sites.test")
	if did, err := p.ProcessOne(context.Background()); err != nil || !did {
		t.Fatalf("ingest stale commit: %v %v", did, err)
	}
	var depID, host, digest string
	if err := db.QueryRow(context.Background(), `SELECT id,desired_host,bundle_digest FROM site_deployments`).Scan(&depID, &host, &digest); err != nil {
		t.Fatal(err)
	}
	if err := routing.NewDurableRegistry(db, artifacts).MarkServed(context.Background(), routing.Route{TeamID: "t_alpha", ProjectID: "p_alpha", DeploymentID: depID, Generation: 1, Private: true}, host, digest); err != nil {
		t.Fatal(err)
	}
	if did, err := p.PromoteOne(context.Background()); err != nil || !did {
		t.Fatalf("stale push decision: %v %v", did, err)
	}
	var status string
	if err := db.QueryRow(context.Background(), `SELECT status FROM github_pushes WHERE delivery_id='delivery-stale'`).Scan(&status); err != nil || status != "superseded" {
		t.Fatalf("old commit auto-promoted: %s %v", status, err)
	}
	var count int
	if err := db.QueryRow(context.Background(), `SELECT count(*) FROM site_hosts WHERE NOT is_fixed`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("stale commit got production Host: %d %v", count, err)
	}
}

func TestActionsPilotPromotionIgnoresLegacySignedCandidate(t *testing.T) {
	db := pipelineDB(t)
	seedPush(t, db, "delivery-actions-origin", "refs/heads/main")
	artifacts := bundle.NewDiskStore(t.TempDir())
	p := NewProcessor(db, uploads.NewDiskStore(t.TempDir()), artifacts,
		fakeFetcher{archive: fixtureArchive(t, map[string]string{"index.html": "<h1>Safe</h1>"})}, "preview.test", "sites.test")
	if worked, err := p.ProcessOne(context.Background()); err != nil || !worked {
		t.Fatalf("fixture source: %t %v", worked, err)
	}
	var id, host, digest string
	if err := db.QueryRow(context.Background(), `SELECT id,desired_host,bundle_digest FROM site_deployments`).Scan(&id, &host, &digest); err != nil {
		t.Fatal(err)
	}
	if err := routing.NewDurableRegistry(db, artifacts).MarkServed(context.Background(), routing.Route{
		TeamID: "t_alpha", ProjectID: "p_alpha", DeploymentID: id, Generation: 1, Private: true,
	}, host, digest); err != nil {
		t.Fatal(err)
	}
	if worked, err := p.PromoteOneActionsPilot(context.Background()); err != nil || worked {
		t.Fatalf("old webhook source escaped Actions-only promotion: %t %v", worked, err)
	}
	if _, err := db.Exec(context.Background(), `UPDATE github_deliveries SET origin='actions_oidc' WHERE delivery_id='delivery-actions-origin'`); err != nil {
		t.Fatal(err)
	}
	if worked, err := p.PromoteOneActionsPilot(context.Background()); err != nil || !worked {
		t.Fatalf("observed Actions candidate not promoted: %t %v", worked, err)
	}
}

type unavailableHeadFetcher struct{ fakeFetcher }

func (unavailableHeadFetcher) CurrentBranchHead(context.Context, int64, int64, string) (string, error) {
	return "", fmt.Errorf("public GitHub API throttled")
}

func TestActionsPromotionDefersRateLimitedBranchCheckWithoutDroppingCandidate(t *testing.T) {
	db := pipelineDB(t)
	seedPush(t, db, "delivery-actions-throttled", "refs/heads/main")
	artifacts := bundle.NewDiskStore(t.TempDir())
	p := NewProcessor(db, uploads.NewDiskStore(t.TempDir()), artifacts,
		fakeFetcher{archive: fixtureArchive(t, map[string]string{"index.html": "<h1>Protected</h1>"})}, "preview.test", "sites.test")
	if done, err := p.ProcessOne(context.Background()); err != nil || !done {
		t.Fatalf("source: %t %v", done, err)
	}
	var id, host, digest string
	if err := db.QueryRow(context.Background(), `SELECT id,desired_host,bundle_digest FROM site_deployments`).Scan(&id, &host, &digest); err != nil {
		t.Fatal(err)
	}
	if err := routing.NewDurableRegistry(db, artifacts).MarkServed(context.Background(), routing.Route{
		TeamID: "t_alpha", ProjectID: "p_alpha", DeploymentID: id, Generation: 1, Private: true,
	}, host, digest); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(context.Background(), `UPDATE github_deliveries SET origin='actions_oidc' WHERE delivery_id='delivery-actions-throttled'`); err != nil {
		t.Fatal(err)
	}
	p.fetcher = unavailableHeadFetcher{}
	if done, err := p.PromoteOneActionsPilot(context.Background()); err != nil || !done {
		t.Fatalf("transient GitHub failure was not deferred: %t %v", done, err)
	}
	var status, code string
	var attempts, aliases int
	var retry time.Time
	if err := db.QueryRow(context.Background(), `SELECT status,last_error_code,attempts,next_attempt_at FROM github_pushes WHERE delivery_id='delivery-actions-throttled'`).Scan(&status, &code, &attempts, &retry); err != nil || status != "preview_candidate" || code != "branch_head_unavailable" || attempts != 0 || !retry.After(time.Now()) {
		t.Fatalf("OIDC candidate lost during public API throttle: %s %s %d %v", status, code, attempts, err)
	}
	if err := db.QueryRow(context.Background(), `SELECT count(*) FROM site_hosts WHERE NOT is_fixed`).Scan(&aliases); err != nil || aliases != 0 {
		t.Fatalf("unverified branch became production: %d %v", aliases, err)
	}
}
