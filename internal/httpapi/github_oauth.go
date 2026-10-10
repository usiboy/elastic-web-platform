package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"elastic-web-platform/internal/source/github"

	"github.com/jackc/pgx/v5"
)

func (s *server) startGitHubOAuth(w http.ResponseWriter, r *http.Request, actor identity, projectID string) {
	if actor.role != "owner" {
		writeError(w, 403, "forbidden")
		return
	}
	if s.oauth == nil || s.git == nil {
		writeError(w, 503, "github_oauth_unavailable")
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
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		writeError(w, 503, "entropy_unavailable")
		return
	}
	state := "ghs_" + base64.RawURLEncoding.EncodeToString(bytes)
	authorizeURL, err := s.oauth.AuthorizeURL(state)
	if err != nil {
		writeError(w, 503, "github_oauth_unavailable")
		return
	}
	ctx := r.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	defer tx.Rollback(ctx)
	var found string
	err = tx.QueryRow(ctx, `SELECT id FROM projects WHERE team_id=$1 AND id=$2 FOR UPDATE`, actor.teamID, projectID).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found")
		return
	}
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	var installed, repository int64
	err = tx.QueryRow(ctx, `SELECT installation_id,repository_id FROM github_bindings WHERE team_id=$1 AND project_id=$2`, actor.teamID, projectID).Scan(&installed, &repository)
	if err == nil && (installed != input.InstallationID || repository != input.RepositoryID) {
		writeError(w, 409, "binding_conflict")
		return
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 503, "database_unavailable")
		return
	}
	hash := sha256.Sum256([]byte(state))
	expires := time.Now().Add(10 * time.Minute)
	_, err = tx.Exec(ctx, `INSERT INTO github_oauth_states(state_hash,team_id,project_id,actor_token_id,installation_id,repository_id,expires_at)
		VALUES($1,$2,$3,$4,$5,$6,$7)`, hash[:], actor.teamID, projectID, actor.id, input.InstallationID, input.RepositoryID, expires)
	if err != nil {
		writeDatabaseError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		writeDatabaseError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	writeJSON(w, 201, map[string]string{"authorizationUrl": authorizeURL, "expiresAt": expires.UTC().Format(time.RFC3339)})
}

func (s *server) finishGitHubOAuth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	if r.TLS == nil {
		writeError(w, 403, "secure_callback_required")
		return
	}
	if s.oauth == nil || s.git == nil {
		writeError(w, 503, "github_oauth_unavailable")
		return
	}
	state, code := r.URL.Query().Get("state"), r.URL.Query().Get("code")
	if len(state) < 16 || len(state) > 128 || !strings.HasPrefix(state, "ghs_") || code == "" || len(code) > 512 {
		writeError(w, 401, "invalid_oauth_state")
		return
	}
	hash := sha256.Sum256([]byte(state))
	ctx := r.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	defer tx.Rollback(ctx)
	var teamID, projectID, actorID string
	var installationID, repositoryID int64
	var expires time.Time
	var consumed *time.Time
	err = tx.QueryRow(ctx, `SELECT team_id,project_id,actor_token_id,installation_id,repository_id,expires_at,consumed_at
		FROM github_oauth_states WHERE state_hash=$1 FOR UPDATE`, hash[:]).Scan(&teamID, &projectID, &actorID, &installationID, &repositoryID, &expires, &consumed)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 401, "invalid_oauth_state")
		return
	}
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	if consumed != nil {
		writeError(w, 409, "oauth_state_used")
		return
	}
	if !expires.After(time.Now()) {
		writeError(w, 401, "oauth_state_expired")
		return
	}
	var role string
	err = tx.QueryRow(ctx, `SELECT role FROM access_tokens WHERE id=$1 AND team_id=$2 AND revoked_at IS NULL AND expires_at>now() FOR UPDATE`, actorID, teamID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) || role != "owner" {
		writeError(w, 401, "owner_access_revoked")
		return
	}
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	var locked string
	err = tx.QueryRow(ctx, `SELECT id FROM projects WHERE id=$1 AND team_id=$2 FOR UPDATE`, projectID, teamID).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found")
		return
	}
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	var oldInstallation, oldRepository int64
	err = tx.QueryRow(ctx, `SELECT installation_id,repository_id FROM github_bindings WHERE team_id=$1 AND project_id=$2`, teamID, projectID).Scan(&oldInstallation, &oldRepository)
	if err == nil && (oldInstallation != installationID || oldRepository != repositoryID) {
		writeError(w, 409, "binding_conflict")
		return
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 503, "database_unavailable")
		return
	}
	userToken, err := s.oauth.ExchangeUserCode(ctx, code, state, repositoryID)
	if err != nil {
		writeError(w, 503, "github_oauth_exchange_failed")
		return
	}
	githubUserID, err := s.oauth.UserIdentity(ctx, userToken)
	if err != nil || githubUserID <= 0 {
		writeError(w, 503, "github_user_identity_failed")
		return
	}
	userRepo, err := s.oauth.UserCanReadRepository(ctx, userToken, installationID, repositoryID)
	if errors.Is(err, github.ErrUserRepoDenied) {
		writeError(w, 403, "github_user_repo_denied")
		return
	}
	if err != nil {
		writeError(w, 503, "github_user_verification_failed")
		return
	}
	appRepo, err := s.git.VerifyRepository(ctx, installationID, repositoryID)
	if err != nil {
		writeError(w, 503, "github_app_verification_failed")
		return
	}
	if appRepo.ID != userRepo.ID || !strings.EqualFold(appRepo.FullName, userRepo.FullName) {
		writeError(w, 403, "github_repo_identity_mismatch")
		return
	}
	if oldInstallation == 0 {
		_, err = tx.Exec(ctx, `INSERT INTO github_bindings(team_id,project_id,installation_id,repository_id,full_name,default_branch,status,verified_at,authorized_github_user_id)
			VALUES($1,$2,$3,$4,$5,$6,'verified',now(),$7)`, teamID, projectID, installationID, repositoryID, appRepo.FullName, appRepo.DefaultBranch, githubUserID)
	} else {
		_, err = tx.Exec(ctx, `UPDATE github_bindings SET full_name=$3,default_branch=$4,status='verified',verified_at=now(),updated_at=now(),authorized_github_user_id=$5
			WHERE team_id=$1 AND project_id=$2`, teamID, projectID, appRepo.FullName, appRepo.DefaultBranch, githubUserID)
	}
	if err != nil {
		writeDatabaseError(w, err)
		return
	}
	if _, err = tx.Exec(ctx, `UPDATE github_oauth_states SET consumed_at=now() WHERE state_hash=$1`, hash[:]); err != nil {
		writeDatabaseError(w, err)
		return
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(team_id,actor_token_id,action,subject_id) VALUES($1,$2,'github.binding.user_verified',$3)`, teamID, actorID, projectID); err != nil {
		writeDatabaseError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		writeDatabaseError(w, err)
		return
	}
	writeJSON(w, 200, binding{TeamID: teamID, ProjectID: projectID, InstallationID: installationID, RepositoryID: repositoryID, FullName: appRepo.FullName, DefaultBranch: appRepo.DefaultBranch, Status: "verified"})
}
