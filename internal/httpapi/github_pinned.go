package httpapi

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"elastic-web-platform/internal/source/uploads"

	"github.com/jackc/pgx/v5"
)

type pinnedGitMetadata struct {
	SourceID      string `json:"sourceId"`
	CommitSHA     string `json:"commitSha"`
	TreeSHA       string `json:"treeSha"`
	ArchiveSHA256 string `json:"archiveSha256"`
	Size          int    `json:"size"`
	Status        string `json:"status"`
}

func (s *server) verifyPinnedGitPush(w http.ResponseWriter, r *http.Request, teamID, projectID, delivery string) {
	if s.bootstrap == "" || subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")), []byte(s.bootstrap)) != 1 {
		writeError(w, 401, "unauthenticated")
		return
	}
	if !deliveryPattern.MatchString(delivery) {
		writeError(w, 422, "invalid_delivery")
		return
	}
	if s.sources == nil || s.git == nil {
		writeError(w, 503, "source_verification_unavailable")
		return
	}
	ctx := r.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	defer tx.Rollback(ctx)
	var installationID, repositoryID int64
	var commitSHA, fullName string
	err = tx.QueryRow(ctx, `SELECT d.installation_id,d.repository_id,p.commit_sha,b.full_name
		FROM github_pushes p JOIN github_deliveries d ON d.delivery_id=p.delivery_id
		JOIN github_bindings b ON b.team_id=p.team_id AND b.project_id=p.project_id AND b.installation_id=d.installation_id AND b.repository_id=d.repository_id
		WHERE p.delivery_id=$1 AND p.team_id=$2 AND p.project_id=$3 AND b.status='verified' FOR UPDATE OF p`, delivery, teamID, projectID).Scan(&installationID, &repositoryID, &commitSHA, &fullName)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found")
		return
	}
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	var existing pinnedGitMetadata
	err = tx.QueryRow(ctx, `SELECT id,commit_sha,tree_sha,archive_sha256,size_bytes,status FROM github_verified_sources WHERE team_id=$1 AND project_id=$2 AND delivery_id=$3`, teamID, projectID, delivery).Scan(&existing.SourceID, &existing.CommitSHA, &existing.TreeSHA, &existing.ArchiveSHA256, &existing.Size, &existing.Status)
	if err == nil {
		if _, err := s.sources.Get(ctx, teamID, projectID, existing.ArchiveSHA256); err != nil {
			writeError(w, 503, "source_store_unavailable")
			return
		}
		writeJSON(w, 200, existing)
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 503, "database_unavailable")
		return
	}
	pinned, err := s.git.FetchPinned(ctx, installationID, repositoryID, commitSHA)
	if err != nil {
		writeError(w, 503, "github_source_unavailable")
		return
	}
	manifest, err := uploads.Validate(pinned.Archive)
	if err != nil || pinned.CommitSHA != commitSHA || pinned.Repository.ID != repositoryID || pinned.Repository.FullName != fullName || manifest.Digest != pinned.ArchiveSHA256 {
		writeError(w, 503, "github_source_mismatch")
		return
	}
	if err := s.sources.Put(ctx, teamID, projectID, manifest.Digest, pinned.Archive); err != nil {
		writeError(w, 503, "source_store_unavailable")
		return
	}
	id, err := newID("gsrc_")
	if err != nil {
		writeError(w, 503, "entropy_unavailable")
		return
	}
	result := pinnedGitMetadata{SourceID: id, CommitSHA: pinned.CommitSHA, TreeSHA: pinned.TreeSHA, ArchiveSHA256: manifest.Digest, Size: manifest.Size, Status: "verified_source"}
	_, err = tx.Exec(ctx, `INSERT INTO github_verified_sources(id,team_id,project_id,delivery_id,commit_sha,tree_sha,archive_sha256,size_bytes) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, id, teamID, projectID, delivery, result.CommitSHA, result.TreeSHA, result.ArchiveSHA256, result.Size)
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO audit_events(team_id,action,subject_id) VALUES($1,'github.source_verified',$2)`, teamID, id)
	}
	if err != nil {
		writeDatabaseError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		writeDatabaseError(w, err)
		return
	}
	writeJSON(w, 201, result)
}

func (s *server) getPinnedGitSource(w http.ResponseWriter, r *http.Request, actor identity, projectID, sourceID string) {
	if actor.role == "agent" && (actor.projectID != projectID || !hasAction(actor.actions, "sources:read")) {
		writeError(w, 404, "not_found")
		return
	}
	if s.sources == nil {
		writeError(w, 503, "source_store_unavailable")
		return
	}
	var result pinnedGitMetadata
	err := s.db.QueryRow(r.Context(), `SELECT id,commit_sha,tree_sha,archive_sha256,size_bytes,status FROM github_verified_sources WHERE id=$1 AND team_id=$2 AND project_id=$3`, sourceID, actor.teamID, projectID).Scan(&result.SourceID, &result.CommitSHA, &result.TreeSHA, &result.ArchiveSHA256, &result.Size, &result.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found")
		return
	}
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	if _, err := s.sources.Get(r.Context(), actor.teamID, projectID, result.ArchiveSHA256); err != nil {
		writeError(w, 503, "source_store_unavailable")
		return
	}
	writeJSON(w, 200, result)
}
