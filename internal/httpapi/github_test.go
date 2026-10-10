package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"elastic-web-platform/internal/bundle"
	"elastic-web-platform/internal/routing"
	"elastic-web-platform/internal/source/github"
	"elastic-web-platform/internal/source/uploads"
)

func gitEvent(t *testing.T, h http.Handler, delivery string, body []byte, valid bool) (int, map[string]any) {
	t.Helper()
	mac := hmac.New(sha256.New, []byte("webhook-test-secret"))
	_, _ = mac.Write(body)
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if !valid {
		signature = "sha256=" + strings.Repeat("0", 64)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/hooks/github", bytes.NewReader(body))
	r.Header.Set("X-Hub-Signature-256", signature)
	r.Header.Set("X-GitHub-Event", "push")
	r.Header.Set("X-GitHub-Delivery", delivery)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("github status %d bad JSON: %s", w.Code, w.Body.String())
	}
	return w.Code, result
}

func githubTestClient(t *testing.T) (*github.Client, func()) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const treeSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	const commitSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	fileBytes := []byte("<h1>Git site</h1>")
	object := sha1.Sum(append([]byte(fmt.Sprintf("blob %d\x00", len(fileBytes))), fileBytes...))
	objectSHA := hex.EncodeToString(object[:])
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/installations/42/access_tokens":
			var input struct {
				RepositoryIDs []int64           `json:"repository_ids"`
				Permissions   map[string]string `json:"permissions"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			if len(input.RepositoryIDs) != 1 || input.RepositoryIDs[0] != 1001 || input.Permissions["contents"] != "read" {
				w.WriteHeader(403)
				return
			}
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"token":"scoped-test","repositories":[{"id":1001}]}`))
		case "/repositories/1001":
			if r.Header.Get("Authorization") != "Bearer scoped-test" {
				w.WriteHeader(403)
				return
			}
			_, _ = w.Write([]byte(`{"id":1001,"full_name":"org/site","default_branch":"main"}`))
		case "/repos/org/site/git/commits/" + commitSHA:
			if r.Header.Get("Authorization") != "Bearer scoped-test" {
				t.Error("commit read without single-repo token")
			}
			_, _ = w.Write([]byte(fmt.Sprintf(`{"sha":%q,"tree":{"sha":%q}}`, commitSHA, treeSHA)))
		case "/repos/org/site/git/trees/" + treeSHA:
			_, _ = w.Write([]byte(fmt.Sprintf(`{"sha":%q,"truncated":false,"tree":[{"path":"index.html","type":"blob","mode":"100644","sha":%q,"size":%d}]}`, treeSHA, objectSHA, len(fileBytes))))
		case "/repos/org/site/git/blobs/" + objectSHA:
			_, _ = w.Write([]byte(fmt.Sprintf(`{"sha":%q,"size":%d,"encoding":"base64","content":%q}`, objectSHA, len(fileBytes), base64.StdEncoding.EncodeToString(fileBytes))))
		case "/repos/org/site/git/ref/heads/main":
			_, _ = w.Write([]byte(fmt.Sprintf(`{"ref":"refs/heads/main","object":{"sha":%q}}`, commitSHA)))
		default:
			w.WriteHeader(404)
		}
	}))
	return github.NewClient(42, key, remote.URL, remote.Client()), remote.Close
}

func TestVerifyPinnedGitPushPersistsRealSourceButDoesNotDeploy(t *testing.T) {
	db := testDatabase(t)
	store := uploads.NewDiskStore(t.TempDir())
	client, closeRemote := githubTestClient(t)
	defer closeRemote()
	h := NewServer(db, "bootstrap-local-test", WithSourceStore(store), WithGitHubVerifier(client), WithGitHubWebhookSecret("webhook-test-secret"))
	a, owner := createTeam(t, h, "git-fetch-a")
	b, bOwner := createTeam(t, h, "git-fetch-b")
	_, project := request(t, h, http.MethodPost, "/v1/teams/"+a+"/projects", owner, "site-a", map[string]string{"slug": "site", "name": "Site"})
	_, other := request(t, h, http.MethodPost, "/v1/teams/"+b+"/projects", bOwner, "site-b", map[string]string{"slug": "site", "name": "Site"})
	pid := project["id"].(string)
	bind := "/v1/admin/teams/" + a + "/projects/" + pid + "/github-binding"
	if code, _ := request(t, h, http.MethodPost, bind, "bootstrap-local-test", "", map[string]int64{"installationId": 42, "repositoryId": 1001}); code != 200 {
		t.Fatalf("binding status: %d", code)
	}
	sha := strings.Repeat("a", 40)
	body := []byte(`{"installation":{"id":42},"repository":{"id":1001},"ref":"refs/heads/main","after":"` + sha + `"}`)
	if code, _ := gitEvent(t, h, "pinned-delivery", body, true); code != 202 {
		t.Fatalf("webhook status: %d", code)
	}
	verifyPath := "/v1/admin/teams/" + a + "/projects/" + pid + "/github-pushes/pinned-delivery:verify"
	if code, _ := request(t, h, http.MethodPost, verifyPath, owner, "", nil); code != 401 {
		t.Fatalf("owner verified Git without operator: %d", code)
	}
	code, verified := request(t, h, http.MethodPost, verifyPath, "bootstrap-local-test", "", nil)
	if code != 201 || verified["status"] != "verified_source" || verified["sourceId"] == "" {
		t.Fatalf("source verify: %d %v", code, verified)
	}
	code, again := request(t, h, http.MethodPost, verifyPath, "bootstrap-local-test", "", nil)
	if code != 200 || again["sourceId"] != verified["sourceId"] {
		t.Fatalf("source verify retry: %d %v", code, again)
	}
	getPath := "/v1/teams/" + a + "/projects/" + pid + "/sources/github/" + verified["sourceId"].(string)
	if code, meta := request(t, h, http.MethodGet, getPath, owner, "", nil); code != 200 || meta["archiveSha256"] != verified["archiveSha256"] {
		t.Fatalf("source read: %d %v", code, meta)
	}
	if code, _ := request(t, h, http.MethodGet, "/v1/teams/"+b+"/projects/"+other["id"].(string)+"/sources/github/"+verified["sourceId"].(string), bOwner, "", nil); code != 404 {
		t.Fatalf("cross-team Git source read: %d", code)
	}
	data, err := store.Get(context.Background(), a, pid, verified["archiveSha256"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := uploads.Validate(data); err != nil {
		t.Fatalf("stored Git archive: %v", err)
	}
	staticBundle, err := bundle.CaptureStaticSource(a, pid, data)
	if err != nil {
		t.Fatalf("static HTML copy from pinned Git bytes: %v", err)
	}
	routes := routing.NewRegistry()
	deploymentID, err := routes.Register(staticBundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := routes.BindFixed("git-preview.test", deploymentID); err != nil {
		t.Fatal(err)
	}
	if _, err := routes.SwapAlias("git-production.test", a, pid, 0, deploymentID, false); err != nil {
		t.Fatal(err)
	}
	requestSite := httptest.NewRequest(http.MethodGet, "https://git-production.test/", nil)
	responseSite := httptest.NewRecorder()
	routing.NewGateway(routes, nil).ServeHTTP(responseSite, requestSite)
	if responseSite.Code != 200 || responseSite.Body.String() != "<h1>Git site</h1>" {
		t.Fatalf("verified Git bytes did not reach static route: %d %q", responseSite.Code, responseSite.Body.String())
	}
	preview := httptest.NewRecorder()
	routing.NewGateway(routes, nil).ServeHTTP(preview, httptest.NewRequest(http.MethodGet, "https://git-preview.test/", nil))
	if preview.Code != 401 {
		t.Fatalf("unprotected Git preview: %d", preview.Code)
	}
	if code, p := request(t, h, http.MethodGet, "/v1/teams/"+a+"/projects/"+pid, owner, "", nil); code != 200 || p["status"] != "not_deployed" {
		t.Fatalf("source verification activated site: %d %v", code, p)
	}
}

func TestGithubBindingIsOperatorAssignedAndProviderVerified(t *testing.T) {
	db := testDatabase(t)
	h := NewServer(db, "bootstrap-local-test")
	a, owner := createTeam(t, h, "git-team")
	_, p := request(t, h, http.MethodPost, "/v1/teams/"+a+"/projects", owner, "git-project", map[string]string{"slug": "site", "name": "Site"})
	pid := p["id"].(string)
	path := "/v1/admin/teams/" + a + "/projects/" + pid + "/github-binding"
	if code, _ := request(t, h, http.MethodPost, path, owner, "", map[string]int64{"installationId": 42, "repositoryId": 1001}); code != 401 {
		t.Fatalf("owner assigned arbitrary repo: %d", code)
	}
	code, pending := request(t, h, http.MethodPost, path, "bootstrap-local-test", "", map[string]int64{"installationId": 42, "repositoryId": 1001})
	if code != 202 || pending["status"] != "pending" {
		t.Fatalf("unconfigured binding: %d %v", code, pending)
	}
	client, closeRemote := githubTestClient(t)
	defer closeRemote()
	h = NewServer(db, "bootstrap-local-test", WithGitHubVerifier(client), WithGitHubWebhookSecret("webhook-test-secret"))
	code, verified := request(t, h, http.MethodPost, path, "bootstrap-local-test", "", map[string]int64{"installationId": 42, "repositoryId": 1001})
	if code != 200 || verified["status"] != "verified" || verified["fullName"] != "org/site" {
		t.Fatalf("scoped provider verification: %d %v", code, verified)
	}
	if code, _ := request(t, h, http.MethodPost, path, "bootstrap-local-test", "", map[string]int64{"installationId": 42, "repositoryId": 1002}); code != 409 {
		t.Fatalf("repo changed under binding: %d", code)
	}
	if code, got := request(t, h, http.MethodGet, "/v1/teams/"+a+"/projects/"+pid+"/github-binding", owner, "", nil); code != 200 || got["status"] != "verified" {
		t.Fatalf("owner read binding: %d %v", code, got)
	}
}

func TestGitHubWebhookRequiresSignedVerifiedBindingAndDeduplicates(t *testing.T) {
	db := testDatabase(t)
	client, closeRemote := githubTestClient(t)
	defer closeRemote()
	h := NewServer(db, "bootstrap-local-test", WithGitHubVerifier(client), WithGitHubWebhookSecret("webhook-test-secret"))
	a, owner := createTeam(t, h, "webhook-team")
	_, p := request(t, h, http.MethodPost, "/v1/teams/"+a+"/projects", owner, "git-project", map[string]string{"slug": "site", "name": "Site"})
	pid := p["id"].(string)
	path := "/v1/admin/teams/" + a + "/projects/" + pid + "/github-binding"
	if code, _ := request(t, h, http.MethodPost, path, "bootstrap-local-test", "", map[string]int64{"installationId": 42, "repositoryId": 1001}); code != 200 {
		t.Fatalf("bind: %d", code)
	}
	sha := strings.Repeat("a", 40)
	body := []byte(`{"installation":{"id":42},"repository":{"id":1001},"ref":"refs/heads/feature/hero","after":"` + sha + `","deleted":false}`)
	if code, _ := gitEvent(t, h, "delivery-a", body, false); code != 401 {
		t.Fatalf("invalid signature: %d", code)
	}
	code, accepted := gitEvent(t, h, "delivery-a", body, true)
	if code != 202 || accepted["status"] != "pending_commit_verification" {
		t.Fatalf("signed push: %d %v", code, accepted)
	}
	statusPath := "/v1/teams/" + a + "/projects/" + pid + "/github-pushes/delivery-a"
	if code, got := request(t, h, http.MethodGet, statusPath, owner, "", nil); code != 200 || got["status"] != "pending_commit_verification" || got["commitSha"] != sha || got["origin"] != "webhook" {
		t.Fatalf("Git push status: %d %v", code, got)
	}
	code, replay := gitEvent(t, h, "delivery-a", body, true)
	if code != 200 || replay["status"] != "pending_commit_verification" {
		t.Fatalf("delivery replay: %d %v", code, replay)
	}
	modified := []byte(strings.Replace(string(body), "feature/hero", "feature/other", 1))
	if code, _ := gitEvent(t, h, "delivery-a", modified, true); code != 409 {
		t.Fatalf("same delivery altered payload: %d", code)
	}
	deleted := []byte(strings.Replace(string(body), `"deleted":false`, `"deleted":true`, 1))
	if code, result := gitEvent(t, h, "delivery-deleted", deleted, true); code != 202 || result["status"] != "ignored_deleted" {
		t.Fatalf("branch deletion retried as invalid push: %d %v", code, result)
	}
	emptyBranch := []byte(strings.Replace(string(body), "refs/heads/feature/hero", "refs/heads/", 1))
	if code, _ := gitEvent(t, h, "delivery-empty-ref", emptyBranch, true); code != 422 {
		t.Fatalf("empty branch accepted: %d", code)
	}
	wrongRepo := []byte(strings.Replace(string(body), `"id":1001`, `"id":1002`, 1))
	if code, _ := gitEvent(t, h, "delivery-b", wrongRepo, true); code != 202 {
		t.Fatalf("unbound repo: %d", code)
	}
	var pushes int
	if err := db.QueryRow(context.Background(), "SELECT count(*) FROM github_pushes WHERE project_id=$1", pid).Scan(&pushes); err != nil || pushes != 1 {
		t.Fatalf("accepted %d pushes: %v", pushes, err)
	}
	if code, got := request(t, h, http.MethodGet, "/v1/teams/"+a+"/projects/"+pid, owner, "", nil); code != 200 || got["status"] != "not_deployed" {
		t.Fatalf("webhook activated project: %d %v", code, got)
	}
}
