package pipeline

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"elastic-web-platform/internal/build"
	"elastic-web-platform/internal/bundle"
	"elastic-web-platform/internal/framework"
	"elastic-web-platform/internal/source/uploads"

	"github.com/jackc/pgx/v5"
)

type SignedBuildResult struct {
	DeploymentID string `json:"deploymentId"`
	Host         string `json:"desiredHost"`
	BundleDigest string `json:"bundleDigest"`
	Status       string `json:"status"`
}

// AcceptSignedBuild is an internal, non-HTTP handoff. Its public key must
// belong exclusively to an independently admitted Builder. Test-generated
// signatures prove only the handoff checks, never actual runtime isolation.
func (p *Processor) AcceptSignedBuild(ctx context.Context, signed build.SignedBuild, archive []byte, trusted ed25519.PublicKey) (SignedBuildResult, error) {
	var result SignedBuildResult
	if p.db == nil || p.sources == nil || p.artifacts == nil || len(p.previewSuffix) > 180 || !suffixPattern.MatchString(p.previewSuffix) || strings.Contains(p.previewSuffix, "..") {
		return result, errors.New("signed build handoff is not configured")
	}
	if err := build.VerifyBuild(trusted, signed, archive); err != nil {
		return result, err
	}
	claim := signed.Statement
	message, _ := json.Marshal(claim)
	statementHash := sha256.Sum256(message)
	publicHash := sha256.Sum256(trusted)
	statementDigest := hex.EncodeToString(statementHash[:])
	publicDigest := hex.EncodeToString(publicHash[:])
	signature, _ := base64.RawStdEncoding.DecodeString(signed.Signature) // verified above

	tx, err := p.db.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	var found string
	if err := tx.QueryRow(ctx, `SELECT id FROM teams WHERE id=$1 FOR UPDATE`, claim.TeamID).Scan(&found); err != nil {
		return SignedBuildResult{}, errors.New("build job Team unavailable")
	}
	var status, sourceID, sourceSHA, commitSHA string
	err = tx.QueryRow(ctx, `SELECT p.status,s.id,s.archive_sha256,s.commit_sha FROM github_pushes p
		JOIN github_deliveries d ON d.delivery_id=p.delivery_id
		JOIN github_bindings b ON b.team_id=p.team_id AND b.project_id=p.project_id AND b.installation_id=d.installation_id AND b.repository_id=d.repository_id AND b.status='verified'
		JOIN github_verified_sources s ON s.delivery_id=p.delivery_id AND s.team_id=p.team_id AND s.project_id=p.project_id
		WHERE p.delivery_id=$1 AND p.team_id=$2 AND p.project_id=$3 FOR UPDATE OF p`, claim.DeliveryID, claim.TeamID, claim.ProjectID).Scan(&status, &sourceID, &sourceSHA, &commitSHA)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return result, errors.New("verified Git source or binding unavailable")
		}
		return result, err
	}
	if sourceSHA != claim.SourceSHA256 || commitSHA != claim.CommitSHA {
		return result, errors.New("signed build does not match fixed Git source")
	}
	var jobID, builderID, imageDigest, jobStatus string
	var nonceHash []byte
	var cpu, memory, storage int64
	var issuedAt, expiresAt time.Time
	err = tx.QueryRow(ctx, `SELECT job_id,nonce_hash,builder_id,image_digest,cpu_milli,memory_mib,storage_mib,status,issued_at,expires_at
		FROM github_build_jobs WHERE team_id=$1 AND project_id=$2 AND delivery_id=$3 FOR UPDATE`, claim.TeamID, claim.ProjectID, claim.DeliveryID).Scan(
		&jobID, &nonceHash, &builderID, &imageDigest, &cpu, &memory, &storage, &jobStatus, &issuedAt, &expiresAt)
	if err != nil || jobID != claim.JobID || builderID != claim.BuilderID || imageDigest != claim.ImageDigest ||
		cpu != claim.CPUMilli || memory != claim.MemoryMiB || storage != claim.StorageMiB {
		return SignedBuildResult{}, errors.New("signed output does not match the current build job")
	}
	expectedNonce := sha256.Sum256([]byte(claim.JobNonce))
	if subtle.ConstantTimeCompare(nonceHash, expectedNonce[:]) != 1 {
		return SignedBuildResult{}, errors.New("build job nonce no longer valid")
	}
	var existingHash, existingKey, existingOutput string
	err = tx.QueryRow(ctx, `SELECT a.statement_sha256,a.public_key_sha256,a.output_sha256,a.deployment_id,d.desired_host,d.bundle_digest
		FROM github_build_attestations a JOIN site_deployments d ON d.id=a.deployment_id AND d.team_id=a.team_id AND d.project_id=a.project_id
		WHERE a.team_id=$1 AND a.project_id=$2 AND a.delivery_id=$3`, claim.TeamID, claim.ProjectID, claim.DeliveryID).Scan(
		&existingHash, &existingKey, &existingOutput, &result.DeploymentID, &result.Host, &result.BundleDigest)
	if err == nil {
		if existingHash != statementDigest || existingKey != publicDigest || existingOutput != claim.OutputSHA256 {
			return SignedBuildResult{}, errors.New("fixed Git source already has different signed output")
		}
		if stored, readErr := p.artifacts.Get(ctx, claim.TeamID, claim.ProjectID, result.BundleDigest); readErr != nil || stored.Verify() != nil {
			return SignedBuildResult{}, errors.New("signed artifact bytes cannot be verified")
		}
		result.Status = status
		return result, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return SignedBuildResult{}, err
	}
	if status != "awaiting_isolated_build" || jobStatus != "issued" || !time.Now().Before(expiresAt) || claim.FinishedAt.Before(issuedAt) || claim.FinishedAt.After(expiresAt) {
		return SignedBuildResult{}, errors.New("fixed Git source is not awaiting isolated build")
	}
	var availableCPU, availableMemory, availableStorage int64
	err = tx.QueryRow(ctx, `SELECT cpu_milli,memory_mib,storage_mib FROM project_resource_reservations WHERE team_id=$1 AND project_id=$2`, claim.TeamID, claim.ProjectID).Scan(&availableCPU, &availableMemory, &availableStorage)
	if err != nil || availableCPU < cpu || availableMemory < memory || availableStorage < storage {
		return SignedBuildResult{}, errors.New("project reservation was released before the signed build was accepted")
	}
	source, err := p.sources.Get(ctx, claim.TeamID, claim.ProjectID, sourceSHA)
	if err != nil {
		return SignedBuildResult{}, err
	}
	manifest, err := uploads.Validate(source)
	if err != nil || manifest.Digest != sourceSHA {
		return SignedBuildResult{}, errors.New("fixed Git source bytes changed")
	}
	preset, err := framework.Detect(source)
	if err != nil || !preset.RequiresBuild || (preset.Name != "vite-vue" && preset.Name != "vite-react") {
		return SignedBuildResult{}, errors.New("Git source is not an admitted Vite preset")
	}
	built, err := bundle.CaptureBuiltArchive(claim.TeamID, claim.ProjectID, sourceSHA,
		"signed-gvisor:"+claim.BuilderID+":"+claim.ImageDigest, archive)
	if err != nil || built.Verify() != nil {
		return SignedBuildResult{}, errors.New("signed output contains invalid static assets")
	}
	if err := p.artifacts.Put(ctx, built); err != nil {
		return SignedBuildResult{}, err
	}
	result.DeploymentID, err = newID("dep_")
	if err != nil {
		return SignedBuildResult{}, err
	}
	result.Host = "d-" + strings.TrimPrefix(result.DeploymentID, "dep_") + "." + p.previewSuffix
	result.BundleDigest = built.Manifest.Digest
	_, err = tx.Exec(ctx, `INSERT INTO site_deployments(id,team_id,project_id,bundle_digest,source_sha256,source_kind,source_id,desired_host)
		VALUES($1,$2,$3,$4,$5,'github',$6,$7)`, result.DeploymentID, claim.TeamID, claim.ProjectID, result.BundleDigest, sourceSHA, sourceID, result.Host)
	if err != nil {
		return SignedBuildResult{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO site_hosts(hostname,team_id,project_id,deployment_id,generation,is_fixed,is_private)
		VALUES($1,$2,$3,$4,1,true,true)`, result.Host, claim.TeamID, claim.ProjectID, result.DeploymentID); err != nil {
		return SignedBuildResult{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO site_route_events(hostname,team_id,project_id,deployment_id,generation)
		VALUES($1,$2,$3,$4,1)`, result.Host, claim.TeamID, claim.ProjectID, result.DeploymentID); err != nil {
		return SignedBuildResult{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO github_build_attestations(team_id,project_id,delivery_id,source_id,deployment_id,statement_sha256,public_key_sha256,output_sha256,builder_id,builder_image_sha256,signature)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, claim.TeamID, claim.ProjectID, claim.DeliveryID, sourceID, result.DeploymentID,
		statementDigest, publicDigest, claim.OutputSHA256, claim.BuilderID, claim.ImageDigest, signature); err != nil {
		return SignedBuildResult{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE github_build_jobs SET status='completed' WHERE team_id=$1 AND project_id=$2 AND delivery_id=$3 AND job_id=$4`, claim.TeamID, claim.ProjectID, claim.DeliveryID, jobID); err != nil {
		return SignedBuildResult{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE github_pushes SET status='preview_candidate',last_error_code='' WHERE delivery_id=$1 AND team_id=$2 AND project_id=$3`, claim.DeliveryID, claim.TeamID, claim.ProjectID); err != nil {
		return SignedBuildResult{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(team_id,action,subject_id) VALUES($1,'github.signed_build_candidate',$2)`, claim.TeamID, result.DeploymentID); err != nil {
		return SignedBuildResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return SignedBuildResult{}, err
	}
	result.Status = "preview_candidate"
	return result, nil
}
