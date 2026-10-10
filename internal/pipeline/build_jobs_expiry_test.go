package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"testing"
	"time"

	"elastic-web-platform/internal/build"
)

func TestExpiredBuildJobFailsOnlyItsGitPushAndKeepsPreviousProduction(t *testing.T) {
	p, claim, output, public, private := pendingViteBuild(t)
	ctx := context.Background()
	previous := "dep_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := p.db.Exec(ctx, `INSERT INTO site_deployments(id,team_id,project_id,bundle_digest,source_sha256)
		VALUES($1,$2,$3,$4,$5)`, previous, claim.TeamID, claim.ProjectID, strings.Repeat("b", 64), strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := p.db.Exec(ctx, `INSERT INTO site_hosts(hostname,team_id,project_id,deployment_id,generation,is_fixed,is_private)
		VALUES('old.sites.test',$1,$2,$3,4,false,false)`, claim.TeamID, claim.ProjectID, previous); err != nil {
		t.Fatal(err)
	}
	issued := issueFixtureJob(t, p, &claim)
	if worked, err := p.ExpireOneBuildJob(ctx); err != nil || worked {
		t.Fatalf("live build job expired prematurely: worked=%t err=%v", worked, err)
	}
	if _, err := p.db.Exec(ctx, `UPDATE github_build_jobs SET issued_at=now()-interval '20 minutes',expires_at=now()-interval '5 minutes'
		WHERE job_id=$1`, issued.ID); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	errors := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			worked, err := p.ExpireOneBuildJob(ctx)
			results <- worked
			errors <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	count := 0
	for worked := range results {
		if worked {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expired job must have exactly one durable transition, got %d", count)
	}
	var pushStatus, errorCode, jobStatus string
	if err := p.db.QueryRow(ctx, `SELECT p.status,p.last_error_code,j.status FROM github_pushes p JOIN github_build_jobs j
		ON j.team_id=p.team_id AND j.project_id=p.project_id AND j.delivery_id=p.delivery_id
		WHERE j.job_id=$1`, issued.ID).Scan(&pushStatus, &errorCode, &jobStatus); err != nil || pushStatus != "failed" || errorCode != "build_job_expired" || jobStatus != "expired" {
		t.Fatalf("expired job did not surface a stable failure: %s %s %s %v", pushStatus, errorCode, jobStatus, err)
	}
	if err := p.db.QueryRow(ctx, `SELECT deployment_id,generation FROM site_hosts WHERE hostname='old.sites.test'`).Scan(&pushStatus, &count); err != nil || pushStatus != previous || count != 4 {
		t.Fatalf("failed build changed existing production: deployment=%s generation=%d err=%v", pushStatus, count, err)
	}
	if err := p.db.QueryRow(ctx, `SELECT count(*) FROM site_deployments`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("expired job created a deployment: %d %v", count, err)
	}
	hash := sha256.Sum256(output)
	claim.OutputSHA256 = hex.EncodeToString(hash[:])
	claim.FinishedAt = time.Now().UTC()
	signed, err := build.SignBuild(private, claim)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.AcceptSignedBuild(ctx, signed, output, public); err == nil {
		t.Fatal("signed output from expired job accepted after failure")
	}
	if _, err := p.IssueBuildJob(ctx, claim.TeamID, claim.ProjectID, claim.DeliveryID,
		build.JobPlan{BuilderID: claim.BuilderID, ImageDigest: claim.ImageDigest, CPUMilli: claim.CPUMilli, MemoryMiB: claim.MemoryMiB, StorageMiB: claim.StorageMiB}, issued.Version); err == nil {
		t.Fatal("expired source silently reissued a job without a fresh delivery")
	}
}
