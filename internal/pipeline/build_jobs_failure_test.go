package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"elastic-web-platform/internal/build"
)

func TestFailedIsolatedBuildKeepsOldAliasAndRejectsLateSignedOutput(t *testing.T) {
	p, claim, output, public, private := pendingViteBuild(t)
	ctx := context.Background()
	old := "dep_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err := p.db.Exec(ctx, `INSERT INTO site_deployments(id,team_id,project_id,bundle_digest,source_sha256)
		VALUES($1,$2,$3,$4,$5)`, old, claim.TeamID, claim.ProjectID, strings.Repeat("b", 64), strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := p.db.Exec(ctx, `INSERT INTO site_hosts(hostname,team_id,project_id,deployment_id,generation,is_fixed,is_private)
		VALUES('production.sites.test',$1,$2,$3,7,false,false)`, claim.TeamID, claim.ProjectID, old); err != nil {
		t.Fatal(err)
	}
	job := issueFixtureJob(t, p, &claim)
	if _, err := p.FailBuildJob(ctx, claim.TeamID, claim.ProjectID, claim.DeliveryID, job.ID, strings.Repeat("f", 64), "build_failed"); err == nil {
		t.Fatal("wrong job nonce marked the build failed")
	}
	if _, err := p.FailBuildJob(ctx, claim.TeamID, claim.ProjectID, claim.DeliveryID, job.ID, job.Nonce, "secret-bearing-detail"); err == nil {
		t.Fatal("arbitrary Builder error persisted to UI")
	}
	changed, err := p.FailBuildJob(ctx, claim.TeamID, claim.ProjectID, claim.DeliveryID, job.ID, job.Nonce, "build_failed")
	if err != nil || !changed {
		t.Fatalf("actual isolated build failure missing: %t %v", changed, err)
	}
	changed, err = p.FailBuildJob(ctx, claim.TeamID, claim.ProjectID, claim.DeliveryID, job.ID, job.Nonce, "build_failed")
	if err != nil || changed {
		t.Fatalf("failure retried with same nonce: %t %v", changed, err)
	}
	var pushStatus, code, jobStatus, deployment string
	var generation int
	if err := p.db.QueryRow(ctx, `SELECT p.status,p.last_error_code,j.status FROM github_pushes p JOIN github_build_jobs j
		ON j.team_id=p.team_id AND j.project_id=p.project_id AND j.delivery_id=p.delivery_id WHERE j.job_id=$1`, job.ID).Scan(&pushStatus, &code, &jobStatus); err != nil || pushStatus != "failed" || code != "build_failed" || jobStatus != "failed" {
		t.Fatalf("build failure status mismatch: %s %s %s %v", pushStatus, code, jobStatus, err)
	}
	if err := p.db.QueryRow(ctx, `SELECT deployment_id,generation FROM site_hosts WHERE hostname='production.sites.test'`).Scan(&deployment, &generation); err != nil || deployment != old || generation != 7 {
		t.Fatalf("old production changed after failed build: %s %d %v", deployment, generation, err)
	}
	hash := sha256.Sum256(output)
	claim.OutputSHA256, claim.FinishedAt = hex.EncodeToString(hash[:]), time.Now().UTC()
	signed, err := build.SignBuild(private, claim)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.AcceptSignedBuild(ctx, signed, output, public); err == nil {
		t.Fatal("late signed output from a failed job was accepted")
	}
}
