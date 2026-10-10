package github

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestOAuthUsesPKCEAndChecksUserRepositoryReadPermission(t *testing.T) {
	const state = "ghs_example_high_entropy_state"
	var verifier string
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/oauth/access_token":
			if r.Method != http.MethodPost || r.Header.Get("Accept") != "application/json" {
				t.Error("OAuth exchange is not JSON/form POST")
			}
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			if r.PostForm.Get("client_id") != "Iv1.example" || r.PostForm.Get("client_secret") != "private-client-secret" || r.PostForm.Get("code") != "approved-code" || r.PostForm.Get("repository_id") != "1001" || r.PostForm.Get("redirect_uri") != "https://control.test/v1/oauth/github/callback" {
				t.Errorf("OAuth exchange mismatch (sensitive values omitted)")
			}
			verifier = r.PostForm.Get("code_verifier")
			_, _ = w.Write([]byte(`{"access_token":"ghu_short_lived","token_type":"bearer"}`))
		case "/user/installations/42/repositories":
			if r.Header.Get("Authorization") != "Bearer ghu_short_lived" || r.URL.Query().Get("per_page") != "100" {
				t.Error("user repository list lacks user identity")
			}
			_, _ = w.Write([]byte(`{"total_count":2,"repositories":[{"id":1001,"full_name":"org/site","default_branch":"main","permissions":{"pull":true}},{"id":1002,"full_name":"org/private","permissions":{"pull":false}}]}`))
		case "/user":
			if r.Header.Get("Authorization") != "Bearer ghu_short_lived" {
				t.Error("user identity missing scoped token")
			}
			_, _ = w.Write([]byte(`{"id":77,"login":"owner"}`))
		default:
			t.Errorf("unexpected OAuth endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer remote.Close()
	client := NewOAuthClient("Iv1.example", "private-client-secret", "https://control.test/v1/oauth/github/callback", []byte(strings.Repeat("K", 32)), remote.URL, remote.URL, remote.URL, remote.Client())
	authURL, err := client.AuthorizeURL(state)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authURL)
	if err != nil || parsed.Path != "/login/oauth/authorize" || parsed.Query().Get("state") != state || parsed.Query().Get("code_challenge_method") != "S256" || parsed.Query().Get("client_id") != "Iv1.example" {
		t.Fatalf("invalid authorize URL: %v", err)
	}
	token, err := client.ExchangeUserCode(context.Background(), "approved-code", state, 1001)
	if err != nil || token != "ghu_short_lived" {
		t.Fatalf("user token exchange failed: %v", err)
	}
	if userID, err := client.UserIdentity(context.Background(), token); err != nil || userID != 77 {
		t.Fatalf("OAuth user identity: %d %v", userID, err)
	}
	digest := sha256.Sum256([]byte(verifier))
	if verifier == "" || parsed.Query().Get("code_challenge") != base64.RawURLEncoding.EncodeToString(digest[:]) {
		t.Fatal("PKCE challenge was not bound to callback verifier")
	}
	repo, err := client.UserCanReadRepository(context.Background(), token, 42, 1001)
	if err != nil || repo.ID != 1001 || repo.FullName != "org/site" {
		t.Fatalf("user repository access: %+v %v", repo, err)
	}
	if _, err := client.UserCanReadRepository(context.Background(), token, 42, 1002); err == nil {
		t.Fatal("user's private repo with pull=false accepted")
	}
}

func TestOAuthRejectsProviderTokenErrorsAndRedirects(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login/oauth/access_token" {
			_, _ = w.Write([]byte(`{"error":"bad_verification_code"}`))
			return
		}
		w.Header().Set("Location", "https://attacker.test/")
		w.WriteHeader(http.StatusFound)
	}))
	defer remote.Close()
	client := NewOAuthClient("Iv1.example", "secret", "https://control.test/v1/oauth/github/callback", []byte(strings.Repeat("K", 32)), remote.URL, remote.URL, remote.URL, remote.Client())
	if _, err := client.ExchangeUserCode(context.Background(), "bad-code", "state", 1001); err == nil {
		t.Fatal("provider error accepted as user auth")
	}
	if _, err := client.UserCanReadRepository(context.Background(), "ghu_valid", 42, 1001); err == nil {
		t.Fatal("redirected user token to another host")
	}
}
