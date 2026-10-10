package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"elastic-web-platform/internal/source/github"
)

func oauthTestClient(t *testing.T, canRead bool) (*github.OAuthClient, func(), *atomic.Int32) {
	t.Helper()
	count := new(atomic.Int32)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/oauth/access_token":
			count.Add(1)
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			if r.PostForm.Get("code") != "approved-code" || r.PostForm.Get("repository_id") != "1001" || r.PostForm.Get("code_verifier") == "" {
				t.Error("OAuth exchange was not scoped")
			}
			_, _ = w.Write([]byte(`{"access_token":"ghu_user_short_lived","token_type":"bearer"}`))
		case "/user/installations/42/repositories":
			if r.Header.Get("Authorization") != "Bearer ghu_user_short_lived" {
				t.Error("missing user identity")
			}
			permission := "true"
			if !canRead {
				permission = "false"
			}
			_, _ = w.Write([]byte(`{"total_count":1,"repositories":[{"id":1001,"full_name":"org/site","default_branch":"main","permissions":{"pull":` + permission + `}}]}`))
		case "/user":
			if r.Header.Get("Authorization") != "Bearer ghu_user_short_lived" {
				t.Error("missing scoped user identity")
			}
			_, _ = w.Write([]byte(`{"id":77,"login":"owner"}`))
		default:
			t.Errorf("unexpected OAuth endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	client := github.NewOAuthClient("Iv1.test", "short-secret", "https://control.test/v1/oauth/github/callback", []byte(strings.Repeat("K", 32)), remote.URL, remote.URL, remote.URL, remote.Client())
	return client, remote.Close, count
}

func oauthCallback(t *testing.T, h http.Handler, state, code string) (int, map[string]any) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "https://control.test/v1/oauth/github/callback?state="+url.QueryEscape(state)+"&code="+url.QueryEscape(code), nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("callback %d returned non-JSON: %s", w.Code, w.Body.String())
	}
	return w.Code, result
}

func signedGitHubEvent(t *testing.T, h http.Handler, event, delivery string, body []byte) (int, map[string]any) {
	t.Helper()
	mac := hmac.New(sha256.New, []byte("webhook-test-secret"))
	_, _ = mac.Write(body)
	r := httptest.NewRequest(http.MethodPost, "/v1/hooks/github", strings.NewReader(string(body)))
	r.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	r.Header.Set("X-GitHub-Event", event)
	r.Header.Set("X-GitHub-Delivery", delivery)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return w.Code, result
}

func TestOwnerOAuthConnectRequiresBothUserAndAppRepositoryAccess(t *testing.T) {
	db := testDatabase(t)
	app, closeApp := githubTestClient(t)
	defer closeApp()
	oauth, closeOAuth, count := oauthTestClient(t, true)
	defer closeOAuth()
	h := NewServer(db, "bootstrap-local-test", WithGitHubVerifier(app), WithGitHubOAuth(oauth), WithGitHubWebhookSecret("webhook-test-secret"))
	a, owner := createTeam(t, h, "oauth-team-a")
	_, other := createTeam(t, h, "oauth-team-b")
	path := "/v1/teams/" + a + "/projects"
	_, p := request(t, h, http.MethodPost, path, owner, "project", map[string]string{"slug": "site", "name": "Site"})
	projectID := p["id"].(string)
	connectPath := path + "/" + projectID + "/github-connect"
	if code, _ := request(t, h, http.MethodPost, connectPath, other, "", map[string]int64{"installationId": 42, "repositoryId": 1001}); code != 404 {
		t.Fatalf("other Team started binding: %d", code)
	}
	_, viewer := request(t, h, http.MethodPost, "/v1/teams/"+a+"/tokens", owner, "viewer", map[string]string{"role": "viewer"})
	if code, _ := request(t, h, http.MethodPost, connectPath, viewer["token"].(string), "", map[string]int64{"installationId": 42, "repositoryId": 1001}); code != 403 {
		t.Fatalf("viewer started binding: %d", code)
	}
	code, start := request(t, h, http.MethodPost, connectPath, owner, "", map[string]int64{"installationId": 42, "repositoryId": 1001})
	if code != 201 || start["authorizationUrl"] == nil {
		t.Fatalf("start GitHub authorization: %d %v", code, start)
	}
	authURL, err := url.Parse(start["authorizationUrl"].(string))
	if err != nil {
		t.Fatal(err)
	}
	state := authURL.Query().Get("state")
	if !strings.HasPrefix(state, "ghs_") || authURL.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("missing OAuth state/PKCE: %v", authURL)
	}
	hash := sha256.Sum256([]byte(state))
	var stored int
	if err := db.QueryRow(context.Background(), `SELECT count(*) FROM github_oauth_states WHERE state_hash=$1 AND expires_at>now()`, hash[:]).Scan(&stored); err != nil || stored != 1 {
		t.Fatalf("OAuth state not hash-scoped: %d %v", stored, err)
	}
	code, verified := oauthCallback(t, h, state, "approved-code")
	if code != 200 || verified["status"] != "verified" || verified["fullName"] != "org/site" || verified["repositoryId"] != float64(1001) {
		t.Fatalf("owner binding result: %d %v", code, verified)
	}
	if code, _ := oauthCallback(t, h, state, "approved-code"); code != 409 {
		t.Fatalf("one-time OAuth state replay: %d", code)
	}
	if count.Load() != 1 {
		t.Fatalf("provider token exchanged %d times", count.Load())
	}
	if code, result := request(t, h, http.MethodGet, path+"/"+projectID+"/github-binding", owner, "", nil); code != 200 || result["status"] != "verified" {
		t.Fatalf("binding not durable: %d %v", code, result)
	}
	var authorizedID int64
	if err := db.QueryRow(context.Background(), `SELECT authorized_github_user_id FROM github_bindings WHERE team_id=$1 AND project_id=$2`, a, projectID).Scan(&authorizedID); err != nil || authorizedID != 77 {
		t.Fatalf("cannot attribute repo grant to GitHub user: %d %v", authorizedID, err)
	}
	push := []byte(`{"installation":{"id":42},"repository":{"id":1001},"ref":"refs/heads/main","after":"` + strings.Repeat("a", 40) + `"}`)
	if code, result := gitEvent(t, h, "oauth-owner-push", push, true); code != 202 || result["status"] != "pending_commit_verification" {
		t.Fatalf("self-service binding not usable by verified webhook: %d %v", code, result)
	}
	revoke := []byte(`{"action":"revoked","sender":{"id":77}}`)
	if code, result := signedGitHubEvent(t, h, "github_app_authorization", "revoke-user-1", revoke); code != 202 || result["status"] != "binding_revoked" {
		t.Fatalf("user revoke ignored: %d %v", code, result)
	}
	if code, result := request(t, h, http.MethodGet, path+"/"+projectID+"/github-binding", owner, "", nil); code != 200 || result["status"] != "revoked" {
		t.Fatalf("revoked repo remained verified: %d %v", code, result)
	}
	if code, result := signedGitHubEvent(t, h, "github_app_authorization", "revoke-user-1", revoke); code != 200 || result["status"] != "binding_revoked" {
		t.Fatalf("revoke replay: %d %v", code, result)
	}
	if code, _ := request(t, h, http.MethodPost, "/v1/admin/teams/"+a+"/projects/"+projectID+"/github-binding", "bootstrap-local-test", "", map[string]int64{"installationId": 42, "repositoryId": 1001}); code != 409 {
		t.Fatalf("operator bypassed revoked user proof: %d", code)
	}
	if code, result := gitEvent(t, h, "post-revoke-push", push, true); code != 202 || result["status"] != "ignored_unbound" {
		t.Fatalf("revoked repo still queued: %d %v", code, result)
	}
	_, renew := request(t, h, http.MethodPost, connectPath, owner, "", map[string]int64{"installationId": 42, "repositoryId": 1001})
	renewURL, err := url.Parse(renew["authorizationUrl"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if code, result := oauthCallback(t, h, renewURL.Query().Get("state"), "approved-code"); code != 200 || result["status"] != "verified" {
		t.Fatalf("Owner could not reauthorize after revocation: %d %v", code, result)
	}
	if code, result := gitEvent(t, h, "after-renewal-push", push, true); code != 202 || result["status"] != "pending_commit_verification" {
		t.Fatalf("renewed binding not usable: %d %v", code, result)
	}
}

func TestOAuthDeniesUnauthorizedRepositoryAndRevokedOwner(t *testing.T) {
	db := testDatabase(t)
	app, closeApp := githubTestClient(t)
	defer closeApp()
	oauth, closeOAuth, _ := oauthTestClient(t, false)
	defer closeOAuth()
	h := NewServer(db, "bootstrap-local-test", WithGitHubVerifier(app), WithGitHubOAuth(oauth))
	a, owner := createTeam(t, h, "oauth-denied")
	path := "/v1/teams/" + a + "/projects"
	_, p := request(t, h, http.MethodPost, path, owner, "project", map[string]string{"slug": "site", "name": "Site"})
	projectID := p["id"].(string)
	connect := path + "/" + projectID + "/github-connect"
	_, start := request(t, h, http.MethodPost, connect, owner, "", map[string]int64{"installationId": 42, "repositoryId": 1001})
	urlValue, _ := url.Parse(start["authorizationUrl"].(string))
	if code, _ := oauthCallback(t, h, urlValue.Query().Get("state"), "approved-code"); code != 403 {
		t.Fatalf("user without repo pull bound project: %d", code)
	}
	var bindings int
	if err := db.QueryRow(context.Background(), `SELECT count(*) FROM github_bindings`).Scan(&bindings); err != nil || bindings != 0 {
		t.Fatalf("unauthorized repo persisted: %d %v", bindings, err)
	}
	_, second := request(t, h, http.MethodPost, connect, owner, "", map[string]int64{"installationId": 42, "repositoryId": 1001})
	state, _ := url.Parse(second["authorizationUrl"].(string))
	ownerHash := sha256.Sum256([]byte(owner))
	if _, err := db.Exec(context.Background(), `UPDATE access_tokens SET revoked_at=now() WHERE token_hash=$1`, ownerHash[:]); err != nil {
		t.Fatal(err)
	}
	if code, _ := oauthCallback(t, h, state.Query().Get("state"), "approved-code"); code != 401 {
		t.Fatalf("revoked Owner completed OAuth: %d", code)
	}
	if code, _ := oauthCallback(t, h, "ghs_unknown", "approved-code"); code != 401 {
		t.Fatalf("unknown state accepted: %d", code)
	}
}

func TestOAuthCallbackRejectsExpiredStateBeforeExchangingCode(t *testing.T) {
	db := testDatabase(t)
	app, closeApp := githubTestClient(t)
	defer closeApp()
	oauth, closeOAuth, count := oauthTestClient(t, true)
	defer closeOAuth()
	h := NewServer(db, "bootstrap-local-test", WithGitHubVerifier(app), WithGitHubOAuth(oauth))
	a, owner := createTeam(t, h, "oauth-expired")
	path := "/v1/teams/" + a + "/projects"
	_, p := request(t, h, http.MethodPost, path, owner, "project", map[string]string{"slug": "site", "name": "Site"})
	_, start := request(t, h, http.MethodPost, path+"/"+p["id"].(string)+"/github-connect", owner, "", map[string]int64{"installationId": 42, "repositoryId": 1001})
	link, err := url.Parse(start["authorizationUrl"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(context.Background(), `UPDATE github_oauth_states SET expires_at=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if code, _ := oauthCallback(t, h, link.Query().Get("state"), "approved-code"); code != 401 {
		t.Fatalf("expired state accepted: %d", code)
	}
	if count.Load() != 0 {
		t.Fatal("provider saw code for expired state")
	}
	request := httptest.NewRequest(http.MethodGet, "http://control.test/v1/oauth/github/callback?state="+url.QueryEscape(link.Query().Get("state"))+"&code=approved-code", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request)
	if w.Code != 403 {
		t.Fatalf("plaintext callback accepted: %d", w.Code)
	}
}

func TestGitHubInstallationDeletionRevokesOperatorBindingAndPendingPush(t *testing.T) {
	db := testDatabase(t)
	app, closeApp := githubTestClient(t)
	defer closeApp()
	h := NewServer(db, "bootstrap-local-test", WithGitHubVerifier(app), WithGitHubWebhookSecret("webhook-test-secret"))
	a, owner := createTeam(t, h, "installation-removed")
	path := "/v1/teams/" + a + "/projects"
	_, p := request(t, h, http.MethodPost, path, owner, "project", map[string]string{"slug": "site", "name": "Site"})
	id := p["id"].(string)
	if code, _ := request(t, h, http.MethodPost, "/v1/admin/teams/"+a+"/projects/"+id+"/github-binding", "bootstrap-local-test", "", map[string]int64{"installationId": 42, "repositoryId": 1001}); code != 200 {
		t.Fatalf("operator bind: %d", code)
	}
	push := []byte(`{"installation":{"id":42},"repository":{"id":1001},"ref":"refs/heads/main","after":"` + strings.Repeat("a", 40) + `"}`)
	if code, _ := gitEvent(t, h, "before-install-delete", push, true); code != 202 {
		t.Fatalf("push: %d", code)
	}
	deleted := []byte(`{"action":"deleted","installation":{"id":42},"sender":{"id":77}}`)
	if code, result := signedGitHubEvent(t, h, "installation", "delete-install-1", deleted); code != 202 || result["status"] != "binding_revoked" {
		t.Fatalf("installation deletion: %d %v", code, result)
	}
	if code, result := request(t, h, http.MethodGet, path+"/"+id+"/github-pushes/before-install-delete", owner, "", nil); code != 200 || result["status"] != "failed" || result["errorCode"] != "github_binding_revoked" {
		t.Fatalf("pending job not blocked: %d %v", code, result)
	}
	if code, result := gitEvent(t, h, "after-install-delete", push, true); code != 202 || result["status"] != "ignored_unbound" {
		t.Fatalf("deleted installation still enqueued: %d %v", code, result)
	}
}

func TestAmbiguousGitHubUserRevocationFailsClosedForOAuthBindings(t *testing.T) {
	db := testDatabase(t)
	app, closeApp := githubTestClient(t)
	defer closeApp()
	oauth, closeOAuth, _ := oauthTestClient(t, true)
	defer closeOAuth()
	h := NewServer(db, "bootstrap-local-test", WithGitHubVerifier(app), WithGitHubOAuth(oauth), WithGitHubWebhookSecret("webhook-test-secret"))
	a, owner := createTeam(t, h, "ghost-revocation")
	path := "/v1/teams/" + a + "/projects"
	_, p := request(t, h, http.MethodPost, path, owner, "project", map[string]string{"slug": "site", "name": "Site"})
	id := p["id"].(string)
	_, start := request(t, h, http.MethodPost, path+"/"+id+"/github-connect", owner, "", map[string]int64{"installationId": 42, "repositoryId": 1001})
	link, err := url.Parse(start["authorizationUrl"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := oauthCallback(t, h, link.Query().Get("state"), "approved-code"); code != 200 {
		t.Fatalf("grant: %d", code)
	}
	ghost := []byte(`{"action":"revoked","sender":{"id":0,"login":"ghost"}}`)
	if code, result := signedGitHubEvent(t, h, "github_app_authorization", "ambiguous-revocation", ghost); code != 202 || result["status"] != "binding_revoked" {
		t.Fatalf("ambiguous revocation not closed: %d %v", code, result)
	}
	if code, result := request(t, h, http.MethodGet, path+"/"+id+"/github-binding", owner, "", nil); code != 200 || result["status"] != "revoked" {
		t.Fatalf("ambiguous actor kept binding: %d %v", code, result)
	}
}
