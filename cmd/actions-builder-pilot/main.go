// actions-builder-pilot only handles the two approved GitHub Actions OIDC
// public-source trials. The per-job signature is not a separate trust domain:
// the host operator and the isolated builder still share one ECS.
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"elastic-web-platform/db/migrations"
	"elastic-web-platform/internal/build"
	"elastic-web-platform/internal/bundle"
	"elastic-web-platform/internal/pipeline"
	"elastic-web-platform/internal/source/github"
	"elastic-web-platform/internal/source/uploads"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const root = "/data/ewp-builder-20261007"
const script = root + "/state/build_actions_oidc_pilot.sh"
const builderID = "ecs-samehost-netns-20261007"
const imageDigest = "e894118949319b479bfc3e16e545cb2f85b7a2194aa86ea9cd4e29538f2b13c3"

type candidate struct {
	delivery, team, project, commit, sourceSHA, name string
	repositoryID                                     int64
	plan                                             build.JobPlan
}

type publicFetcher struct{ client *github.Client }

func (f publicFetcher) FetchPinned(ctx context.Context, _ int64, repoID int64, commit string) (github.PinnedSource, error) {
	return f.client.FetchPublicPilotPinned(ctx, repoID, commit)
}

func (f publicFetcher) CurrentBranchHead(ctx context.Context, _ int64, repoID int64, branch string) (string, error) {
	return f.client.PublicPilotBranchHead(ctx, repoID, branch)
}

func pending(ctx context.Context, db *pgxpool.Pool) (candidate, error) {
	var item candidate
	err := db.QueryRow(ctx, `SELECT p.delivery_id,p.team_id,p.project_id,p.commit_sha,s.archive_sha256,d.repository_id
		FROM github_pushes p JOIN github_deliveries d ON d.delivery_id=p.delivery_id AND d.origin='actions_oidc'
		JOIN github_verified_sources s ON s.delivery_id=p.delivery_id AND s.team_id=p.team_id AND s.project_id=p.project_id
		JOIN github_bindings b ON b.team_id=p.team_id AND b.project_id=p.project_id AND b.repository_id=d.repository_id AND b.status='verified'
		LEFT JOIN github_build_jobs j ON j.delivery_id=p.delivery_id AND j.team_id=p.team_id AND j.project_id=p.project_id
		WHERE p.status='awaiting_isolated_build' AND j.job_id IS NULL ORDER BY d.received_at LIMIT 1`).Scan(
		&item.delivery, &item.team, &item.project, &item.commit, &item.sourceSHA, &item.repositoryID)
	if err != nil {
		return candidate{}, err
	}
	item.plan = build.JobPlan{BuilderID: builderID, ImageDigest: imageDigest, StorageMiB: 2048}
	switch {
	case item.team == "t_f9cf963718a72612c48940af8a8296e4" && item.project == "p_34401f5c05af1079010cb95ee3ec1275" && item.repositoryID == 1404606559:
		item.name, item.plan.CPUMilli, item.plan.MemoryMiB = "testA", 1000, 2048
	case item.team == "t_eee8046ca49c271f5bea06acf285be66" && item.project == "p_e5123bae12388454bb82fc1590750ab9" && item.repositoryID == 1404606859:
		item.name, item.plan.CPUMilli, item.plan.MemoryMiB = "testB", 2000, 3072
	default:
		return candidate{}, errors.New("OIDC source is outside the approved A/B pilot")
	}
	return item, nil
}

func newerTrustedOIDC(ctx context.Context, db *pgxpool.Pool, item candidate) (bool, error) {
	var newer bool
	err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM github_pushes p
		JOIN github_deliveries n ON n.delivery_id=p.delivery_id AND n.origin='actions_oidc'
		JOIN github_deliveries old ON old.delivery_id=$1
		WHERE p.team_id=$2 AND p.project_id=$3 AND p.ref=old.ref
		AND (n.received_at,n.delivery_id)>(old.received_at,old.delivery_id))`, item.delivery, item.team, item.project).Scan(&newer)
	return newer, err
}

func run(ctx context.Context, db *pgxpool.Pool, sources uploads.Store, artifacts bundle.Store, git *github.Client, suffix string) error {
	item, err := pending(ctx, db)
	if errors.Is(err, pgx.ErrNoRows) {
		processor := pipeline.NewProcessor(db, sources, artifacts, publicFetcher{git}, suffix, "sites.ecp.lingtong.com")
		if expired, expiryErr := processor.ExpireOneBuildJob(ctx); expiryErr != nil || expired {
			return expiryErr
		}
		_, promoteErr := processor.PromoteOneActionsPilot(ctx)
		return promoteErr
	}
	if err != nil {
		return err
	}
	// Boot-time Docker readiness and the bounded tmpfs mount must be real
	// before issuing a one-use nonce. A failed preflight is retried by the
	// timer, instead of consuming the Git commit as a failed build.
	// PrivateTmp/ProtectHome give this service its own mount namespace.
	// Prepare the 64 MiB output tmpfs in PID 1's mount namespace so the
	// separate Builder daemon sees the *same* hard-limited volume.
	preflight := exec.CommandContext(ctx, "/usr/bin/nsenter", "-t", "1", "-m", "/bin/bash", root+"/state/prepare_bounded_builder_output.sh", item.name)
	preflight.Stdout, preflight.Stderr = io.Discard, io.Discard
	if err := preflight.Run(); err != nil {
		log.Printf("isolated OIDC builder preflight deferred: project=%s", item.name)
		return nil
	}
	processor := pipeline.NewProcessor(db, sources, artifacts, nil, suffix)
	job, err := processor.IssueBuildJob(ctx, item.team, item.project, item.delivery, item.plan, 0)
	if err != nil {
		return err
	}
	fail := func(code string) error {
		failureCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		failed, err := processor.FailBuildJob(failureCtx, item.team, item.project, item.delivery, job.ID, job.Nonce, code)
		if err != nil {
			return err
		}
		if !failed {
			return errors.New("build failure transition did not commit")
		}
		log.Printf("isolated OIDC build failed: project=%s code=%s", item.name, code)
		return nil
	}
	if newer, err := newerTrustedOIDC(ctx, db, item); err != nil {
		return err
	} else if newer {
		return fail("build_superseded")
	}
	if source, readErr := sources.Get(ctx, item.team, item.project, item.sourceSHA); readErr != nil {
		return fail("builder_unavailable")
	} else if archive, validateErr := uploads.Validate(source); validateErr != nil || archive.Digest != item.sourceSHA {
		return fail("build_output_invalid")
	}
	output := filepath.Join(root, "state", "oidc-"+item.name+"-"+strings.TrimPrefix(job.ID, "job_")+".tar.gz")
	defer os.Remove(output)
	buildCtx, cancel := context.WithTimeout(ctx, 115*time.Second)
	defer cancel()
	command := exec.CommandContext(buildCtx, "/usr/bin/nsenter", "-t", "1", "-m", "/bin/bash", script, item.name, item.sourceSHA, output)
	command.Stdout, command.Stderr = io.Discard, io.Discard // raw tenant build output never reaches host logs
	if err := command.Run(); err != nil {
		if errors.Is(buildCtx.Err(), context.DeadlineExceeded) {
			return fail("build_timeout")
		}
		return fail("build_failed")
	}
	stat, err := os.Lstat(output)
	if err != nil || !stat.Mode().IsRegular() || stat.Size() == 0 || stat.Size() > 16<<20 {
		return fail("build_output_invalid")
	}
	archive, err := os.ReadFile(output)
	if err != nil {
		return fail("build_output_invalid")
	}
	if _, err := bundle.CaptureBuiltArchive(item.team, item.project, item.sourceSHA, "signed-gvisor:"+builderID+":"+imageDigest, archive); err != nil {
		return fail("build_output_invalid")
	}
	newer, err := newerTrustedOIDC(ctx, db, item)
	if err != nil {
		return fail("builder_unavailable")
	}
	if newer {
		return fail("build_superseded")
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fail("builder_unavailable")
	}
	digest := sha256.Sum256(archive)
	statement := build.BuildStatement{Version: 1, BuilderID: builderID, TeamID: item.team, ProjectID: item.project,
		DeliveryID: item.delivery, JobID: job.ID, JobNonce: job.Nonce, CommitSHA: item.commit, SourceSHA256: item.sourceSHA,
		OutputSHA256: hex.EncodeToString(digest[:]), ImageDigest: imageDigest, Runtime: "runsc", CPUMilli: item.plan.CPUMilli,
		MemoryMiB: item.plan.MemoryMiB, StorageMiB: item.plan.StorageMiB, FinishedAt: time.Now().UTC()}
	signed, err := build.SignBuild(private, statement)
	if err != nil {
		return fail("builder_unavailable")
	}
	result, err := processor.AcceptSignedBuild(ctx, signed, archive, public)
	if err != nil {
		return err
	}
	fmt.Printf("oidc_private_candidate=%s project=%s host=%s\n", result.DeploymentID, item.name, result.Host)
	return nil
}

func main() {
	dbURL, sourceRoot, artifactRoot, suffix := os.Getenv("DATABASE_URL"), os.Getenv("SOURCE_STORE_DIR"), os.Getenv("ARTIFACT_STORE_DIR"), os.Getenv("SITE_PREVIEW_SUFFIX")
	if dbURL == "" || sourceRoot != "/data/ewp-preview-20261006/sources" || artifactRoot != "/data/ewp-preview-20261006/artifacts" || suffix != "preview.ecp.lingtong.com" {
		log.Fatal("fixed OIDC pilot stores and protected database are required")
	}
	ctx, done := context.WithTimeout(context.Background(), 160*time.Second)
	defer done()
	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatal("pilot database unavailable")
	}
	defer db.Close()
	if err := migrations.Apply(ctx, db); err != nil {
		log.Fatal("pilot migration unavailable")
	}
	if err := run(ctx, db, uploads.NewDiskStore(sourceRoot), bundle.NewDiskStore(artifactRoot), github.NewClient(0, nil, "https://api.github.com", nil), suffix); err != nil {
		log.Fatal("OIDC builder pilot deferred: ", err)
	}
}
