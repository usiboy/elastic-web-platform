package httpapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"

	"elastic-web-platform/internal/source/github"

	"github.com/jackc/pgx/v5"
)

var deliveryPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

type binding struct {
	TeamID         string `json:"teamId"`
	ProjectID      string `json:"projectId"`
	InstallationID int64  `json:"installationId"`
	RepositoryID   int64  `json:"repositoryId"`
	FullName       string `json:"fullName"`
	DefaultBranch  string `json:"defaultBranch"`
	Status         string `json:"status"`
}

func (s *server) assignGitHubBinding(w http.ResponseWriter, r *http.Request, teamID, projectID string) {
	if s.bootstrap == "" || subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")), []byte(s.bootstrap)) != 1 {
		writeError(w, 401, "unauthenticated")
		return
	}
	var input struct {
		InstallationID int64 `json:"installationId"`
		RepositoryID   int64 `json:"repositoryId"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	if input.InstallationID <= 0 || input.RepositoryID <= 0 {
		writeError(w, 422, "invalid_binding")
		return
	}
	ctx := r.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	defer tx.Rollback(ctx)
	var id string
	if err = tx.QueryRow(ctx, `SELECT id FROM projects WHERE team_id=$1 AND id=$2 FOR UPDATE`, teamID, projectID).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 404, "not_found")
		} else {
			writeError(w, 503, "database_unavailable")
		}
		return
	}
	var current binding
	err = tx.QueryRow(ctx, `SELECT installation_id,repository_id,full_name,default_branch,status FROM github_bindings WHERE team_id=$1 AND project_id=$2`, teamID, projectID).Scan(&current.InstallationID, &current.RepositoryID, &current.FullName, &current.DefaultBranch, &current.Status)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 503, "database_unavailable")
		return
	}
	if err == nil && (current.InstallationID != input.InstallationID || current.RepositoryID != input.RepositoryID) {
		writeError(w, 409, "binding_conflict")
		return
	}
	if err == nil && current.Status == "revoked" {
		writeError(w, 409, "binding_revoked")
		return
	}
	created := errors.Is(err, pgx.ErrNoRows)
	if created {
		_, err = tx.Exec(ctx, `INSERT INTO github_bindings(team_id,project_id,installation_id,repository_id,status) VALUES($1,$2,$3,$4,'pending')`, teamID, projectID, input.InstallationID, input.RepositoryID)
		if err != nil {
			writeDatabaseError(w, err)
			return
		}
	}
	result := binding{TeamID: teamID, ProjectID: projectID, InstallationID: input.InstallationID, RepositoryID: input.RepositoryID, Status: "pending"}
	if s.git != nil {
		repo, verifyErr := s.git.VerifyRepository(ctx, input.InstallationID, input.RepositoryID)
		if verifyErr != nil {
			if created {
				if err = tx.Commit(ctx); err != nil {
					writeError(w, 503, "database_unavailable")
					return
				}
			}
			writeError(w, 503, "github_verification_unavailable")
			return
		}
		result.FullName, result.DefaultBranch, result.Status = repo.FullName, repo.DefaultBranch, "verified"
		_, err = tx.Exec(ctx, `UPDATE github_bindings SET full_name=$3,default_branch=$4,status='verified',verified_at=now(),updated_at=now() WHERE team_id=$1 AND project_id=$2`, teamID, projectID, repo.FullName, repo.DefaultBranch)
		if err != nil {
			writeDatabaseError(w, err)
			return
		}
	} else if !created {
		result.FullName, result.DefaultBranch, result.Status = current.FullName, current.DefaultBranch, current.Status
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events(team_id,action,subject_id) VALUES($1,$2,$3)`, teamID, "github.binding."+result.Status, projectID)
	if err != nil {
		writeDatabaseError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		writeDatabaseError(w, err)
		return
	}
	if result.Status == "pending" {
		writeJSON(w, 202, result)
	} else {
		writeJSON(w, 200, result)
	}
}

func (s *server) getGitHubBinding(w http.ResponseWriter, r *http.Request, actor identity, projectID string) {
	if actor.role == "agent" && (actor.projectID != projectID || !hasAction(actor.actions, "projects:read")) {
		writeError(w, 404, "not_found")
		return
	}
	var result binding
	err := s.db.QueryRow(r.Context(), `SELECT installation_id,repository_id,full_name,default_branch,status FROM github_bindings WHERE team_id=$1 AND project_id=$2`, actor.teamID, projectID).Scan(&result.InstallationID, &result.RepositoryID, &result.FullName, &result.DefaultBranch, &result.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found")
		return
	}
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	result.TeamID, result.ProjectID = actor.teamID, projectID
	writeJSON(w, 200, result)
}

func (s *server) githubWebhook(w http.ResponseWriter, r *http.Request) {
	if s.webhookSecret == "" {
		writeError(w, 503, "github_webhook_disabled")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 256<<10))
	if err != nil {
		var max *http.MaxBytesError
		if errors.As(err, &max) {
			writeError(w, 413, "webhook_too_large")
		} else {
			writeError(w, 422, "invalid_webhook")
		}
		return
	}
	if !github.VerifySignature(s.webhookSecret, body, r.Header.Get("X-Hub-Signature-256")) {
		writeError(w, 401, "invalid_signature")
		return
	}
	event := r.Header.Get("X-GitHub-Event")
	if event == "github_app_authorization" || event == "installation" || event == "installation_repositories" {
		s.githubRevocationWebhook(w, r, event, body)
		return
	}
	if event != "push" {
		writeJSON(w, 202, map[string]string{"status": "ignored_event"})
		return
	}
	delivery := r.Header.Get("X-GitHub-Delivery")
	if !deliveryPattern.MatchString(delivery) {
		writeError(w, 422, "invalid_delivery")
		return
	}
	var input struct {
		Installation struct {
			ID int64 `json:"id"`
		} `json:"installation"`
		Repository struct {
			ID int64 `json:"id"`
		} `json:"repository"`
		Ref     string `json:"ref"`
		After   string `json:"after"`
		Deleted bool   `json:"deleted"`
	}
	if err := json.Unmarshal(body, &input); err != nil || input.Installation.ID <= 0 || input.Repository.ID <= 0 || !strings.HasPrefix(input.Ref, "refs/heads/") || len(input.Ref) <= len("refs/heads/") || len(input.Ref) > 256 {
		writeError(w, 422, "invalid_push")
		return
	}
	if input.Deleted {
		writeJSON(w, 202, map[string]string{"status": "ignored_deleted"})
		return
	}
	if !commitPattern.MatchString(input.After) {
		writeError(w, 422, "invalid_push")
		return
	}
	ctx := r.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1),2)`, delivery); err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	hash := sha256.Sum256(body)
	var oldHash []byte
	var oldStatus string
	err = tx.QueryRow(ctx, `SELECT payload_hash,status FROM github_deliveries WHERE delivery_id=$1`, delivery).Scan(&oldHash, &oldStatus)
	if err == nil {
		if !sameDigest(oldHash, hash) {
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
	rows, err := tx.Query(ctx, `SELECT team_id,project_id,full_name FROM github_bindings WHERE installation_id=$1 AND repository_id=$2 AND status='verified'`, input.Installation.ID, input.Repository.ID)
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	type match struct{ team, project, fullName string }
	var matched []match
	for rows.Next() {
		var m match
		if err := rows.Scan(&m.team, &m.project, &m.fullName); err != nil {
			rows.Close()
			writeError(w, 503, "database_unavailable")
			return
		}
		matched = append(matched, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	status := "ignored_unbound"
	if len(matched) > 0 {
		if s.git == nil {
			writeError(w, 503, "github_verification_unavailable")
			return
		}
		repo, verifyErr := s.git.VerifyRepository(ctx, input.Installation.ID, input.Repository.ID)
		if verifyErr != nil {
			writeError(w, 503, "github_verification_unavailable")
			return
		}
		for _, m := range matched {
			if m.fullName != repo.FullName {
				writeError(w, 503, "github_identity_changed")
				return
			}
		}
		status = "pending_commit_verification" // No source archive or runnable build has been verified yet.
	}
	_, err = tx.Exec(ctx, `INSERT INTO github_deliveries(delivery_id,payload_hash,installation_id,repository_id,ref,commit_sha,status) VALUES($1,$2,$3,$4,$5,$6,$7)`, delivery, hash[:], input.Installation.ID, input.Repository.ID, input.Ref, input.After, status)
	if err != nil {
		writeDatabaseError(w, err)
		return
	}
	for _, m := range matched {
		_, err = tx.Exec(ctx, `INSERT INTO github_pushes(delivery_id,team_id,project_id,commit_sha,ref,status) VALUES($1,$2,$3,$4,$5,'pending_commit_verification')`, delivery, m.team, m.project, input.After, input.Ref)
		if err != nil {
			writeDatabaseError(w, err)
			return
		}
	}
	if err = tx.Commit(ctx); err != nil {
		writeDatabaseError(w, err)
		return
	}
	writeJSON(w, 202, map[string]string{"status": status})
}
