package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"elastic-web-platform/internal/source/github"
	"elastic-web-platform/internal/source/githubactions"
	"elastic-web-platform/internal/source/uploads"
)

type pilotVerifier struct{ claim githubactions.Claims }

func (v pilotVerifier) Verify(_ context.Context, token string, policy githubactions.Pilot) (githubactions.Claims, error) {
	if token != "signed-oidc" || v.claim.RepositoryID != policy.RepositoryID || v.claim.Repository != policy.Repository {
		return githubactions.Claims{}, errors.New("scope mismatch")
	}
	return v.claim, nil
}

type pilotPublicGit struct {
	pinned github.PinnedSource
	head   string
}

func (g pilotPublicGit) CurrentBranchHead(_ context.Context, installation, id int64, branch string) (string, error) {
	if id != g.pinned.Repository.ID || branch != "main" {
		return "", errors.New("unexpected Git branch")
	}
	return g.head, nil
}

func (g pilotPublicGit) FetchPinned(_ context.Context, installation, id int64, commit string) (github.PinnedSource, error) {
	if id != g.pinned.Repository.ID || commit != g.pinned.CommitSHA {
		return github.PinnedSource{}, errors.New("unexpected source")
	}
	return g.pinned, nil
}

func postActionsPilot(t *testing.T, h http.Handler, repositoryID string) (int, map[string]any) {
	t.Helper()
	data, _ := json.Marshal(map[string]string{"repositoryId": repositoryID})
	req := httptest.NewRequest(http.MethodPost, "/v1/hooks/github-actions", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer signed-oidc")
	req.Header.Set("X-EWP-GITHUB-TOKEN", "repo-scoped-token-123456789")
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, req)
	var value map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	return recorder.Code, value
}

func TestOIDCPilotRecordsOnlyAVerifiedFixedBuildSourceAndRejectsReplay(t *testing.T) {
	db := testDatabase(t)
	ctx := context.Background()
	team, project := "t_f9cf963718a72612c48940af8a8296e4", "p_34401f5c05af1079010cb95ee3ec1275"
	if _, err := db.Exec(ctx, `INSERT INTO teams(id,slug,name) VALUES($1,'pilot-a','Pilot A')`, team); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO projects(id,team_id,slug,name) VALUES($2,$1,'site','Site')`, team, project); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO github_bindings(team_id,project_id,installation_id,repository_id,full_name,default_branch,status)
		VALUES($1,$2,167883648,1404606559,'usiboy/testA','main','verified')`, team, project); err != nil {
		t.Fatal(err)
	}
	archive := deploymentSource(t, map[string]string{"package.json": `{"scripts":{"build":"vite build"},"dependencies":{"vue":"3.5.0"},"devDependencies":{"vite":"6.0.0"}}`,
		"package-lock.json": `{"lockfileVersion":3,"packages":{}}`, "index.html": "<div id=app></div>"})
	hash := sha256.Sum256(archive)
	sha := strings.Repeat("a", 40)
	git := pilotPublicGit{head: sha, pinned: github.PinnedSource{Archive: archive, ArchiveSHA256: hex.EncodeToString(hash[:]), CommitSHA: sha, TreeSHA: strings.Repeat("b", 40),
		Repository: github.Repository{ID: 1404606559, FullName: "usiboy/testA", DefaultBranch: "main"}}}
	claim := githubactions.Claims{RepositoryID: "1404606559", Repository: "usiboy/testA", SHA: sha, RunID: "331", RunAttempt: "1", JTI: "unique-test-jti"}
	store := uploads.NewDiskStore(t.TempDir())
	factory := func(installation, repository int64, token string) (GitHubActionsSource, error) {
		if installation != 167883648 || repository != 1404606559 || token != "repo-scoped-token-123456789" {
			return nil, errors.New("wrong scoped identity")
		}
		return git, nil
	}
	h := NewServer(db, "bootstrap-local-test", WithSourceStore(store), WithGitHubActionsPilot(pilotVerifier{claim}, factory))
	code, result := postActionsPilot(t, h, "1404606559")
	if code != 202 || result["status"] != "awaiting_isolated_build" || result["deliveryId"] == "" {
		t.Fatalf("valid OIDC source was rejected: %d %v", code, result)
	}
	code, _ = postActionsPilot(t, h, "1404606559")
	if code != 409 {
		t.Fatalf("replayed Actions run minted another source: %d", code)
	}
	var status, source string
	if err := db.QueryRow(ctx, `SELECT p.status,s.archive_sha256 FROM github_pushes p JOIN github_verified_sources s ON s.team_id=p.team_id AND s.project_id=p.project_id AND s.delivery_id=p.delivery_id
		WHERE p.delivery_id=$1 AND p.team_id=$2 AND p.project_id=$3`, result["deliveryId"], team, project).Scan(&status, &source); err != nil || status != "awaiting_isolated_build" || source != git.pinned.ArchiveSHA256 {
		t.Fatalf("Git source was not pinned: %s %s %v", status, source, err)
	}
	var deployments int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM site_deployments`).Scan(&deployments); err != nil || deployments != 0 {
		t.Fatalf("unbuilt Git input became Preview: %d %v", deployments, err)
	}
	if code, _ := postActionsPilot(t, h, "1404606859"); code != 401 {
		t.Fatalf("A identity crossed into B: %d", code)
	}
	if _, err := db.Exec(ctx, `UPDATE github_bindings SET status='revoked' WHERE team_id=$1 AND project_id=$2`, team, project); err != nil {
		t.Fatal(err)
	}
	claim.RunID, claim.JTI = "332", "new-test-jti"
	h = NewServer(db, "bootstrap-local-test", WithSourceStore(store), WithGitHubActionsPilot(pilotVerifier{claim}, factory))
	if code, _ := postActionsPilot(t, h, "1404606559"); code != 409 {
		t.Fatalf("revoked binding accepted OIDC delivery: %d", code)
	}
}
