package httpapi

import (
	"crypto/sha256"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *server) createPreviewExchange(w http.ResponseWriter, r *http.Request, actor identity, projectID, deploymentID string) {
	if actor.role == "agent" && (actor.projectID != projectID || !hasAction(actor.actions, "previews:read")) {
		writeError(w, 404, "not_found")
		return
	}
	ctx := r.Context()
	var host string
	err := s.db.QueryRow(ctx, `SELECT d.desired_host FROM site_deployments d JOIN site_hosts h
		ON h.hostname=d.desired_host AND h.team_id=d.team_id AND h.project_id=d.project_id AND h.deployment_id=d.id
		WHERE d.team_id=$1 AND d.project_id=$2 AND d.id=$3 AND h.is_private AND h.is_fixed`, actor.teamID, projectID, deploymentID).Scan(&host)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found")
		return
	}
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	code, err := newToken()
	if err != nil {
		writeError(w, 503, "entropy_unavailable")
		return
	}
	digest := sha256.Sum256([]byte(code))
	expiry := time.Now().Add(time.Minute)
	command, err := s.db.Exec(ctx, `INSERT INTO preview_exchanges(code_hash,team_id,project_id,deployment_id,actor_token_id,expires_at)
		SELECT $1,$2,$3,$4,id,$5 FROM access_tokens
		WHERE id=$6 AND team_id=$2 AND revoked_at IS NULL AND expires_at>now()`, digest[:], actor.teamID, projectID, deploymentID, expiry, actor.id)
	if err != nil {
		writeDatabaseError(w, err)
		return
	}
	if command.RowsAffected() != 1 {
		writeError(w, 401, "unauthenticated")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 201, map[string]any{"code": code, "desiredHost": host, "expiresAt": expiry.UTC().Format(time.RFC3339)})
}
