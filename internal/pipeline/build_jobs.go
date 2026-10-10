package pipeline

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"time"

	"elastic-web-platform/internal/build"

	"github.com/jackc/pgx/v5"
)

type BuildJob struct {
	ID           string        `json:"jobId"`
	Nonce        string        `json:"jobNonce"`
	Version      int64         `json:"version"`
	SourceSHA256 string        `json:"sourceSha256"`
	CommitSHA    string        `json:"commitSha"`
	Plan         build.JobPlan `json:"plan"`
	IssuedAt     time.Time     `json:"issuedAt"`
	ExpiresAt    time.Time     `json:"expiresAt"`
}

// IssueBuildJob is an internal control-plane operation, not an API/Worker
// scheduler. It binds a short-lived random work order to verified Git bytes
// and a project's logical reservation, but does not reserve physical nodes or
// prove that an isolated Builder exists. A versioned reissue revokes the old
// nonce; the nonce itself is returned once and only its hash is persisted.
func (p *Processor) IssueBuildJob(ctx context.Context, teamID, projectID, deliveryID string, plan build.JobPlan, expectedVersion int64) (BuildJob, error) {
	var job BuildJob
	if p.db == nil || !plan.Valid() || teamID == "" || projectID == "" || deliveryID == "" || expectedVersion < 0 || expectedVersion > 1<<40 {
		return job, errors.New("invalid build job request")
	}
	tx, err := p.db.Begin(ctx)
	if err != nil {
		return job, err
	}
	defer tx.Rollback(ctx)
	var found string
	if err := tx.QueryRow(ctx, `SELECT id FROM teams WHERE id=$1 FOR UPDATE`, teamID).Scan(&found); err != nil {
		return job, errors.New("Team unavailable for build job")
	}
	var status, sourceID string
	err = tx.QueryRow(ctx, `SELECT p.status,s.id,s.archive_sha256,s.commit_sha FROM github_pushes p
		JOIN github_deliveries d ON d.delivery_id=p.delivery_id
		JOIN github_bindings b ON b.team_id=p.team_id AND b.project_id=p.project_id AND b.installation_id=d.installation_id AND b.repository_id=d.repository_id AND b.status='verified'
		JOIN github_verified_sources s ON s.delivery_id=p.delivery_id AND s.team_id=p.team_id AND s.project_id=p.project_id
		WHERE p.delivery_id=$1 AND p.team_id=$2 AND p.project_id=$3 FOR UPDATE OF p`, deliveryID, teamID, projectID).Scan(
		&status, &sourceID, &job.SourceSHA256, &job.CommitSHA)
	if err != nil || status != "awaiting_isolated_build" {
		return BuildJob{}, errors.New("Git source is not awaiting an isolated build")
	}
	var cpu, memory, storage int64
	err = tx.QueryRow(ctx, `SELECT cpu_milli,memory_mib,storage_mib FROM project_resource_reservations WHERE team_id=$1 AND project_id=$2`, teamID, projectID).Scan(&cpu, &memory, &storage)
	if err != nil || cpu < plan.CPUMilli || memory < plan.MemoryMiB || storage < plan.StorageMiB {
		return BuildJob{}, errors.New("build job exceeds the project reservation")
	}
	var version int64
	var oldStatus string
	err = tx.QueryRow(ctx, `SELECT version,status FROM github_build_jobs WHERE team_id=$1 AND project_id=$2 AND delivery_id=$3`, teamID, projectID, deliveryID).Scan(&version, &oldStatus)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return BuildJob{}, err
	}
	if version != expectedVersion || oldStatus == "completed" {
		return BuildJob{}, errors.New("build job version conflict")
	}
	job.ID, err = newID("job_")
	if err != nil {
		return BuildJob{}, err
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return BuildJob{}, err
	}
	job.Nonce = hex.EncodeToString(random)
	nonceHash := sha256.Sum256([]byte(job.Nonce))
	job.Version = version + 1
	job.Plan = plan
	job.IssuedAt = time.Now().UTC()
	job.ExpiresAt = job.IssuedAt.Add(15 * time.Minute)
	_, err = tx.Exec(ctx, `INSERT INTO github_build_jobs(team_id,project_id,delivery_id,source_id,job_id,nonce_hash,version,builder_id,image_digest,cpu_milli,memory_mib,storage_mib,status,issued_at,expires_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'issued',$13,$14)
		ON CONFLICT(team_id,project_id,delivery_id) DO UPDATE SET source_id=excluded.source_id,job_id=excluded.job_id,nonce_hash=excluded.nonce_hash,version=excluded.version,
		builder_id=excluded.builder_id,image_digest=excluded.image_digest,cpu_milli=excluded.cpu_milli,memory_mib=excluded.memory_mib,storage_mib=excluded.storage_mib,
		status='issued',issued_at=excluded.issued_at,expires_at=excluded.expires_at`, teamID, projectID, deliveryID, sourceID, job.ID, nonceHash[:], job.Version,
		plan.BuilderID, plan.ImageDigest, plan.CPUMilli, plan.MemoryMiB, plan.StorageMiB, job.IssuedAt, job.ExpiresAt)
	if err != nil {
		return BuildJob{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(team_id,action,subject_id) VALUES($1,'github.build_job_issued',$2)`, teamID, job.ID); err != nil {
		return BuildJob{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return BuildJob{}, err
	}
	return job, nil
}

// ExpireOneBuildJob turns an uncompleted, elapsed work order into a stable Git
// failure. Team→push→job lock order matches issue/accept, so concurrent
// completion cannot silently replace an already failed production candidate.
func (p *Processor) ExpireOneBuildJob(ctx context.Context) (bool, error) {
	if p.db == nil {
		return false, errors.New("build job store unavailable")
	}
	tx, err := p.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var teamID, projectID, deliveryID string
	err = tx.QueryRow(ctx, `SELECT team_id,project_id,delivery_id FROM github_build_jobs
		WHERE status='issued' AND expires_at<=now() ORDER BY expires_at LIMIT 1`).Scan(&teamID, &projectID, &deliveryID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var found string
	if err = tx.QueryRow(ctx, `SELECT id FROM teams WHERE id=$1 FOR UPDATE`, teamID).Scan(&found); err != nil {
		return false, err
	}
	var pushStatus string
	if err = tx.QueryRow(ctx, `SELECT status FROM github_pushes WHERE team_id=$1 AND project_id=$2 AND delivery_id=$3 FOR UPDATE`, teamID, projectID, deliveryID).Scan(&pushStatus); err != nil {
		return false, err
	}
	var jobID, jobStatus string
	var expiry time.Time
	if err = tx.QueryRow(ctx, `SELECT job_id,status,expires_at FROM github_build_jobs
		WHERE team_id=$1 AND project_id=$2 AND delivery_id=$3 FOR UPDATE`, teamID, projectID, deliveryID).Scan(&jobID, &jobStatus, &expiry); err != nil {
		return false, err
	}
	if jobStatus != "issued" || expiry.After(time.Now()) {
		return false, nil
	}
	if _, err = tx.Exec(ctx, `UPDATE github_build_jobs SET status='expired'
		WHERE team_id=$1 AND project_id=$2 AND delivery_id=$3 AND job_id=$4`, teamID, projectID, deliveryID, jobID); err != nil {
		return false, err
	}
	if pushStatus == "awaiting_isolated_build" {
		if _, err = tx.Exec(ctx, `UPDATE github_pushes SET status='failed',last_error_code='build_job_expired'
			WHERE team_id=$1 AND project_id=$2 AND delivery_id=$3`, teamID, projectID, deliveryID); err != nil {
			return false, err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(team_id,action,subject_id)
		VALUES($1,'github.build_job_expired',$2)`, teamID, jobID); err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// FailBuildJob marks a verified, isolated build failure immediately. The
// one-time nonce and job ID authorize the transition; no deployment or Alias
// changes on failure. Lock ordering matches issue/accept/expiry.
func (p *Processor) FailBuildJob(ctx context.Context, teamID, projectID, deliveryID, jobID, nonce, code string) (bool, error) {
	if p.db == nil || teamID == "" || projectID == "" || deliveryID == "" || jobID == "" || len(nonce) != 64 || !isLowerHex(nonce) {
		return false, errors.New("invalid build failure request")
	}
	switch code {
	case "build_timeout", "build_failed", "build_output_invalid", "builder_unavailable", "build_superseded":
	default:
		return false, errors.New("invalid build failure code")
	}
	tx, err := p.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var found string
	if err := tx.QueryRow(ctx, `SELECT id FROM teams WHERE id=$1 FOR UPDATE`, teamID).Scan(&found); err != nil {
		return false, err
	}
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM github_pushes WHERE team_id=$1 AND project_id=$2 AND delivery_id=$3 FOR UPDATE`, teamID, projectID, deliveryID).Scan(&status); err != nil {
		return false, err
	}
	var currentID, jobStatus string
	var nonceHash []byte
	if err := tx.QueryRow(ctx, `SELECT job_id,nonce_hash,status FROM github_build_jobs
		WHERE team_id=$1 AND project_id=$2 AND delivery_id=$3 FOR UPDATE`, teamID, projectID, deliveryID).Scan(&currentID, &nonceHash, &jobStatus); err != nil {
		return false, err
	}
	hash := sha256.Sum256([]byte(nonce))
	if currentID != jobID || subtle.ConstantTimeCompare(nonceHash, hash[:]) != 1 {
		return false, errors.New("build job identity mismatch")
	}
	if jobStatus == "failed" && status == "failed" {
		return false, nil
	}
	if jobStatus != "issued" || status != "awaiting_isolated_build" {
		return false, errors.New("build job is not pending")
	}
	if _, err := tx.Exec(ctx, `UPDATE github_build_jobs SET status='failed' WHERE team_id=$1 AND project_id=$2 AND delivery_id=$3 AND job_id=$4`, teamID, projectID, deliveryID, jobID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE github_pushes SET status='failed',last_error_code=$4 WHERE team_id=$1 AND project_id=$2 AND delivery_id=$3`, teamID, projectID, deliveryID, code); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(team_id,action,subject_id) VALUES($1,'github.build_job_failed',$2)`, teamID, jobID); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func isLowerHex(value string) bool {
	for _, ch := range value {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}
