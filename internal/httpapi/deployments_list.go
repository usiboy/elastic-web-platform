package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
)

var deploymentCursorPattern = regexp.MustCompile(`^dep_[0-9a-f]{32}$`)

func (s *server) listDeployments(w http.ResponseWriter, r *http.Request, actor identity, projectID string) {
	if actor.role == "agent" && (actor.projectID != projectID || !hasAction(actor.actions, "deployments:read")) {
		writeError(w, 404, "not_found")
		return
	}
	ctx := r.Context()
	var found string
	err := s.db.QueryRow(ctx, `SELECT id FROM projects WHERE team_id=$1 AND id=$2`, actor.teamID, projectID).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found")
		return
	}
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	cursor := r.URL.Query().Get("cursor")
	var after any
	if cursor != "" {
		if !deploymentCursorPattern.MatchString(cursor) {
			writeError(w, 422, "invalid_cursor")
			return
		}
		var created time.Time
		err = s.db.QueryRow(ctx, `SELECT created_at FROM site_deployments WHERE team_id=$1 AND project_id=$2 AND id=$3`, actor.teamID, projectID, cursor).Scan(&created)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 422, "invalid_cursor")
			return
		}
		if err != nil {
			writeError(w, 503, "database_unavailable")
			return
		}
		after = created
	}
	rows, err := s.db.Query(ctx, `SELECT id,desired_host,status,observed_state,source_kind,source_id,created_at
		FROM site_deployments WHERE team_id=$1 AND project_id=$2
		AND ($3::timestamptz IS NULL OR (created_at,id)<($3::timestamptz,$4::text))
		ORDER BY created_at DESC,id DESC LIMIT 21`, actor.teamID, projectID, after, cursor)
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0, 21)
	for rows.Next() {
		var id, status, observed, sourceKind string
		var host, sourceID sql.NullString
		var created time.Time
		if err := rows.Scan(&id, &host, &status, &observed, &sourceKind, &sourceID, &created); err != nil {
			writeError(w, 503, "database_unavailable")
			return
		}
		item := map[string]any{"deploymentId": id, "desiredState": status, "observedState": observed, "sourceKind": sourceKind, "createdAt": created.UTC().Format(time.RFC3339Nano)}
		if host.Valid {
			item["desiredHost"] = host.String
		}
		if sourceID.Valid {
			item["sourceId"] = sourceID.String
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	more := len(items) > 20
	if more {
		items = items[:20]
	}
	result := map[string]any{"deployments": items, "hasMore": more}
	if more {
		result["nextCursor"] = items[len(items)-1]["deploymentId"]
	}
	writeJSON(w, 200, result)
}
