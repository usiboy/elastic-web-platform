package httpapi

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
)

func (s *server) getGitHubPushStatus(w http.ResponseWriter, r *http.Request, actor identity, projectID, deliveryID string) {
	if actor.role == "agent" && (actor.projectID != projectID || !hasAction(actor.actions, "projects:read")) {
		writeError(w, 404, "not_found")
		return
	}
	var status, commitSHA, ref, errorCode, origin string
	var attempts int
	var sourceID, deploymentID sql.NullString
	err := s.db.QueryRow(r.Context(), `SELECT p.status,p.commit_sha,p.ref,p.attempts,p.last_error_code,s.id,d.id,g.origin
		FROM github_pushes p
		JOIN github_deliveries g ON g.delivery_id=p.delivery_id
		LEFT JOIN github_verified_sources s ON s.delivery_id=p.delivery_id AND s.team_id=p.team_id AND s.project_id=p.project_id
		LEFT JOIN site_deployments d ON d.team_id=s.team_id AND d.project_id=s.project_id AND d.source_kind='github' AND d.source_id=s.id
		WHERE p.delivery_id=$1 AND p.team_id=$2 AND p.project_id=$3`, deliveryID, actor.teamID, projectID).Scan(&status, &commitSHA, &ref, &attempts, &errorCode, &sourceID, &deploymentID, &origin)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found")
		return
	}
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	result := map[string]any{"deliveryId": deliveryID, "commitSha": commitSHA, "ref": ref, "status": status, "attempts": attempts, "origin": origin}
	if errorCode != "" {
		result["errorCode"] = errorCode
	}
	if sourceID.Valid {
		result["sourceId"] = sourceID.String
	}
	if deploymentID.Valid {
		result["deploymentId"] = deploymentID.String
	}
	writeJSON(w, 200, result)
}
