package pipeline

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"elastic-web-platform/internal/build"
	"elastic-web-platform/internal/bundle"
	"elastic-web-platform/internal/source/github"
	"elastic-web-platform/internal/source/uploads"
)

// Opt-in: actual pinned public Git bytes and previously verified gVisor A/B
// output enter the isolated Postgres handoff. The temporary signing key lives
// only in this test, not on a provisioned Builder; no ECS state is changed.
func TestLivePinnedABGVisorOutputsEnterPrivateCandidatesOnly(t *testing.T) {
	root := os.Getenv("EWP_LIVE_AB_GVISOR_OUTPUT_ROOT")
	if root == "" {
		t.Skip("set explicit protected gVisor A/B archive root and App identity")
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		t.Fatal("gVisor archive root must be a private directory")
	}
	readPrivate := func(name, expected string) []byte {
		t.Helper()
		path := filepath.Join(root, name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 16<<20 {
			t.Fatal("gVisor archive must be a bounded private file")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != expected {
			t.Fatal("fixed source or gVisor build archive changed")
		}
		return data
	}
	appID, err := strconv.ParseInt(os.Getenv("EWP_LIVE_GITHUB_APP_ID"), 10, 64)
	if err != nil || appID <= 0 {
		t.Fatal("real App ID required")
	}
	installationID, err := strconv.ParseInt(os.Getenv("EWP_LIVE_GITHUB_INSTALLATION_ID"), 10, 64)
	if err != nil || installationID <= 0 {
		t.Fatal("real installation ID required")
	}
	key, err := github.LoadPrivateKey(os.Getenv("EWP_LIVE_GITHUB_KEY_FILE"))
	if err != nil {
		t.Fatal("protected GitHub App identity unavailable")
	}
	db := pipelineDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 160*time.Second)
	defer cancel()
	if _, err := db.Exec(ctx, `INSERT INTO teams(id,slug,name) VALUES('t_beta','beta','Beta'); INSERT INTO projects(id,team_id,slug,name) VALUES('p_beta','t_beta','site','Site')`); err != nil {
		t.Fatal(err)
	}
	entries := []struct {
		team, project, repo, commit, source, output string
		sourceName, outputName, html, jsName, js    string
		repoID                                      int64
	}{
		{"t_alpha", "p_alpha", "usiboy/testA", "e6062c7469d13712515dbf19474c851c044b753f",
			"e705152a12b9cd06d3881c97240b53fb345267e4aa7ea9a0ed531a63a59a3b8d", "51fb8db4845b6a8f312ffba5de75e31a76f6540c64fab3b66009759c98c9ea55",
			"ewp-source-1082685543/repo-1404606559-e6062c7469d1.tar.gz", "testA-runsc-e6062c7.tar.gz", "e1026657cf7807381ea12f21325a8638decf0b1989eb42d4e549c3fb98a32ff6",
			"/assets/index-DXuZjoRV.js", "78d59d5fb71dc694944b2f47c49b2190cb4b2a5116827295ccce49093dd5272a", 1404606559},
		{"t_beta", "p_beta", "usiboy/testB", "cfaa2f283f5ced67b85899919fe10a4a9f99d014",
			"4ba10abcb742a445aea1d55b622fac1e4a0fcf4b7eee6928a3789fc819bde9c3", "19de65abbd55d12bf6eea3fdbe15baef5256aa9200b340021f89893aaa20f967",
			"ewp-source-1082685543/repo-1404606859-cfaa2f283f5c.tar.gz", "testB-runsc-cfaa2f2.tar.gz", "b0bf964b1808620fe848549c939140411499dc472e7540ebe5e0f4a278d0348a",
			"/assets/index-DRp-iAjd.js", "0d4f51df202ba1f460cfc674d5c3212ef7856cc9f1c7a3eb5f4e6c7a38f5f07b", 1404606859},
	}
	for _, entry := range entries {
		if _, err := db.Exec(ctx, `INSERT INTO github_bindings(team_id,project_id,installation_id,repository_id,full_name,default_branch,status)
			VALUES($1,$2,$3,$4,$5,'main','verified')`, entry.team, entry.project, installationID, entry.repoID, entry.repo); err != nil {
			t.Fatal(err)
		}
	}
	artifacts := bundle.NewDiskStore(t.TempDir())
	p := NewProcessor(db, uploads.NewDiskStore(t.TempDir()), artifacts, github.NewClient(appID, key, "https://api.github.com", nil), "preview.test")
	for i := 0; i < len(entries); i++ {
		if done, err := p.PollOne(ctx); err != nil || !done {
			t.Fatalf("Git branch poll %d failed: %v", i, err)
		}
		if done, err := p.ProcessOne(ctx); err != nil || !done {
			t.Fatalf("fixed Git source %d failed: %v", i, err)
		}
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		readPrivate(entry.sourceName, entry.source)
		output := readPrivate(entry.outputName, entry.output)
		var delivery, commit, sourceSHA, status string
		err := db.QueryRow(ctx, `SELECT p.delivery_id,p.commit_sha,s.archive_sha256,p.status FROM github_pushes p
			JOIN github_verified_sources s ON s.delivery_id=p.delivery_id AND s.team_id=p.team_id AND s.project_id=p.project_id
			WHERE p.team_id=$1 AND p.project_id=$2`, entry.team, entry.project).Scan(&delivery, &commit, &sourceSHA, &status)
		if err != nil || commit != entry.commit || sourceSHA != entry.source || status != "awaiting_isolated_build" {
			t.Fatalf("real Git source changed: team=%s err=%v", entry.team, err)
		}
		candidate, err := bundle.CaptureBuiltArchive(entry.team, entry.project, sourceSHA, "verified-test-output:runsc", output)
		if err != nil {
			t.Fatal(err)
		}
		for name, expected := range map[string]string{"/index.html": entry.html, entry.jsName: entry.js} {
			data, found := candidate.Read(name)
			digest := sha256.Sum256(data)
			if !found || hex.EncodeToString(digest[:]) != expected {
				t.Fatalf("actual %s CI asset did not match the gVisor output", entry.team)
			}
		}
		if _, err := db.Exec(ctx, `INSERT INTO team_resource_budgets(team_id,cpu_milli,memory_mib,storage_mib,version) VALUES($1,3000,4096,4096,1)`, entry.team); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `INSERT INTO project_resource_reservations(team_id,project_id,cpu_milli,memory_mib,storage_mib,version)
			VALUES($1,$2,2000,3072,2048,1)`, entry.team, entry.project); err != nil {
			t.Fatal(err)
		}
		plan := build.JobPlan{BuilderID: "colima-runsc-test-20261006", ImageDigest: "e4bf2a82ad0a4037d28035ae71529873c069b13eb0455466ae0bc13363826e34", CPUMilli: 2000, MemoryMiB: 3072, StorageMiB: 2048}
		job, err := p.IssueBuildJob(ctx, entry.team, entry.project, delivery, plan, 0)
		if err != nil {
			t.Fatal(err)
		}
		statement := build.BuildStatement{Version: 1, BuilderID: plan.BuilderID, TeamID: entry.team, ProjectID: entry.project, DeliveryID: delivery,
			JobID: job.ID, JobNonce: job.Nonce, CommitSHA: commit, SourceSHA256: sourceSHA, OutputSHA256: entry.output,
			ImageDigest: plan.ImageDigest, Runtime: "runsc", CPUMilli: plan.CPUMilli, MemoryMiB: plan.MemoryMiB, StorageMiB: plan.StorageMiB, FinishedAt: time.Now().UTC()}
		signed, err := build.SignBuild(private, statement)
		if err != nil {
			t.Fatal(err)
		}
		wrongOutput := entries[1].output
		if entry.team == "t_beta" {
			wrongOutput = entries[0].output
		}
		if _, err := p.AcceptSignedBuild(ctx, signed, readPrivate(map[string]string{"t_alpha": entries[1].outputName, "t_beta": entries[0].outputName}[entry.team], wrongOutput), public); err == nil {
			t.Fatal("another tenant's real build archive was accepted")
		}
		result, err := p.AcceptSignedBuild(ctx, signed, output, public)
		if err != nil || result.Status != "preview_candidate" || !strings.HasSuffix(result.Host, ".preview.test") {
			t.Fatalf("real %s gVisor output did not reach a private candidate: %+v %v", entry.team, result, err)
		}
		if repeat, err := p.AcceptSignedBuild(ctx, signed, output, public); err != nil || repeat.DeploymentID != result.DeploymentID {
			t.Fatalf("real %s output replay changed deployment: %+v %v", entry.team, repeat, err)
		}
		var privateHost bool
		if err := db.QueryRow(ctx, `SELECT is_private FROM site_hosts WHERE hostname=$1`, result.Host).Scan(&privateHost); err != nil || !privateHost {
			t.Fatalf("real %s output escaped private Preview: %v", entry.team, err)
		}
		t.Logf("team=%s commit=%s source_sha256=%s runsc_archive_sha256=%s candidate=%s private_host=%s", entry.team, commit, sourceSHA, entry.output, result.DeploymentID, result.Host)
	}
	var deployments, aliases int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM site_deployments WHERE source_kind='github'`).Scan(&deployments); err != nil || deployments != 2 {
		t.Fatalf("unexpected signed candidate count: %d %v", deployments, err)
	}
	if err := db.QueryRow(ctx, `SELECT count(*) FROM site_hosts WHERE NOT is_fixed`).Scan(&aliases); err != nil || aliases != 0 {
		t.Fatalf("unobserved signed output was promoted: %d %v", aliases, err)
	}
}
