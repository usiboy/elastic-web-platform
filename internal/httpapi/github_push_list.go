package httpapi

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
)

type gitPushCursor struct {
	At         time.Time `json:"at"`
	DeliveryID string    `json:"deliveryId"`
}

type gitPushSummary struct {
	DeliveryID    string     `json:"deliveryId"`
	CommitSHA     string     `json:"commitSha"`
	Ref           string     `json:"ref"`
	Status        string     `json:"status"`
	Attempts      int        `json:"attempts"`
	Origin        string     `json:"origin"`
	ReceivedAt    time.Time  `json:"receivedAt"`
	ErrorCode     string     `json:"errorCode,omitempty"`
	SourceID      string     `json:"sourceId,omitempty"`
	DeploymentID  string     `json:"deploymentId,omitempty"`
	SourceBytes   int64      `json:"sourceBytes,omitempty"`
	BuildStatus   string     `json:"buildStatus,omitempty"`
	BuilderID     string     `json:"builderId,omitempty"`
	BuildIssued   *time.Time `json:"buildIssuedAt,omitempty"`
	BuildExpiry   *time.Time `json:"buildExpiresAt,omitempty"`
	BuildAccepted *time.Time `json:"buildAcceptedAt,omitempty"`
}

func (s *server) listGitHubPushes(w http.ResponseWriter, r *http.Request, actor identity, projectID string) {
	if actor.role == "agent" && (actor.projectID != projectID || !hasAction(actor.actions, "projects:read")) {
		writeError(w, 404, "not_found")
		return
	}
	queryCursor := r.URL.Query().Get("cursor")
	var cursorAt any
	var cursorID string
	if queryCursor != "" {
		if len(queryCursor) > 512 {
			writeError(w, 422, "invalid_cursor")
			return
		}
		decoded, err := base64.RawURLEncoding.DecodeString(queryCursor)
		var cursor gitPushCursor
		if err != nil || json.Unmarshal(decoded, &cursor) != nil || cursor.At.IsZero() || cursor.DeliveryID == "" || len(cursor.DeliveryID) > 128 {
			writeError(w, 422, "invalid_cursor")
			return
		}
		cursorAt, cursorID = cursor.At, cursor.DeliveryID
	}
	var exists string
	err := s.db.QueryRow(r.Context(), `SELECT id FROM projects WHERE team_id=$1 AND id=$2`, actor.teamID, projectID).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found")
		return
	}
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	rows, err := s.db.Query(r.Context(), `SELECT p.delivery_id,p.commit_sha,p.ref,p.status,p.attempts,p.last_error_code,g.origin,g.received_at,s.id,d.id,
		COALESCE(s.size_bytes,0),COALESCE(j.status,''),COALESCE(j.builder_id,''),j.issued_at,j.expires_at,a.imported_at
		FROM github_pushes p JOIN github_deliveries g ON g.delivery_id=p.delivery_id
		LEFT JOIN github_verified_sources s ON s.delivery_id=p.delivery_id AND s.team_id=p.team_id AND s.project_id=p.project_id
		LEFT JOIN site_deployments d ON d.team_id=s.team_id AND d.project_id=s.project_id AND d.source_kind='github' AND d.source_id=s.id
		LEFT JOIN github_build_jobs j ON j.delivery_id=p.delivery_id AND j.team_id=p.team_id AND j.project_id=p.project_id
		LEFT JOIN github_build_attestations a ON a.delivery_id=p.delivery_id AND a.team_id=p.team_id AND a.project_id=p.project_id
		WHERE p.team_id=$1 AND p.project_id=$2 AND ($3::timestamptz IS NULL OR (g.received_at,p.delivery_id)<($3::timestamptz,$4))
		ORDER BY g.received_at DESC,p.delivery_id DESC LIMIT 21`, actor.teamID, projectID, cursorAt, cursorID)
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	defer rows.Close()
	pushes := make([]gitPushSummary, 0, 20)
	for rows.Next() {
		var item gitPushSummary
		var sourceID, deploymentID sql.NullString
		if err := rows.Scan(&item.DeliveryID, &item.CommitSHA, &item.Ref, &item.Status, &item.Attempts, &item.ErrorCode, &item.Origin, &item.ReceivedAt,
			&sourceID, &deploymentID, &item.SourceBytes, &item.BuildStatus, &item.BuilderID, &item.BuildIssued, &item.BuildExpiry, &item.BuildAccepted); err != nil {
			writeError(w, 503, "database_unavailable")
			return
		}
		item.SourceID, item.DeploymentID = sourceID.String, deploymentID.String
		pushes = append(pushes, item)
	}
	if rows.Err() != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	more := len(pushes) > 20
	if more {
		pushes = pushes[:20]
	}
	result := map[string]any{"pushes": pushes, "hasMore": more}
	if more {
		last := pushes[len(pushes)-1]
		encoded, _ := json.Marshal(gitPushCursor{At: last.ReceivedAt, DeliveryID: last.DeliveryID})
		result["nextCursor"] = base64.RawURLEncoding.EncodeToString(encoded)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, result)
}
