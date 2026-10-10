package pipeline

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"elastic-web-platform/internal/bundle"
	"elastic-web-platform/internal/source/github"
	"elastic-web-platform/internal/source/uploads"
)

// Opt-in real provider check, read-only and no public webhook. It keeps the
// App private key on the invoking Mac and does not build untrusted Vite code.
func TestLiveAppPollsTwoBoundRepositoriesWithoutBuildingVite(t *testing.T) {
	file := os.Getenv("EWP_LIVE_GITHUB_KEY_FILE")
	if file == "" {
		t.Skip("requires explicit read-only live App credential")
	}
	appID, err := strconv.ParseInt(os.Getenv("EWP_LIVE_GITHUB_APP_ID"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	installationID, err := strconv.ParseInt(os.Getenv("EWP_LIVE_GITHUB_INSTALLATION_ID"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	ids := strings.Split(os.Getenv("EWP_LIVE_GITHUB_REPOSITORY_IDS"), ",")
	if len(ids) != 2 {
		t.Fatal("requires exactly two explicitly authorized test repositories")
	}
	aID, err := strconv.ParseInt(strings.TrimSpace(ids[0]), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	bID, err := strconv.ParseInt(strings.TrimSpace(ids[1]), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	owner := os.Getenv("EWP_LIVE_GITHUB_OWNER")
	if owner == "" {
		t.Fatal("expected repository owner required")
	}
	key, err := github.LoadPrivateKey(file)
	if err != nil {
		t.Fatal("protected App key unavailable: ", err)
	}
	db := pipelineDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	if _, err := db.Exec(ctx, `INSERT INTO teams(id,slug,name) VALUES('t_beta','beta','Beta'); INSERT INTO projects(id,team_id,slug,name) VALUES('p_beta','t_beta','site','Site')`); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		team, project, repo string
		repoID              int64
	}{{"t_alpha", "p_alpha", owner + "/testA", aID}, {"t_beta", "p_beta", owner + "/testB", bID}} {
		if _, err := db.Exec(ctx, `INSERT INTO github_bindings(team_id,project_id,installation_id,repository_id,full_name,default_branch,status)
			VALUES($1,$2,$3,$4,$5,'main','verified')`, item.team, item.project, installationID, item.repoID, item.repo); err != nil {
			t.Fatal(err)
		}
	}
	p := NewProcessor(db, uploads.NewDiskStore(t.TempDir()), bundle.NewDiskStore(t.TempDir()), github.NewClient(appID, key, "https://api.github.com", nil), "preview.test")
	for i := 0; i < 2; i++ {
		if did, err := p.PollOne(ctx); !did || err != nil {
			t.Fatalf("repo poll %d: %v %v", i, did, err)
		}
	}
	for i := 0; i < 2; i++ {
		if did, err := p.ProcessOne(ctx); !did || err != nil {
			t.Fatalf("repo source %d: %v %v", i, did, err)
		}
	}
	rows, err := db.Query(ctx, `SELECT p.team_id,p.status,d.origin,p.commit_sha FROM github_pushes p JOIN github_deliveries d ON d.delivery_id=p.delivery_id ORDER BY p.team_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var count int
	for rows.Next() {
		var team, status, origin, sha string
		if err := rows.Scan(&team, &status, &origin, &sha); err != nil {
			t.Fatal(err)
		}
		if status != "awaiting_isolated_build" || origin != "app_poll" || len(sha) != 40 {
			t.Fatalf("repo %s incorrectly deployed: %s %s %s", team, status, origin, sha)
		}
		t.Logf("team=%s sourceCommit=%s status=%s origin=%s", team, sha, status, origin)
		count++
	}
	if rows.Err() != nil || count != 2 {
		t.Fatalf("Git polling scope count=%d: %v", count, rows.Err())
	}
	var deployments int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM site_deployments`).Scan(&deployments); err != nil || deployments != 0 {
		t.Fatalf("unisolated Vite site published: %d %v", deployments, err)
	}
}
