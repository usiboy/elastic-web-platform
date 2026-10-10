package pipeline

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"

	"elastic-web-platform/internal/bundle"
	"elastic-web-platform/internal/framework"
	"elastic-web-platform/internal/source/github"
	"elastic-web-platform/internal/source/uploads"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Fetcher interface {
	FetchPinned(context.Context, int64, int64, string) (github.PinnedSource, error)
	CurrentBranchHead(context.Context, int64, int64, string) (string, error)
}

type Processor struct {
	db               *pgxpool.Pool
	sources          uploads.Store
	artifacts        bundle.Store
	fetcher          Fetcher
	previewSuffix    string
	productionSuffix string
}

var suffixPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*[a-z0-9]$`)

func NewProcessor(db *pgxpool.Pool, sources uploads.Store, artifacts bundle.Store, fetcher Fetcher, previewSuffix string, productionSuffix ...string) *Processor {
	p := &Processor{db: db, sources: sources, artifacts: artifacts, fetcher: fetcher, previewSuffix: previewSuffix}
	if len(productionSuffix) > 0 {
		p.productionSuffix = productionSuffix[0]
	}
	return p
}

type event struct {
	delivery, team, project, sha, fullName string
	installationID, repositoryID           int64
}

// ProcessOne claims one durable, signed push. A transaction holds the claim
// across scoped fetch and immutable writes; failure rolls back its DB side
// effects and records a bounded retry rather than inventing a deployment.
func (p *Processor) ProcessOne(ctx context.Context) (bool, error) {
	if p.db == nil || p.sources == nil || p.artifacts == nil || p.fetcher == nil || len(p.previewSuffix) > 180 || !suffixPattern.MatchString(p.previewSuffix) || strings.Contains(p.previewSuffix, "..") {
		return false, errors.New("Git processor is not configured")
	}
	tx, err := p.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var item event
	err = tx.QueryRow(ctx, `SELECT p.delivery_id,p.team_id,p.project_id,p.commit_sha,d.installation_id,d.repository_id,b.full_name
		FROM github_pushes p JOIN github_deliveries d ON d.delivery_id=p.delivery_id
		JOIN github_bindings b ON b.team_id=p.team_id AND b.project_id=p.project_id AND b.installation_id=d.installation_id AND b.repository_id=d.repository_id AND b.status='verified'
		WHERE p.status='pending_commit_verification' AND p.next_attempt_at<=now()
		ORDER BY d.received_at,p.team_id,p.project_id LIMIT 1 FOR UPDATE OF p SKIP LOCKED`).Scan(&item.delivery, &item.team, &item.project, &item.sha, &item.installationID, &item.repositoryID, &item.fullName)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `SAVEPOINT source_attempt`); err != nil {
		return true, err
	}
	if err = p.ingest(ctx, tx, item); err != nil {
		if _, rollbackErr := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT source_attempt`); rollbackErr != nil {
			return true, rollbackErr
		}
		_, updateErr := tx.Exec(ctx, `UPDATE github_pushes SET attempts=attempts+1,
			status=CASE WHEN attempts+1>=5 THEN 'failed' ELSE 'pending_commit_verification' END,
			next_attempt_at=now()+interval '5 seconds',last_error_code='source_processing_failed'
			WHERE delivery_id=$1 AND team_id=$2 AND project_id=$3`, item.delivery, item.team, item.project)
		if updateErr != nil {
			return true, updateErr
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return true, commitErr
		}
		return true, errors.New("source processing failed; retry is recorded")
	}
	return true, tx.Commit(ctx)
}

func (p *Processor) ingest(ctx context.Context, tx pgx.Tx, item event) error {
	pinned, err := p.fetcher.FetchPinned(ctx, item.installationID, item.repositoryID, item.sha)
	if err != nil {
		return err
	}
	manifest, err := uploads.Validate(pinned.Archive)
	if err != nil || pinned.CommitSHA != item.sha || pinned.Repository.ID != item.repositoryID || pinned.Repository.FullName != item.fullName || pinned.ArchiveSHA256 != manifest.Digest {
		return errors.New("fixed Git bytes failed verification")
	}
	if err = p.sources.Put(ctx, item.team, item.project, manifest.Digest, pinned.Archive); err != nil {
		return err
	}
	var sourceID, existingHash string
	err = tx.QueryRow(ctx, `SELECT id,archive_sha256 FROM github_verified_sources WHERE team_id=$1 AND project_id=$2 AND delivery_id=$3`, item.team, item.project, item.delivery).Scan(&sourceID, &existingHash)
	if err == nil {
		if existingHash != manifest.Digest {
			return errors.New("existing source digest changed")
		}
	} else if errors.Is(err, pgx.ErrNoRows) {
		sourceID, err = newID("gsrc_")
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO github_verified_sources(id,team_id,project_id,delivery_id,commit_sha,tree_sha,archive_sha256,size_bytes)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, sourceID, item.team, item.project, item.delivery, pinned.CommitSHA, pinned.TreeSHA, manifest.Digest, manifest.Size)
		if err != nil {
			return err
		}
	} else {
		return err
	}
	config, err := framework.Detect(pinned.Archive)
	if err != nil {
		return err
	}
	if config.RequiresBuild {
		_, err = tx.Exec(ctx, `UPDATE github_pushes SET status='awaiting_isolated_build',last_error_code='' WHERE delivery_id=$1 AND team_id=$2 AND project_id=$3`, item.delivery, item.team, item.project)
		return err
	}
	output, err := bundle.CaptureStaticSource(item.team, item.project, pinned.Archive)
	if err != nil || output.Manifest.SourceSHA256 != manifest.Digest {
		return errors.New("static source capture failed")
	}
	if err = p.artifacts.Put(ctx, output); err != nil {
		return err
	}
	deploymentID, err := newID("dep_")
	if err != nil {
		return err
	}
	host := "d-" + strings.TrimPrefix(deploymentID, "dep_") + "." + p.previewSuffix
	_, err = tx.Exec(ctx, `INSERT INTO site_deployments(id,team_id,project_id,bundle_digest,source_sha256,source_kind,source_id,desired_host)
		VALUES($1,$2,$3,$4,$5,'github',$6,$7)`, deploymentID, item.team, item.project, output.Manifest.Digest, manifest.Digest, sourceID, host)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO site_hosts(hostname,team_id,project_id,deployment_id,generation,is_fixed,is_private)
		VALUES($1,$2,$3,$4,1,true,true)`, host, item.team, item.project, deploymentID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO site_route_events(hostname,team_id,project_id,deployment_id,generation)
		VALUES($1,$2,$3,$4,1)`, host, item.team, item.project, deploymentID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE github_pushes SET status='preview_candidate',last_error_code='' WHERE delivery_id=$1 AND team_id=$2 AND project_id=$3`, item.delivery, item.team, item.project); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(team_id,action,subject_id) VALUES($1,'github.preview_candidate',$2)`, item.team, deploymentID); err != nil {
		return err
	}
	return nil
}

func newID(prefix string) (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(data), nil
}
