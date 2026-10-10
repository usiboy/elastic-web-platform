package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"elastic-web-platform/internal/framework"
	"elastic-web-platform/internal/source/githubactions"
	"elastic-web-platform/internal/source/uploads"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type actionsProject struct {
	team, project string
	repoID        int64
	policy        githubactions.Pilot
}

func actionsPilotFor(repositoryID string) (actionsProject, bool) {
	switch repositoryID {
	case "1404606559":
		return actionsProject{team: "t_f9cf963718a72612c48940af8a8296e4", project: "p_34401f5c05af1079010cb95ee3ec1275", repoID: 1404606559,
			policy: githubactions.Pilot{RepositoryID: repositoryID, Repository: "usiboy/testA", OwnerID: "1673411",
				WorkflowRef: "usiboy/testA/.github/workflows/ewp-preview.yml@refs/heads/main", Audience: "ewp-ecs-testA"}}, true
	case "1404606859":
		return actionsProject{team: "t_eee8046ca49c271f5bea06acf285be66", project: "p_e5123bae12388454bb82fc1590750ab9", repoID: 1404606859,
			policy: githubactions.Pilot{RepositoryID: repositoryID, Repository: "usiboy/testB", OwnerID: "1673411",
				WorkflowRef: "usiboy/testB/.github/workflows/ewp-preview.yml@refs/heads/main", Audience: "ewp-ecs-testB"}}, true
	}
	return actionsProject{}, false
}

func (s *server) githubActionsPilot(w http.ResponseWriter, r *http.Request) {
	if s.actionsVerifier == nil || s.actionsGit == nil || s.sources == nil {
		writeError(w, 503, "actions_pilot_unavailable")
		return
	}
	// Parse the unverified payload only to select one of two fixed trust
	// policies. No untrusted claim may influence a DB write or Git fetch until
	// the exact JWKS signature and all audience/repository/ref claims pass.
	provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if provided == r.Header.Get("Authorization") || len(provided) > 8192 {
		writeError(w, 401, "unauthenticated")
		return
	}
	var hint struct {
		RepositoryID string `json:"repositoryId"`
	}
	if !readJSON(w, r, &hint) {
		return
	}
	project, ok := actionsPilotFor(hint.RepositoryID)
	if !ok {
		writeError(w, 403, "repository_not_admitted")
		return
	}
	claims, err := s.actionsVerifier.Verify(r.Context(), provided, project.policy)
	if err != nil {
		log.Printf("OIDC pilot identity rejected: %v", err) // Verifier errors contain no JWT or claims.
		writeError(w, 401, "invalid_actions_identity")
		return
	}
	scopedToken := r.Header.Get("X-EWP-GITHUB-TOKEN")
	if len(scopedToken) < 16 || len(scopedToken) > 1024 || strings.TrimSpace(scopedToken) != scopedToken {
		writeError(w, 401, "actions_repo_token_required")
		return
	}
	var installationID int64
	err = s.db.QueryRow(r.Context(), `SELECT installation_id FROM github_bindings WHERE team_id=$1 AND project_id=$2 AND repository_id=$3 AND full_name=$4 AND default_branch='main' AND status='verified'`,
		project.team, project.project, project.repoID, project.policy.Repository).Scan(&installationID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 409, "binding_revoked")
		return
	}
	if err != nil {
		writeError(w, 503, "database_unavailable")
		return
	}
	git, err := s.actionsGit(installationID, project.repoID, scopedToken)
	if err != nil {
		writeError(w, 401, "actions_repo_token_invalid")
		return
	}
	head, err := git.CurrentBranchHead(r.Context(), installationID, project.repoID, "main")
	if err != nil {
		writeError(w, 503, "github_source_unavailable")
		return
	}
	if head != claims.SHA {
		writeError(w, 409, "superseded_commit")
		return
	}
	pinned, err := git.FetchPinned(r.Context(), installationID, project.repoID, claims.SHA)
	if err != nil || pinned.Repository.ID != project.repoID || pinned.Repository.FullName != project.policy.Repository || pinned.CommitSHA != claims.SHA {
		writeError(w, 503, "github_source_unavailable")
		return
	}
	manifest, err := uploads.Validate(pinned.Archive)
	if err != nil || pinned.ArchiveSHA256 != manifest.Digest {
		writeError(w, 422, "invalid_source_archive")
		return
	}
	preset, err := framework.Detect(pinned.Archive)
	if err != nil || !preset.RequiresBuild || (preset.Name != "vite-vue" && preset.Name != "vite-react") {
		writeError(w, 422, "unsupported_isolated_build")
		return
	}
	if err := s.sources.Put(r.Context(), project.team, project.project, manifest.Digest, pinned.Archive); err != nil {
		writeError(w, 503, "source_store_unavailable")
		return
	}
	result, err := s.recordActionsSource(r.Context(), claims, project, pinned.TreeSHA, manifest.Digest, manifest.Size)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 409, "binding_revoked")
		} else if errors.Is(err, errActionsReplay) {
			writeError(w, 409, "actions_run_already_recorded")
		} else {
			writeError(w, 503, "database_unavailable")
		}
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 202, map[string]string{"status": "awaiting_isolated_build", "deliveryId": result})
}

var errActionsReplay = errors.New("actions job has already been received")

func (s *server) recordActionsSource(ctx context.Context, claim githubactions.Claims, pilot actionsProject, tree, digest string, size int) (string, error) {
	runID, _ := strconv.ParseInt(claim.RunID, 10, 64) // validated by OIDC verifier
	attempt, _ := strconv.Atoi(claim.RunAttempt)
	if runID <= 0 || attempt <= 0 || claim.RepositoryID != pilot.policy.RepositoryID {
		return "", errors.New("OIDC run identity invalid")
	}
	key := sha256.Sum256([]byte(claim.RepositoryID + ":" + claim.RunID + ":" + claim.RunAttempt))
	delivery := "oidc-" + hex.EncodeToString(key[:16])
	nonce := claim.JTI
	if nonce == "" {
		// GitHub's discovery document does not promise a jti claim. A signed
		// repo/run/attempt tuple is still unique and rejected on replay by PK.
		nonce = "run:" + claim.RepositoryID + ":" + claim.RunID + ":" + claim.RunAttempt
	}
	jti := sha256.Sum256([]byte(nonce))
	sourceID, err := newID("gsrc_")
	if err != nil {
		return "", err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var installationID int64
	err = tx.QueryRow(ctx, `SELECT installation_id FROM github_bindings WHERE team_id=$1 AND project_id=$2 AND repository_id=$3 AND full_name=$4 AND default_branch='main' AND status='verified' FOR SHARE`,
		pilot.team, pilot.project, pilot.repoID, pilot.policy.Repository).Scan(&installationID)
	if err != nil {
		return "", err
	}
	var existing string
	err = tx.QueryRow(ctx, `SELECT delivery_id FROM github_actions_receipts WHERE repository_id=$1 AND run_id=$2 AND run_attempt=$3`, pilot.repoID, runID, attempt).Scan(&existing)
	if err == nil {
		return "", errActionsReplay
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO github_deliveries(delivery_id,payload_hash,installation_id,repository_id,ref,commit_sha,status,origin)
		VALUES($1,$2,$3,$4,'refs/heads/main',$5,'pending_commit_verification','actions_oidc')`, delivery, jti[:], installationID, pilot.repoID, claim.SHA)
	if err != nil {
		return "", actionsInsertError(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO github_actions_receipts(repository_id,run_id,run_attempt,jti_sha256,delivery_id) VALUES($1,$2,$3,$4,$5)`, pilot.repoID, runID, attempt, jti[:], delivery)
	if err != nil {
		return "", actionsInsertError(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO github_pushes(delivery_id,team_id,project_id,commit_sha,ref,status)
		VALUES($1,$2,$3,$4,'refs/heads/main','awaiting_isolated_build')`, delivery, pilot.team, pilot.project, claim.SHA)
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO github_verified_sources(id,team_id,project_id,delivery_id,commit_sha,tree_sha,archive_sha256,size_bytes)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, sourceID, pilot.team, pilot.project, delivery, claim.SHA, tree, digest, size)
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events(team_id,action,subject_id) VALUES($1,'github.branch_polled',$2)`, pilot.team, delivery)
	if err != nil {
		return "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	return delivery, nil
}

func actionsInsertError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return errActionsReplay
	}
	return err
}
