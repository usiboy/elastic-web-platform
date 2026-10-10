package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"elastic-web-platform/internal/bundle"
	"elastic-web-platform/internal/source/github"
	"elastic-web-platform/internal/source/githubactions"
	"elastic-web-platform/internal/source/uploads"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
var projectCursorPattern = regexp.MustCompile(`^p_[0-9a-f]{32}$`)

type server struct {
	db               *pgxpool.Pool
	bootstrap        string
	sources          uploads.Store
	artifacts        bundle.Store
	previewSuffix    string
	productionSuffix string
	git              GitHubVerifier
	oauth            GitHubOAuth
	webhookSecret    string
	actionsVerifier  GitHubActionsVerifier
	actionsGit       GitHubActionsSourceFactory
}

type GitHubVerifier interface {
	VerifyRepository(context.Context, int64, int64) (github.Repository, error)
	FetchPinned(context.Context, int64, int64, string) (github.PinnedSource, error)
}

type GitHubOAuth interface {
	AuthorizeURL(string) (string, error)
	ExchangeUserCode(context.Context, string, string, int64) (string, error)
	UserIdentity(context.Context, string) (int64, error)
	UserCanReadRepository(context.Context, string, int64, int64) (github.Repository, error)
}

type GitHubActionsVerifier interface {
	Verify(context.Context, string, githubactions.Pilot) (githubactions.Claims, error)
}

type GitHubActionsSource interface {
	CurrentBranchHead(context.Context, int64, int64, string) (string, error)
	FetchPinned(context.Context, int64, int64, string) (github.PinnedSource, error)
}

type GitHubActionsSourceFactory func(int64, int64, string) (GitHubActionsSource, error)

func WithGitHubActionsPilot(verifier GitHubActionsVerifier, source GitHubActionsSourceFactory) Option {
	return func(s *server) { s.actionsVerifier, s.actionsGit = verifier, source }
}

type identity struct {
	id, teamID, projectID, role string
	actions                     []string
	expiresAt                   time.Time
}

type project struct {
	ID     string `json:"id"`
	TeamID string `json:"teamId"`
	Slug   string `json:"slug"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type Option func(*server)

func WithSourceStore(store uploads.Store) Option {
	return func(s *server) { s.sources = store }
}

func WithArtifactStore(store bundle.Store) Option {
	return func(s *server) { s.artifacts = store }
}

func WithPreviewSuffix(suffix string) Option {
	return func(s *server) { s.previewSuffix = suffix }
}

func WithProductionSuffix(suffix string) Option {
	return func(s *server) { s.productionSuffix = suffix }
}

func WithGitHubVerifier(verifier GitHubVerifier) Option {
	return func(s *server) { s.git = verifier }
}

func WithGitHubOAuth(client GitHubOAuth) Option {
	return func(s *server) { s.oauth = client }
}

func WithGitHubWebhookSecret(secret string) Option {
	return func(s *server) { s.webhookSecret = secret }
}

func NewServer(db *pgxpool.Pool, bootstrapToken string, options ...Option) http.Handler {
	s := &server{db: db, bootstrap: bootstrapToken}
	for _, option := range options {
		option(s)
	}
	return s
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/livez" && r.Method == http.MethodGet {
		writeJSON(w, 200, map[string]string{"status": "alive"})
		return
	}
	if r.URL.Path == "/readyz" && r.Method == http.MethodGet {
		if err := s.db.Ping(r.Context()); err != nil {
			writeError(w, 503, "database_unavailable")
			return
		}
		writeJSON(w, 200, map[string]string{"status": "ready"})
		return
	}
	if r.URL.Path == "/v1/me" && r.Method == http.MethodGet {
		actor, ok := s.authenticate(w, r)
		if !ok {
			return
		}
		result := map[string]any{"teamId": actor.teamID, "role": actor.role, "actions": actor.actions, "expiresAt": actor.expiresAt.UTC().Format(time.RFC3339)}
		if actor.projectID != "" {
			result["projectId"] = actor.projectID
		}
		writeJSON(w, 200, result)
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) == 2 && parts[0] == "v1" && parts[1] == "teams" && r.Method == http.MethodPost {
		s.createTeam(w, r)
		return
	}
	if len(parts) == 3 && parts[0] == "v1" && parts[1] == "hooks" && parts[2] == "github" && r.Method == http.MethodPost {
		s.githubWebhook(w, r)
		return
	}
	if len(parts) == 3 && parts[0] == "v1" && parts[1] == "hooks" && parts[2] == "github-actions" && r.Method == http.MethodPost {
		s.githubActionsPilot(w, r)
		return
	}
	if len(parts) == 4 && parts[0] == "v1" && parts[1] == "oauth" && parts[2] == "github" && parts[3] == "callback" && r.Method == http.MethodGet {
		s.finishGitHubOAuth(w, r)
		return
	}
	if len(parts) == 7 && parts[0] == "v1" && parts[1] == "admin" && parts[2] == "teams" && parts[4] == "projects" && parts[6] == "github-binding" && r.Method == http.MethodPost {
		s.assignGitHubBinding(w, r, parts[3], parts[5])
		return
	}
	if len(parts) == 8 && parts[0] == "v1" && parts[1] == "admin" && parts[2] == "teams" && parts[4] == "projects" && parts[6] == "github-pushes" && strings.HasSuffix(parts[7], ":verify") && r.Method == http.MethodPost {
		s.verifyPinnedGitPush(w, r, parts[3], parts[5], strings.TrimSuffix(parts[7], ":verify"))
		return
	}
	if len(parts) == 5 && parts[0] == "v1" && parts[1] == "admin" && parts[2] == "teams" && parts[4] == "resource-budget" && r.Method == http.MethodPut {
		s.setTeamResourceBudget(w, r, parts[3])
		return
	}
	if len(parts) < 4 || parts[0] != "v1" || parts[1] != "teams" || parts[2] == "" {
		writeError(w, 404, "not_found")
		return
	}
	actor, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	if actor.teamID != parts[2] {
		writeError(w, 404, "not_found")
		return
	}
	switch {
	case len(parts) == 4 && parts[3] == "projects" && r.Method == http.MethodPost:
		s.createProject(w, r, actor)
	case len(parts) == 4 && parts[3] == "projects" && r.Method == http.MethodGet:
		s.listProjects(w, r, actor)
	case len(parts) == 5 && parts[3] == "projects" && r.Method == http.MethodGet:
		s.getProject(w, r, actor, parts[4])
	case len(parts) == 4 && parts[3] == "resources" && r.Method == http.MethodGet:
		s.getTeamResources(w, r, actor)
	case len(parts) == 6 && parts[3] == "projects" && parts[5] == "resources" && r.Method == http.MethodGet:
		s.getProjectResources(w, r, actor, parts[4])
	case len(parts) == 6 && parts[3] == "projects" && parts[5] == "resources" && r.Method == http.MethodPut:
		s.setProjectResources(w, r, actor, parts[4])
	case len(parts) == 7 && parts[3] == "projects" && parts[5] == "sources" && parts[6] == "uploads" && r.Method == http.MethodPost:
		s.uploadSource(w, r, actor, parts[4])
	case len(parts) == 7 && parts[3] == "projects" && parts[5] == "sources" && r.Method == http.MethodGet:
		s.getSource(w, r, actor, parts[4], parts[6])
	case len(parts) == 6 && parts[3] == "projects" && parts[5] == "github-binding" && r.Method == http.MethodGet:
		s.getGitHubBinding(w, r, actor, parts[4])
	case len(parts) == 6 && parts[3] == "projects" && parts[5] == "github-connect" && r.Method == http.MethodPost:
		s.startGitHubOAuth(w, r, actor, parts[4])
	case len(parts) == 8 && parts[3] == "projects" && parts[5] == "sources" && parts[6] == "github" && r.Method == http.MethodGet:
		s.getPinnedGitSource(w, r, actor, parts[4], parts[7])
	case len(parts) == 7 && parts[3] == "projects" && parts[5] == "github-pushes" && r.Method == http.MethodGet:
		s.getGitHubPushStatus(w, r, actor, parts[4], parts[6])
	case len(parts) == 6 && parts[3] == "projects" && parts[5] == "github-pushes" && r.Method == http.MethodGet:
		s.listGitHubPushes(w, r, actor, parts[4])
	case len(parts) == 6 && parts[3] == "projects" && parts[5] == "activity" && r.Method == http.MethodGet:
		s.listProjectActivity(w, r, actor, parts[4])
	case len(parts) == 6 && parts[3] == "projects" && parts[5] == "usage" && r.Method == http.MethodGet:
		s.getProjectUsage(w, r, actor, parts[4])
	case len(parts) == 6 && parts[3] == "projects" && parts[5] == "deployments" && r.Method == http.MethodPost:
		s.createStaticCandidate(w, r, actor, parts[4])
	case len(parts) == 6 && parts[3] == "projects" && parts[5] == "deployments" && r.Method == http.MethodGet:
		s.listDeployments(w, r, actor, parts[4])
	case len(parts) == 7 && parts[3] == "projects" && parts[5] == "deployments" && r.Method == http.MethodGet:
		s.getStaticCandidate(w, r, actor, parts[4], parts[6])
	case len(parts) == 8 && parts[3] == "projects" && parts[5] == "deployments" && parts[7] == "preview-exchanges" && r.Method == http.MethodPost:
		s.createPreviewExchange(w, r, actor, parts[4], parts[6])
	case len(parts) == 7 && parts[3] == "projects" && parts[5] == "aliases" && (parts[6] == "production:promote" || parts[6] == "production:rollback") && r.Method == http.MethodPost:
		s.promoteStaticProduction(w, r, actor, parts[4])
	case len(parts) == 7 && parts[3] == "projects" && parts[5] == "aliases" && parts[6] == "production" && r.Method == http.MethodGet:
		s.getProductionAlias(w, r, actor, parts[4])
	case len(parts) == 4 && parts[3] == "tokens" && r.Method == http.MethodPost:
		s.createToken(w, r, actor)
	case len(parts) == 5 && parts[3] == "tokens" && r.Method == http.MethodDelete:
		s.revokeToken(w, r, actor, parts[4])
	default:
		writeError(w, 404, "not_found")
	}
}

func (s *server) authenticate(w http.ResponseWriter, r *http.Request) (identity, bool) {
	var actor identity
	field := r.Header.Get("Authorization")
	if !strings.HasPrefix(field, "Bearer ewp_") || len(field) > 128 {
		writeError(w, 401, "unauthenticated")
		return actor, false
	}
	hash := sha256.Sum256([]byte(strings.TrimPrefix(field, "Bearer ")))
	var scoped sql.NullString
	err := s.db.QueryRow(r.Context(), `SELECT id, team_id, project_id, role, actions, expires_at
		FROM access_tokens WHERE token_hash=$1 AND revoked_at IS NULL AND expires_at>now()`, hash[:]).Scan(
		&actor.id, &actor.teamID, &scoped, &actor.role, &actor.actions, &actor.expiresAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 401, "unauthenticated")
		} else {
			writeError(w, 503, "database_unavailable")
		}
		return identity{}, false
	}
	actor.projectID = scoped.String
	return actor, true
}

func (s *server) createTeam(w http.ResponseWriter, r *http.Request) {
	provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if s.bootstrap == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(s.bootstrap)) != 1 {
		writeError(w, 401, "unauthenticated")
		return
	}
	key := idempotencyKey(r)
	if key == "" {
		writeError(w, 422, "idempotency_key_required")
		return
	}
	var input struct{ Slug, Name string }
	if !readJSON(w, r, &input) {
		return
	}
	if !slugPattern.MatchString(input.Slug) || strings.TrimSpace(input.Name) == "" || len(input.Name) > 120 {
		writeError(w, 422, "invalid_team")
		return
	}
	digest := payloadHash(input)
	ctx := r.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	defer tx.Rollback(ctx)
	// Concurrent deliveries of the same bootstrap request must observe the first result.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, key); err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	var previous []byte
	var teamID string
	err = tx.QueryRow(ctx, `SELECT request_hash, team_id FROM bootstrap_requests WHERE request_key=$1`, key).Scan(&previous, &teamID)
	if err == nil {
		if !sameDigest(previous, digest) {
			writeError(w, 409, "idempotency_conflict")
			return
		}
		writeJSON(w, 200, map[string]string{"id": teamID, "slug": input.Slug, "name": input.Name})
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 503, "database_unavailable")
		return
	}
	teamID, err = newID("t_")
	if err != nil {
		writeError(w, 503, "entropy_unavailable")
		return
	}
	tokenID, err := newID("key_")
	if err != nil {
		writeError(w, 503, "entropy_unavailable")
		return
	}
	secret, err := newToken()
	if err != nil {
		writeError(w, 503, "entropy_unavailable")
		return
	}
	hash := sha256.Sum256([]byte(secret))
	_, err = tx.Exec(ctx, `INSERT INTO teams(id,slug,name) VALUES($1,$2,$3)`, teamID, input.Slug, input.Name)
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO access_tokens(id,team_id,token_hash,role,expires_at) VALUES($1,$2,$3,'owner',$4)`, tokenID, teamID, hash[:], time.Now().Add(30*24*time.Hour))
	}
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO bootstrap_requests(request_key,request_hash,team_id) VALUES($1,$2,$3)`, key, digest[:], teamID)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO audit_events(team_id,action,subject_id) VALUES($1,'team.created',$2)`, teamID, teamID)
	}
	if err != nil {
		writeDatabaseError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeDatabaseError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 201, map[string]string{"id": teamID, "slug": input.Slug, "name": input.Name, "ownerToken": secret})
}

func (s *server) createProject(w http.ResponseWriter, r *http.Request, actor identity) {
	if actor.role != "owner" && actor.role != "developer" {
		writeError(w, 403, "forbidden")
		return
	}
	key := idempotencyKey(r)
	if key == "" {
		writeError(w, 422, "idempotency_key_required")
		return
	}
	var input struct{ Slug, Name string }
	if !readJSON(w, r, &input) {
		return
	}
	if !slugPattern.MatchString(input.Slug) || strings.TrimSpace(input.Name) == "" || len(input.Name) > 120 {
		writeError(w, 422, "invalid_project")
		return
	}
	digest := payloadHash(input)
	ctx := r.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	defer tx.Rollback(ctx)
	// Serialize requests within one team, including concurrent retries of the same key.
	if err = tx.QueryRow(ctx, `SELECT id FROM teams WHERE id=$1 FOR UPDATE`, actor.teamID).Scan(new(string)); err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	var previous []byte
	var p project
	err = tx.QueryRow(ctx, `SELECT request_hash, project_id FROM project_requests WHERE team_id=$1 AND request_key=$2`, actor.teamID, key).Scan(&previous, &p.ID)
	if err == nil {
		if !sameDigest(previous, digest) {
			writeError(w, 409, "idempotency_conflict")
			return
		}
		if err = tx.QueryRow(ctx, `SELECT id,team_id,slug,name,status FROM projects WHERE team_id=$1 AND id=$2`, actor.teamID, p.ID).Scan(&p.ID, &p.TeamID, &p.Slug, &p.Name, &p.Status); err != nil {
			writeError(w, 503, "database_unavailable")
			return
		}
		writeJSON(w, 200, p)
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 503, "database_unavailable")
		return
	}
	p.ID, err = newID("p_")
	if err != nil {
		writeError(w, 503, "entropy_unavailable")
		return
	}
	p.TeamID, p.Slug, p.Name, p.Status = actor.teamID, input.Slug, input.Name, "not_deployed"
	_, err = tx.Exec(ctx, `INSERT INTO projects(id,team_id,slug,name) VALUES($1,$2,$3,$4)`, p.ID, p.TeamID, p.Slug, p.Name)
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO project_requests(team_id,request_key,request_hash,project_id) VALUES($1,$2,$3,$4)`, p.TeamID, key, digest[:], p.ID)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO audit_events(team_id,actor_token_id,action,subject_id) VALUES($1,$2,'project.created',$3)`, p.TeamID, actor.id, p.ID)
	}
	if err != nil {
		writeDatabaseError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		writeDatabaseError(w, err)
		return
	}
	writeJSON(w, 201, p)
}

func (s *server) listProjects(w http.ResponseWriter, r *http.Request, actor identity) {
	if actor.role == "agent" && !hasAction(actor.actions, "projects:read") {
		writeError(w, 403, "forbidden")
		return
	}
	cursor := r.URL.Query().Get("cursor")
	if cursor != "" && !projectCursorPattern.MatchString(cursor) {
		writeError(w, 422, "invalid_cursor")
		return
	}
	rows, err := s.db.Query(r.Context(), `SELECT id,team_id,slug,name,status FROM projects WHERE team_id=$1 AND ($2='' OR id=$2) AND id>$3 ORDER BY id LIMIT 101`, actor.teamID, actor.projectID, cursor)
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	defer rows.Close()
	projects := make([]project, 0)
	for rows.Next() {
		var p project
		if err := rows.Scan(&p.ID, &p.TeamID, &p.Slug, &p.Name, &p.Status); err != nil {
			writeError(w, 503, "database_unavailable")
			return
		}
		projects = append(projects, p)
	}
	if rows.Err() != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	more := len(projects) > 100
	if more {
		projects = projects[:100]
	}
	result := map[string]any{"projects": projects, "hasMore": more}
	if more {
		result["nextCursor"] = projects[len(projects)-1].ID
	}
	writeJSON(w, 200, result)
}

func (s *server) getProject(w http.ResponseWriter, r *http.Request, actor identity, id string) {
	if actor.role == "agent" && (!hasAction(actor.actions, "projects:read") || actor.projectID != id) {
		writeError(w, 404, "not_found")
		return
	}
	var p project
	err := s.db.QueryRow(r.Context(), `SELECT id,team_id,slug,name,status FROM projects WHERE id=$1 AND team_id=$2`, id, actor.teamID).Scan(&p.ID, &p.TeamID, &p.Slug, &p.Name, &p.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found")
		return
	}
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	writeJSON(w, 200, p)
}

func (s *server) createToken(w http.ResponseWriter, r *http.Request, actor identity) {
	if actor.role != "owner" {
		writeError(w, 403, "forbidden")
		return
	}
	key := idempotencyKey(r)
	if key == "" {
		writeError(w, 422, "idempotency_key_required")
		return
	}
	var input struct {
		Role      string   `json:"role"`
		ProjectID string   `json:"projectId"`
		Actions   []string `json:"actions"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	if input.Role != "agent" && input.Role != "viewer" && input.Role != "developer" && input.Role != "owner" {
		writeError(w, 422, "invalid_role")
		return
	}
	if input.Role == "agent" {
		if input.ProjectID == "" || len(input.Actions) == 0 {
			writeError(w, 422, "agent_scope_required")
			return
		}
		for _, action := range input.Actions {
			if action != "projects:read" && action != "sources:read" && action != "sources:upload" && action != "deployments:create" && action != "deployments:read" && action != "previews:read" {
				writeError(w, 422, "invalid_action")
				return
			}
		}
	} else if input.ProjectID != "" || len(input.Actions) != 0 {
		writeError(w, 422, "invalid_scope")
		return
	} else {
		input.Actions = []string{} // pgx must send an empty array, not SQL NULL.
	}
	ctx := r.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	defer tx.Rollback(ctx)
	if err = tx.QueryRow(ctx, `SELECT id FROM teams WHERE id=$1 FOR UPDATE`, actor.teamID).Scan(new(string)); err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	digest := payloadHash(input)
	var previous []byte
	var previousID string
	err = tx.QueryRow(ctx, `SELECT request_hash,token_id FROM token_requests WHERE team_id=$1 AND request_key=$2`, actor.teamID, key).Scan(&previous, &previousID)
	if err == nil {
		if !sameDigest(previous, digest) {
			writeError(w, 409, "idempotency_conflict")
			return
		}
		writeJSON(w, 200, map[string]string{"id": previousID}) // A secret is returned only at creation.
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 503, "database_unavailable")
		return
	}
	if input.Role == "agent" {
		if err = tx.QueryRow(ctx, `SELECT id FROM projects WHERE team_id=$1 AND id=$2`, actor.teamID, input.ProjectID).Scan(new(string)); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, 404, "not_found")
			} else {
				writeError(w, 503, "database_unavailable")
			}
			return
		}
	}
	id, err := newID("key_")
	if err != nil {
		writeError(w, 503, "entropy_unavailable")
		return
	}
	secret, err := newToken()
	if err != nil {
		writeError(w, 503, "entropy_unavailable")
		return
	}
	hash := sha256.Sum256([]byte(secret))
	var projectID any
	if input.ProjectID != "" {
		projectID = input.ProjectID
	}
	expires := time.Now().Add(24 * time.Hour)
	if input.Role == "agent" {
		expires = time.Now().Add(time.Hour)
	}
	if input.Role == "owner" {
		expires = time.Now().Add(30 * 24 * time.Hour)
	}
	_, err = tx.Exec(ctx, `INSERT INTO access_tokens(id,team_id,project_id,token_hash,role,actions,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, id, actor.teamID, projectID, hash[:], input.Role, input.Actions, expires)
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO token_requests(team_id,request_key,request_hash,token_id) VALUES($1,$2,$3,$4)`, actor.teamID, key, digest[:], id)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO audit_events(team_id,actor_token_id,action,subject_id) VALUES($1,$2,'token.created',$3)`, actor.teamID, actor.id, id)
	}
	if err != nil {
		writeDatabaseError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		writeDatabaseError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 201, map[string]any{"id": id, "token": secret, "expiresAt": expires.UTC().Format(time.RFC3339)})
}

func (s *server) revokeToken(w http.ResponseWriter, r *http.Request, actor identity, id string) {
	if actor.role != "owner" {
		writeError(w, 403, "forbidden")
		return
	}
	if actor.id == id {
		writeError(w, 409, "cannot_revoke_current_token")
		return
	}
	ctx := r.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	defer tx.Rollback(ctx)
	command, err := tx.Exec(ctx, `UPDATE access_tokens SET revoked_at=COALESCE(revoked_at,now()) WHERE team_id=$1 AND id=$2`, actor.teamID, id)
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	if command.RowsAffected() == 0 {
		writeError(w, 404, "not_found")
		return
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events(team_id,actor_token_id,action,subject_id) VALUES($1,$2,'token.revoked',$3)`, actor.teamID, actor.id, id)
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	writeJSON(w, 200, map[string]string{"status": "revoked"})
}

func newID(prefix string) (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(bytes), nil
}

func newToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return "ewp_" + base64.RawURLEncoding.EncodeToString(bytes), nil
}

func payloadHash(value any) [32]byte {
	data, _ := json.Marshal(value)
	return sha256.Sum256(data)
}

func sameDigest(previous []byte, digest [32]byte) bool {
	return subtle.ConstantTimeCompare(previous, digest[:]) == 1
}

func hasAction(actions []string, action string) bool {
	for _, granted := range actions {
		if granted == action {
			return true
		}
	}
	return false
}

func idempotencyKey(r *http.Request) string {
	key := r.Header.Get("Idempotency-Key")
	if len(key) == 0 || len(key) > 128 || strings.TrimSpace(key) != key {
		return ""
	}
	return key
}

func readJSON(w http.ResponseWriter, r *http.Request, dest any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dest); err != nil {
		writeError(w, 422, "invalid_json")
		return false
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		writeError(w, 422, "invalid_json")
		return false
	}
	return true
}

func writeDatabaseError(w http.ResponseWriter, err error) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		writeError(w, 409, "conflict")
		return
	}
	writeError(w, 503, "database_unavailable")
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code}})
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
