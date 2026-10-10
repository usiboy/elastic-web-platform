package github

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTemporarySingleRepoTokenCannotReadAnotherRepo(t *testing.T) {
	var checked int
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-ephemeral-only" {
			t.Error("scoped Git token missing")
		}
		checked++
		switch r.URL.Path {
		case "/installation/repositories":
			_, _ = w.Write([]byte(`{"total_count":1,"repositories":[{"id":1001}]}`))
		case "/repositories/1001":
			_, _ = w.Write([]byte(`{"id":1001,"full_name":"org/site","default_branch":"main"}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer remote.Close()
	client, err := NewScopedTokenClient(42, []ScopedToken{{InstallationID: 42, RepositoryID: 1001, Token: "test-ephemeral-only", ExpiresAt: time.Now().Add(40 * time.Minute)}}, remote.URL, remote.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.VerifyRepository(context.Background(), 42, 1002); err == nil || checked != 0 {
		t.Fatalf("wrong Repo reached provider: calls=%d err=%v", checked, err)
	}
	if _, err := client.VerifyRepository(context.Background(), 43, 1001); err == nil || checked != 0 {
		t.Fatalf("wrong Installation reached provider: calls=%d err=%v", checked, err)
	}
	repo, err := client.VerifyRepository(context.Background(), 42, 1001)
	if err != nil || repo.ID != 1001 || checked != 2 {
		t.Fatalf("single Repo token could not read its own repo: %+v %v calls=%d", repo, err, checked)
	}
	expired, err := NewScopedTokenClient(42, []ScopedToken{{InstallationID: 42, RepositoryID: 1001, Token: "test-ephemeral-only", ExpiresAt: time.Now().Add(-time.Minute)}}, remote.URL, remote.Client())
	if err == nil || expired != nil || checked != 2 {
		t.Fatalf("expired token reached provider: calls=%d err=%v", checked, err)
	}
}

func TestAppMintedTokenRequiresSingleRepoAndExpiry(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(45 * time.Minute).UTC().Format(time.RFC3339)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/app/installations/42/access_tokens" || r.Method != http.MethodPost {
			t.Error("unexpected GitHub token request")
			w.WriteHeader(404)
			return
		}
		w.WriteHeader(201)
		_, _ = w.Write([]byte(fmt.Sprintf(`{"token":"test-ephemeral-only","expires_at":%q,"permissions":{"contents":"read","metadata":"read"},"repositories":[{"id":1001}]}`, expires)))
	}))
	defer remote.Close()
	client := NewClient(12345, key, remote.URL, remote.Client())
	scoped, err := client.MintScopedRepositoryToken(context.Background(), 42, 1001)
	if err != nil || scoped.RepositoryID != 1001 || scoped.InstallationID != 42 || scoped.Token == "" || scoped.ExpiresAt.Before(time.Now().Add(30*time.Minute)) {
		t.Fatalf("single Repo mint failed: %v", err)
	}
}

func TestRevokeScopedTokenSendsBearerOnlyToFixedOrigin(t *testing.T) {
	longToken := "ghs_" + strings.Repeat("x", 386)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/installation/token" || r.Method != http.MethodDelete || r.Header.Get("Authorization") != "Bearer "+longToken {
			t.Error("token revocation did not use the installation endpoint")
			w.WriteHeader(403)
			return
		}
		w.WriteHeader(204)
	}))
	defer remote.Close()
	client, err := NewScopedTokenClient(42, []ScopedToken{{InstallationID: 42, RepositoryID: 1001, Token: longToken, ExpiresAt: time.Now().Add(40 * time.Minute)}}, remote.URL, remote.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.RevokeScopedToken(context.Background(), longToken); err != nil {
		t.Fatalf("single-repo token revocation failed: %v", err)
	}
}
