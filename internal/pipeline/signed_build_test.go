package pipeline

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"testing"
	"time"

	"elastic-web-platform/internal/build"
	"elastic-web-platform/internal/bundle"
	"elastic-web-platform/internal/source/uploads"
)

func pendingViteBuild(t *testing.T) (*Processor, build.BuildStatement, []byte, ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	db := pipelineDB(t)
	seedPush(t, db, "delivery-signed", "refs/heads/main")
	source := fixtureArchive(t, map[string]string{
		"index.html":        "<div id='app'></div>",
		"package.json":      `{"scripts":{"build":"vite build"},"dependencies":{"vue":"3.5.0"},"devDependencies":{"vite":"6.0.0"}}`,
		"package-lock.json": `{"lockfileVersion":3,"packages":{}}`,
	})
	processor := NewProcessor(db, uploads.NewDiskStore(t.TempDir()), bundle.NewDiskStore(t.TempDir()), fakeFetcher{archive: source}, "preview.test")
	if done, err := processor.ProcessOne(context.Background()); err != nil || !done {
		t.Fatalf("fixed Git source ingestion failed: %v %v", done, err)
	}
	output := fixtureArchive(t, map[string]string{"./index.html": "<script src='/assets/app.js'></script>", "./assets/app.js": "console.log('fixture')"})
	sourceHash, outputHash := sha256.Sum256(source), sha256.Sum256(output)
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	statement := build.BuildStatement{
		Version: 1, BuilderID: "builder-test-only", TeamID: "t_alpha", ProjectID: "p_alpha", DeliveryID: "delivery-signed",
		JobID: "job_11111111111111111111111111111111", JobNonce: strings.Repeat("2", 64),
		CommitSHA: strings.Repeat("a", 40), SourceSHA256: hex.EncodeToString(sourceHash[:]), OutputSHA256: hex.EncodeToString(outputHash[:]),
		ImageDigest: strings.Repeat("c", 64), Runtime: "runsc", CPUMilli: 1000, MemoryMiB: 2048, StorageMiB: 2048, FinishedAt: time.Now().UTC(),
	}
	return processor, statement, output, public, private
}

func issueFixtureJob(t *testing.T, processor *Processor, claim *build.BuildStatement) BuildJob {
	t.Helper()
	if _, err := processor.db.Exec(context.Background(), `INSERT INTO team_resource_budgets(team_id,cpu_milli,memory_mib,storage_mib,version) VALUES($1,1500,4096,4096,1)`, claim.TeamID); err != nil {
		t.Fatal(err)
	}
	if _, err := processor.db.Exec(context.Background(), `INSERT INTO project_resource_reservations(team_id,project_id,cpu_milli,memory_mib,storage_mib,version) VALUES($1,$2,1000,2048,2048,1)`, claim.TeamID, claim.ProjectID); err != nil {
		t.Fatal(err)
	}
	job, err := processor.IssueBuildJob(context.Background(), claim.TeamID, claim.ProjectID, claim.DeliveryID,
		build.JobPlan{BuilderID: claim.BuilderID, ImageDigest: claim.ImageDigest, CPUMilli: claim.CPUMilli, MemoryMiB: claim.MemoryMiB, StorageMiB: claim.StorageMiB}, 0)
	if err != nil {
		t.Fatal(err)
	}
	claim.JobID, claim.JobNonce = job.ID, job.Nonce
	claim.FinishedAt = time.Now().UTC()
	return job
}

func TestSignedBuildImportRequiresBoundVerifiedGitSourceAndReplaysImmutableBytes(t *testing.T) {
	p, claim, output, public, private := pendingViteBuild(t)
	signed, err := build.SignBuild(private, claim)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.AcceptSignedBuild(context.Background(), signed, output, public); err == nil {
		t.Fatal("Builder created a candidate without a control-issued job")
	}
	issued := issueFixtureJob(t, p, &claim)
	beforeIssue := claim
	beforeIssue.FinishedAt = issued.IssuedAt.Add(-time.Second)
	premature, err := build.SignBuild(private, beforeIssue)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.AcceptSignedBuild(context.Background(), premature, output, public); err == nil {
		t.Fatal("signed output dated before the control job was issued")
	}
	signed, err = build.SignBuild(private, claim)
	if err != nil {
		t.Fatal(err)
	}
	wrongPublic, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.AcceptSignedBuild(context.Background(), signed, output, wrongPublic); err == nil {
		t.Fatal("signature from a different trust anchor accepted")
	}
	if _, err := p.AcceptSignedBuild(context.Background(), signed, []byte("wrong output"), public); err == nil {
		t.Fatal("wrong signed output bytes reached candidate")
	}
	changed := claim
	changed.SourceSHA256 = strings.Repeat("d", 64)
	wrongSource, err := build.SignBuild(private, changed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.AcceptSignedBuild(context.Background(), wrongSource, output, public); err == nil {
		t.Fatal("builder-signed different source was accepted")
	}
	changed = claim
	changed.TeamID = "t_beta"
	crossTenant, err := build.SignBuild(private, changed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.AcceptSignedBuild(context.Background(), crossTenant, output, public); err == nil {
		t.Fatal("cross-Team signed statement was accepted")
	}
	changed = claim
	changed.ProjectID = "p_other"
	crossProject, err := build.SignBuild(private, changed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.AcceptSignedBuild(context.Background(), crossProject, output, public); err == nil {
		t.Fatal("cross-Project signed statement was accepted")
	}
	var initialStatus string
	if err := p.db.QueryRow(context.Background(), `SELECT status FROM github_pushes WHERE delivery_id=$1`, claim.DeliveryID).Scan(&initialStatus); err != nil || initialStatus != "awaiting_isolated_build" {
		t.Fatalf("failed signatures changed source status: %q %v", initialStatus, err)
	}
	result, err := p.AcceptSignedBuild(context.Background(), signed, output, public)
	if err != nil || result.DeploymentID == "" || result.Status != "preview_candidate" {
		t.Fatalf("verified output did not create private candidate: %+v %v", result, err)
	}
	if again, err := p.AcceptSignedBuild(context.Background(), signed, output, public); err != nil || again != result {
		t.Fatalf("identical signed replay created a different candidate: %+v %v", again, err)
	}
	changed = claim
	changed.BuilderID = "different-builder"
	alternate, err := build.SignBuild(private, changed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.AcceptSignedBuild(context.Background(), alternate, output, public); err == nil {
		t.Fatal("different statement replaced an immutable Git candidate")
	}
	var count int
	if err := p.db.QueryRow(context.Background(), `SELECT count(*) FROM site_deployments WHERE source_kind='github'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("signed replay duplicated deployment: %d %v", count, err)
	}
	if err := p.db.QueryRow(context.Background(), `SELECT count(*) FROM github_build_attestations`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("signed provenance missing or duplicated: %d %v", count, err)
	}
	var privateHost bool
	if err := p.db.QueryRow(context.Background(), `SELECT is_private FROM site_hosts WHERE hostname=$1`, result.Host).Scan(&privateHost); err != nil || !privateHost {
		t.Fatalf("signed output escaped protected Preview: %t %v", privateHost, err)
	}
	artifact, err := p.artifacts.Get(context.Background(), claim.TeamID, claim.ProjectID, result.BundleDigest)
	if err != nil || artifact.Verify() != nil || artifact.Manifest.SourceSHA256 != claim.SourceSHA256 {
		t.Fatalf("stored signed bundle failed readback: %v", err)
	}
}

func TestConcurrentSignedBuildReplayCreatesOnlyOneCandidate(t *testing.T) {
	p, claim, output, public, private := pendingViteBuild(t)
	issueFixtureJob(t, p, &claim)
	signed, err := build.SignBuild(private, claim)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan SignedBuildResult, 2)
	errors := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := p.AcceptSignedBuild(context.Background(), signed, output, public)
			results <- result
			errors <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("identical concurrent delivery failed: %v", err)
		}
	}
	var deploymentID string
	for result := range results {
		if deploymentID != "" && result.DeploymentID != deploymentID {
			t.Fatalf("concurrent identical build made different deployments: %s and %s", deploymentID, result.DeploymentID)
		}
		deploymentID = result.DeploymentID
	}
	var count int
	if err := p.db.QueryRow(context.Background(), `SELECT count(*) FROM site_deployments`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("concurrent signed output duplicated deployments: %d %v", count, err)
	}
}

func TestSignedBuildImportStopsOnRevokedBindingOrMalformedOutput(t *testing.T) {
	p, claim, output, public, private := pendingViteBuild(t)
	issueFixtureJob(t, p, &claim)
	signed, _ := build.SignBuild(private, claim)
	malformed := []byte("not a tar gzip archive")
	bad := claim
	sum := sha256.Sum256(malformed)
	bad.OutputSHA256 = hex.EncodeToString(sum[:])
	malformedSigned, err := build.SignBuild(private, bad)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.AcceptSignedBuild(context.Background(), malformedSigned, malformed, public); err == nil {
		t.Fatal("valid signature made unsafe build archive deployable")
	}
	if _, err := p.db.Exec(context.Background(), `UPDATE github_bindings SET status='revoked' WHERE team_id=$1 AND project_id=$2`, claim.TeamID, claim.ProjectID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.AcceptSignedBuild(context.Background(), signed, output, public); err == nil {
		t.Fatal("revoked Git binding accepted a signed build")
	}
	var count int
	if err := p.db.QueryRow(context.Background(), `SELECT count(*) FROM site_deployments`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("revoked/malformed build created deployment: %d %v", count, err)
	}
}

func TestBuildJobRequiresReservationCASAndLiveUnexpiredNonce(t *testing.T) {
	p, claim, output, public, private := pendingViteBuild(t)
	plan := build.JobPlan{BuilderID: claim.BuilderID, ImageDigest: claim.ImageDigest, CPUMilli: claim.CPUMilli, MemoryMiB: claim.MemoryMiB, StorageMiB: claim.StorageMiB}
	if _, err := p.IssueBuildJob(context.Background(), claim.TeamID, claim.ProjectID, claim.DeliveryID, plan, 0); err == nil {
		t.Fatal("build job issued without project resource reservation")
	}
	issueFixtureJob(t, p, &claim)
	oldSigned, err := build.SignBuild(private, claim)
	if err != nil {
		t.Fatal(err)
	}
	for _, tamper := range []func(*build.BuildStatement){
		func(c *build.BuildStatement) { c.JobNonce = strings.Repeat("9", 64) },
		func(c *build.BuildStatement) { c.ImageDigest = strings.Repeat("8", 64) },
	} {
		copy := claim
		tamper(&copy)
		forged, err := build.SignBuild(private, copy)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.AcceptSignedBuild(context.Background(), forged, output, public); err == nil {
			t.Fatal("builder-signed change to job nonce/image was accepted")
		}
	}
	if _, err := p.IssueBuildJob(context.Background(), claim.TeamID, claim.ProjectID, claim.DeliveryID, plan, 0); err == nil {
		t.Fatal("stale request rotated an outstanding job")
	}
	rotated, err := p.IssueBuildJob(context.Background(), claim.TeamID, claim.ProjectID, claim.DeliveryID, plan, 1)
	if err != nil || rotated.Version != 2 || rotated.ID == claim.JobID {
		t.Fatalf("explicit job rotation failed: %+v %v", rotated, err)
	}
	if _, err := p.AcceptSignedBuild(context.Background(), oldSigned, output, public); err == nil {
		t.Fatal("signature from a replaced job was accepted")
	}
	claim.JobID, claim.JobNonce, claim.FinishedAt = rotated.ID, rotated.Nonce, time.Now().UTC()
	signed, _ := build.SignBuild(private, claim)
	if _, err := p.db.Exec(context.Background(), `UPDATE github_build_jobs SET issued_at=now()-interval '1 hour', expires_at=now()-interval '1 minute' WHERE job_id=$1`, claim.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.AcceptSignedBuild(context.Background(), signed, output, public); err == nil {
		t.Fatal("expired build job was accepted")
	}
	fresh, err := p.IssueBuildJob(context.Background(), claim.TeamID, claim.ProjectID, claim.DeliveryID, plan, 2)
	if err != nil || fresh.Version != 3 || fresh.ID == rotated.ID {
		t.Fatalf("expired job could not be reissued: %+v %v", fresh, err)
	}
	claim.JobID, claim.JobNonce, claim.FinishedAt = fresh.ID, fresh.Nonce, time.Now().UTC()
	signed, _ = build.SignBuild(private, claim)
	if _, err := p.db.Exec(context.Background(), `UPDATE project_resource_reservations SET cpu_milli=0,memory_mib=0,storage_mib=0,version=version+1 WHERE team_id=$1 AND project_id=$2`, claim.TeamID, claim.ProjectID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.AcceptSignedBuild(context.Background(), signed, output, public); err == nil {
		t.Fatal("released resource reservation still admitted the build")
	}
	if _, err := p.db.Exec(context.Background(), `UPDATE project_resource_reservations SET cpu_milli=1000,memory_mib=2048,storage_mib=2048,version=version+1 WHERE team_id=$1 AND project_id=$2`, claim.TeamID, claim.ProjectID); err != nil {
		t.Fatal(err)
	}
	if result, err := p.AcceptSignedBuild(context.Background(), signed, output, public); err != nil || result.Status != "preview_candidate" {
		t.Fatalf("valid reissued job did not reach private candidate: %+v %v", result, err)
	}
	var status string
	if err := p.db.QueryRow(context.Background(), `SELECT status FROM github_build_jobs WHERE job_id=$1`, fresh.ID).Scan(&status); err != nil || status != "completed" {
		t.Fatalf("successful job nonce was not consumed: %s %v", status, err)
	}
	if _, err := p.IssueBuildJob(context.Background(), claim.TeamID, claim.ProjectID, claim.DeliveryID, plan, 3); err == nil {
		t.Fatal("completed build job was reissued")
	}
}

func TestConcurrentBuildJobReissueHonorsVersion(t *testing.T) {
	p, claim, _, _, _ := pendingViteBuild(t)
	issueFixtureJob(t, p, &claim)
	plan := build.JobPlan{BuilderID: claim.BuilderID, ImageDigest: claim.ImageDigest, CPUMilli: claim.CPUMilli, MemoryMiB: claim.MemoryMiB, StorageMiB: claim.StorageMiB}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := p.IssueBuildJob(context.Background(), claim.TeamID, claim.ProjectID, claim.DeliveryID, plan, 1)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else {
			conflict++
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("concurrent CAS reissued more than one job: success=%d conflict=%d", success, conflict)
	}
	var version int64
	if err := p.db.QueryRow(context.Background(), `SELECT version FROM github_build_jobs WHERE team_id=$1 AND project_id=$2 AND delivery_id=$3`, claim.TeamID, claim.ProjectID, claim.DeliveryID).Scan(&version); err != nil || version != 2 {
		t.Fatalf("concurrent job version drift: %d %v", version, err)
	}
}
