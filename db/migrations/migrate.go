package migrations

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed 001_identity_projects.sql 002_source_uploads.sql 003_github_sources.sql 004_pinned_git_sources.sql 005_static_routes.sql 006_static_candidates.sql 007_preview_sessions.sql 008_observed_routes.sql 009_git_pipeline.sql 010_git_production.sql 011_github_oauth_states.sql 012_github_revocations.sql 013_github_poll_origin.sql 014_fixture_host_readback.sql 015_project_resource_reservations.sql 016_github_pushes_project_lookup.sql 017_signed_github_builds.sql 018_github_build_jobs.sql 019_expired_github_build_jobs.sql 020_runtime_pilot.sql 021_failed_github_build_jobs.sql 022_actions_oidc_pilot.sql
var files embed.FS

// Apply serializes schema changes and rejects edits to an already applied SQL
// file. All migrations in this call commit together or roll back together.
func Apply(ctx context.Context, db *pgxpool.Pool) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext(current_schema()),7842)`); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version text PRIMARY KEY, sha256 text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	for _, name := range []string{"001_identity_projects.sql", "002_source_uploads.sql", "003_github_sources.sql", "004_pinned_git_sources.sql", "005_static_routes.sql", "006_static_candidates.sql", "007_preview_sessions.sql", "008_observed_routes.sql", "009_git_pipeline.sql", "010_git_production.sql", "011_github_oauth_states.sql", "012_github_revocations.sql", "013_github_poll_origin.sql", "014_fixture_host_readback.sql", "015_project_resource_reservations.sql", "016_github_pushes_project_lookup.sql", "017_signed_github_builds.sql", "018_github_build_jobs.sql", "019_expired_github_build_jobs.sql", "020_runtime_pilot.sql", "021_failed_github_build_jobs.sql", "022_actions_oidc_pilot.sql"} {
		statement, err := files.ReadFile(name)
		if err != nil {
			return err
		}
		hash := sha256.Sum256(statement)
		checksum := hex.EncodeToString(hash[:])
		var existing string
		err = tx.QueryRow(ctx, `SELECT sha256 FROM schema_migrations WHERE version=$1`, name).Scan(&existing)
		if err == nil {
			if existing != checksum {
				return fmt.Errorf("migration %s checksum changed", name)
			}
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err = tx.Exec(ctx, string(statement)); err != nil {
			return fmt.Errorf("apply %s: %w", name, err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO schema_migrations(version,sha256) VALUES($1,$2)`, name, checksum); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
