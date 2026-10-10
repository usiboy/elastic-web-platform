package pipeline

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"elastic-web-platform/internal/bundle"
	"elastic-web-platform/internal/source/github"
	"elastic-web-platform/internal/source/uploads"
)

type pollFetcher struct{ fail bool }

func (p pollFetcher) CurrentBranchHead(_ context.Context, installationID, repositoryID int64, branch string) (string, error) {
	if p.fail {
		return "", errors.New("provider unavailable")
	}
	if installationID != 42 || branch != "main" {
		return "", errors.New("out of scope")
	}
	switch repositoryID {
	case 1001:
		return strings.Repeat("a", 40), nil
	case 1002:
		return strings.Repeat("b", 40), nil
	}
	return "", errors.New("out of scope")
}

func (p pollFetcher) FetchPinned(_ context.Context, _, _ int64, _ string) (github.PinnedSource, error) {
	return github.PinnedSource{}, errors.New("PollOne must not fetch or build arbitrary source")
}

func TestPollBoundRepositoriesWithoutPublicWebhookKeepsTeamScopes(t *testing.T) {
	db := pipelineDB(t)
	ctx := context.Background()
	if _, err := db.Exec(ctx, `INSERT INTO teams(id,slug,name) VALUES('t_beta','beta','Beta'); INSERT INTO projects(id,team_id,slug,name) VALUES('p_beta','t_beta','site','Site')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO github_bindings(team_id,project_id,installation_id,repository_id,full_name,default_branch,status)
		VALUES('t_alpha','p_alpha',42,1001,'org/testA','main','verified'),('t_beta','p_beta',42,1002,'org/testB','main','verified')`); err != nil {
		t.Fatal(err)
	}
	p := NewProcessor(db, uploads.NewDiskStore(t.TempDir()), bundle.NewDiskStore(t.TempDir()), pollFetcher{}, "preview.test")
	for i := 0; i < 2; i++ {
		if did, err := p.PollOne(ctx); !did || err != nil {
			t.Fatalf("poll %d: %v %v", i, did, err)
		}
	}
	if did, err := p.PollOne(ctx); did || err != nil {
		t.Fatalf("unchanged heads repeated: %v %v", did, err)
	}
	rows, err := db.Query(ctx, `SELECT p.team_id,p.project_id,d.repository_id,d.commit_sha,d.origin FROM github_pushes p JOIN github_deliveries d ON d.delivery_id=p.delivery_id ORDER BY p.team_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var count int
	for rows.Next() {
		var team, project, sha, origin string
		var repoID int64
		if err := rows.Scan(&team, &project, &repoID, &sha, &origin); err != nil {
			t.Fatal(err)
		}
		count++
		if origin != "app_poll" || (team == "t_alpha" && (project != "p_alpha" || repoID != 1001 || sha != strings.Repeat("a", 40))) || (team == "t_beta" && (project != "p_beta" || repoID != 1002 || sha != strings.Repeat("b", 40))) {
			t.Fatalf("cross Team or fake webhook origin: %s %s %d %s %s", team, project, repoID, sha, origin)
		}
	}
	if rows.Err() != nil || count != 2 {
		t.Fatalf("poll count %d: %v", count, rows.Err())
	}
	if _, err := db.Exec(ctx, `UPDATE github_bindings SET status='revoked' WHERE team_id='t_beta'`); err != nil {
		t.Fatal(err)
	}
	if did, err := p.PollOne(ctx); did || err != nil {
		t.Fatalf("revoked repo polled: %v %v", did, err)
	}
}

func TestPollProviderFailureDoesNotWriteInventedSource(t *testing.T) {
	db := pipelineDB(t)
	ctx := context.Background()
	if _, err := db.Exec(ctx, `INSERT INTO github_bindings(team_id,project_id,installation_id,repository_id,full_name,default_branch,status)
		VALUES('t_alpha','p_alpha',42,1001,'org/testA','main','verified')`); err != nil {
		t.Fatal(err)
	}
	p := NewProcessor(db, uploads.NewDiskStore(t.TempDir()), bundle.NewDiskStore(t.TempDir()), pollFetcher{fail: true}, "preview.test")
	if did, err := p.PollOne(ctx); did || err == nil {
		t.Fatalf("provider failed but event written: %v %v", did, err)
	}
	var count int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM github_deliveries`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("invented Git evidence: %d %v", count, err)
	}
}

func TestConcurrentPollOfSameHeadCreatesExactlyOneEvent(t *testing.T) {
	db := pipelineDB(t)
	ctx := context.Background()
	if _, err := db.Exec(ctx, `INSERT INTO github_bindings(team_id,project_id,installation_id,repository_id,full_name,default_branch,status)
		VALUES('t_alpha','p_alpha',42,1001,'org/testA','main','verified')`); err != nil {
		t.Fatal(err)
	}
	p := NewProcessor(db, uploads.NewDiskStore(t.TempDir()), bundle.NewDiskStore(t.TempDir()), pollFetcher{}, "preview.test")
	type result struct {
		did bool
		err error
	}
	answers := make(chan result, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); did, err := p.PollOne(ctx); answers <- result{did, err} }()
	}
	wg.Wait()
	close(answers)
	winners := 0
	for answer := range answers {
		if answer.err != nil {
			t.Fatal(answer.err)
		}
		if answer.did {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("same branch head was accepted %d times", winners)
	}
	var events int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM github_deliveries`).Scan(&events); err != nil || events != 1 {
		t.Fatalf("event count %d: %v", events, err)
	}
}
