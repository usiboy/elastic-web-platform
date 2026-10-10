package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"

	"elastic-web-platform/internal/source/uploads"

	"github.com/jackc/pgx/v5"
)

type sourceMetadata struct {
	SourceID string `json:"sourceId"`
	SHA256   string `json:"sha256"`
	Size     int    `json:"size"`
	Files    int    `json:"files"`
	Status   string `json:"status"`
}

func canUpload(actor identity, projectID string) (bool, bool) {
	if actor.role == "agent" {
		if actor.projectID != projectID {
			return false, false
		}
		return hasAction(actor.actions, "sources:upload"), true
	}
	return actor.role == "owner" || actor.role == "developer", true
}

func (s *server) uploadSource(w http.ResponseWriter, r *http.Request, actor identity, projectID string) {
	allowed, sameScope := canUpload(actor, projectID)
	if !sameScope {
		writeError(w, 404, "not_found")
		return
	}
	if !allowed {
		writeError(w, 403, "forbidden")
		return
	}
	if s.sources == nil {
		writeError(w, 503, "source_store_unavailable")
		return
	}
	key := idempotencyKey(r)
	if key == "" {
		writeError(w, 422, "idempotency_key_required")
		return
	}
	if r.Header.Get("Content-Type") != "application/gzip" {
		writeError(w, 415, "unsupported_media_type")
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, uploads.MaxArchiveBytes))
	if err != nil {
		var max *http.MaxBytesError
		if errors.As(err, &max) {
			writeError(w, 413, "source_too_large")
		} else {
			writeError(w, 422, "invalid_source")
		}
		return
	}
	manifest, err := uploads.Validate(data)
	if err != nil {
		writeError(w, 422, "invalid_source")
		return
	}
	ctx := r.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	defer tx.Rollback(ctx)
	var locked string
	err = tx.QueryRow(ctx, `SELECT id FROM projects WHERE team_id=$1 AND id=$2 FOR UPDATE`, actor.teamID, projectID).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found")
		return
	}
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	requestHash := sha256.Sum256([]byte(manifest.Digest))
	var existingHash []byte
	var existingID string
	err = tx.QueryRow(ctx, `SELECT request_hash,source_id FROM upload_requests WHERE team_id=$1 AND project_id=$2 AND request_key=$3`, actor.teamID, projectID, key).Scan(&existingHash, &existingID)
	if err == nil {
		if !sameDigest(existingHash, requestHash) {
			writeError(w, 409, "idempotency_conflict")
			return
		}
		var result sourceMetadata
		err = tx.QueryRow(ctx, `SELECT id,sha256,size_bytes,file_count,status FROM source_uploads WHERE team_id=$1 AND project_id=$2 AND id=$3`, actor.teamID, projectID, existingID).Scan(&result.SourceID, &result.SHA256, &result.Size, &result.Files, &result.Status)
		if err != nil {
			writeError(w, 503, "database_unavailable")
			return
		}
		// A DB row alone is not enough to claim that the source is retrievable.
		if _, err = s.sources.Get(ctx, actor.teamID, projectID, result.SHA256); err != nil {
			writeError(w, 503, "source_store_unavailable")
			return
		}
		writeJSON(w, 200, result)
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 503, "database_unavailable")
		return
	}
	if err := s.sources.Put(ctx, actor.teamID, projectID, manifest.Digest, data); err != nil {
		writeError(w, 503, "source_store_unavailable")
		return
	}
	id, err := newID("src_")
	if err != nil {
		writeError(w, 503, "entropy_unavailable")
		return
	}
	result := sourceMetadata{SourceID: id, SHA256: manifest.Digest, Size: manifest.Size, Files: manifest.Files, Status: "stored_unverified"}
	_, err = tx.Exec(ctx, `INSERT INTO source_uploads(id,team_id,project_id,sha256,size_bytes,file_count,actor_token_id) VALUES($1,$2,$3,$4,$5,$6,$7)`, id, actor.teamID, projectID, manifest.Digest, manifest.Size, manifest.Files, actor.id)
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO upload_requests(team_id,project_id,request_key,request_hash,source_id) VALUES($1,$2,$3,$4,$5)`, actor.teamID, projectID, key, requestHash[:], id)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO audit_events(team_id,actor_token_id,action,subject_id) VALUES($1,$2,'source.uploaded',$3)`, actor.teamID, actor.id, id)
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

func (s *server) getSource(w http.ResponseWriter, r *http.Request, actor identity, projectID, sourceID string) {
	if actor.role == "agent" {
		if actor.projectID != projectID || !hasAction(actor.actions, "sources:read") {
			writeError(w, 404, "not_found")
			return
		}
	}
	if s.sources == nil {
		writeError(w, 503, "source_store_unavailable")
		return
	}
	var result sourceMetadata
	err := s.db.QueryRow(r.Context(), `SELECT id,sha256,size_bytes,file_count,status FROM source_uploads WHERE team_id=$1 AND project_id=$2 AND id=$3`, actor.teamID, projectID, sourceID).Scan(&result.SourceID, &result.SHA256, &result.Size, &result.Files, &result.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found")
		return
	}
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	if _, err := hex.DecodeString(result.SHA256); err != nil {
		writeError(w, 503, "source_store_unavailable")
		return
	}
	if _, err := s.sources.Get(r.Context(), actor.teamID, projectID, result.SHA256); err != nil {
		writeError(w, 503, "source_store_unavailable")
		return
	}
	writeJSON(w, 200, result)
}
