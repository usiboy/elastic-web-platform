package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
)

// Project activity contains only control-plane transitions. Builder stdout,
// secrets and tenant-provided strings are never exposed through this route.
func (s *server) listProjectActivity(w http.ResponseWriter, r *http.Request, actor identity, projectID string) {
	if !s.authorizeProjectRead(w, r, actor, projectID) {
		return
	}
	rows, err := s.db.Query(r.Context(), `SELECT a.action,a.subject_id,a.created_at FROM audit_events a
		WHERE a.team_id=$1 AND a.action IN ('github.branch_polled','github.preview_candidate','github.signed_build_candidate',
			'github.build_job_issued','github.build_job_failed','github.build_job_expired',
			'github.production_requested','github.superseded','preview.observed','production.observed')
		AND (EXISTS (SELECT 1 FROM github_pushes p WHERE p.team_id=$1 AND p.project_id=$2 AND p.delivery_id=a.subject_id)
			OR EXISTS (SELECT 1 FROM github_build_jobs j WHERE j.team_id=$1 AND j.project_id=$2 AND j.job_id=a.subject_id)
			OR EXISTS (SELECT 1 FROM site_deployments d WHERE d.team_id=$1 AND d.project_id=$2 AND d.id=a.subject_id))
		ORDER BY a.id DESC LIMIT 30`, actor.teamID, projectID)
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var action, subject string
		var at time.Time
		if err := rows.Scan(&action, &subject, &at); err != nil {
			writeError(w, 503, "database_unavailable")
			return
		}
		items = append(items, map[string]any{"action": action, "subjectId": subject, "at": at.UTC().Format(time.RFC3339Nano)})
	}
	if rows.Err() != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"events": items})
}

func (s *server) getProjectUsage(w http.ResponseWriter, r *http.Request, actor identity, projectID string) {
	if !s.authorizeProjectRead(w, r, actor, projectID) {
		return
	}
	var sourceBytes, verifiedSources, acceptedBuilds int64
	err := s.db.QueryRow(r.Context(), `SELECT COUNT(*),COALESCE(SUM(size_bytes),0) FROM github_verified_sources
		WHERE team_id=$1 AND project_id=$2`, actor.teamID, projectID).Scan(&verifiedSources, &sourceBytes)
	if err == nil {
		err = s.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM github_build_attestations WHERE team_id=$1 AND project_id=$2`, actor.teamID, projectID).Scan(&acceptedBuilds)
	}
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"verifiedSources": verifiedSources, "sourceArchiveBytes": sourceBytes,
		"acceptedBuilds": acceptedBuilds, "requestCount": nil, "cpuTimeMs": nil, "persistentStorageBytes": nil})
}

func (s *server) authorizeProjectRead(w http.ResponseWriter, r *http.Request, actor identity, projectID string) bool {
	if actor.role == "agent" && (actor.projectID != projectID || !hasAction(actor.actions, "projects:read")) {
		writeError(w, 404, "not_found")
		return false
	}
	var id string
	err := s.db.QueryRow(r.Context(), `SELECT id FROM projects WHERE team_id=$1 AND id=$2`, actor.teamID, projectID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found")
		return false
	}
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return false
	}
	return true
}
