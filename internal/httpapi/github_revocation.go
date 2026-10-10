package httpapi

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
)

// githubRevocationWebhook handles only HMAC-authenticated App events. A
// revoked grant stops new source work; already-running public sites are a
// separate lifecycle policy and are not silently deleted by this handler.
func (s *server) githubRevocationWebhook(w http.ResponseWriter, r *http.Request, event string, body []byte) {
	delivery := r.Header.Get("X-GitHub-Delivery")
	if !deliveryPattern.MatchString(delivery) {
		writeError(w, 422, "invalid_delivery")
		return
	}
	var input struct {
		Action string `json:"action"`
		Sender struct {
			ID    int64  `json:"id"`
			Login string `json:"login"`
		} `json:"sender"`
		Installation struct {
			ID int64 `json:"id"`
		} `json:"installation"`
		RepositoriesRemoved []struct {
			ID int64 `json:"id"`
		} `json:"repositories_removed"`
	}
	if err := json.Unmarshal(body, &input); err != nil {
		writeError(w, 422, "invalid_revocation")
		return
	}
	var revokeUser, revokeInstallation, revokeAllOAuthUsers bool
	switch event {
	case "github_app_authorization":
		if input.Action != "revoked" {
			writeError(w, 422, "invalid_revocation")
			return
		}
		revokeUser = true
		revokeAllOAuthUsers = input.Sender.ID <= 0 || input.Sender.Login == "ghost"
	case "installation":
		if input.Action != "deleted" && input.Action != "suspend" {
			writeJSON(w, 202, map[string]string{"status": "ignored_event"})
			return
		}
		if input.Installation.ID <= 0 {
			writeError(w, 422, "invalid_revocation")
			return
		}
		revokeInstallation = true
	case "installation_repositories":
		if input.Action != "removed" {
			writeJSON(w, 202, map[string]string{"status": "ignored_event"})
			return
		}
		if input.Installation.ID <= 0 {
			writeError(w, 422, "invalid_revocation")
			return
		}
		for _, entry := range input.RepositoriesRemoved {
			if entry.ID <= 0 {
				writeError(w, 422, "invalid_revocation")
				return
			}
		}
		revokeInstallation = true
	}
	ctx := r.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1),5)`, delivery); err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	hash := sha256.Sum256(body)
	var oldHash []byte
	var oldEvent, oldStatus string
	err = tx.QueryRow(ctx, `SELECT payload_hash,event,status FROM github_revocation_deliveries WHERE delivery_id=$1`, delivery).Scan(&oldHash, &oldEvent, &oldStatus)
	if err == nil {
		if oldEvent != event || !sameDigest(oldHash, hash) {
			writeError(w, 409, "delivery_conflict")
			return
		}
		writeJSON(w, 200, map[string]string{"status": oldStatus})
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 503, "database_unavailable")
		return
	}
	var rows pgx.Rows
	if revokeUser {
		if revokeAllOAuthUsers {
			rows, err = tx.Query(ctx, `UPDATE github_bindings SET status='revoked',updated_at=now()
				WHERE status='verified' AND authorized_github_user_id IS NOT NULL RETURNING team_id,project_id`)
		} else {
			rows, err = tx.Query(ctx, `UPDATE github_bindings SET status='revoked',updated_at=now()
				WHERE status='verified' AND authorized_github_user_id=$1 RETURNING team_id,project_id`, input.Sender.ID)
		}
	} else if revokeInstallation {
		if event == "installation_repositories" && len(input.RepositoriesRemoved) > 0 {
			ids := make([]int64, 0, len(input.RepositoriesRemoved))
			for _, entry := range input.RepositoriesRemoved {
				ids = append(ids, entry.ID)
			}
			rows, err = tx.Query(ctx, `UPDATE github_bindings SET status='revoked',updated_at=now()
				WHERE status='verified' AND installation_id=$1 AND repository_id=ANY($2::bigint[]) RETURNING team_id,project_id`, input.Installation.ID, ids)
		} else {
			rows, err = tx.Query(ctx, `UPDATE github_bindings SET status='revoked',updated_at=now()
				WHERE status='verified' AND installation_id=$1 RETURNING team_id,project_id`, input.Installation.ID)
		}
	}
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	type scope struct{ team, project string }
	var affected []scope
	for rows.Next() {
		var item scope
		if err := rows.Scan(&item.team, &item.project); err != nil {
			rows.Close()
			writeError(w, 503, "database_unavailable")
			return
		}
		affected = append(affected, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	for _, item := range affected {
		if _, err := tx.Exec(ctx, `UPDATE github_pushes SET status='failed',last_error_code='github_binding_revoked'
			WHERE team_id=$1 AND project_id=$2 AND status IN ('pending_commit_verification','preview_candidate','awaiting_isolated_build')`, item.team, item.project); err != nil {
			writeError(w, 503, "database_unavailable")
			return
		}
		if _, err := tx.Exec(ctx, `INSERT INTO audit_events(team_id,action,subject_id) VALUES($1,'github.binding.revoked',$2)`, item.team, item.project); err != nil {
			writeError(w, 503, "database_unavailable")
			return
		}
	}
	status := "ignored_unbound"
	if len(affected) > 0 {
		status = "binding_revoked"
	}
	if _, err := tx.Exec(ctx, `INSERT INTO github_revocation_deliveries(delivery_id,payload_hash,event,status) VALUES($1,$2,$3,$4)`, delivery, hash[:], event, status); err != nil {
		writeDatabaseError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeDatabaseError(w, err)
		return
	}
	writeJSON(w, 202, map[string]string{"status": status})
}
