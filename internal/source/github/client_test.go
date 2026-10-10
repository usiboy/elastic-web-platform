package github

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVerifyRepositoryUsesOnlyScopedInstallationToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var checked int
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/installations/42/access_tokens":
			if r.Method != http.MethodPost {
				t.Errorf("unexpected method %s", r.Method)
			}
			parts := strings.Split(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), ".")
			if len(parts) != 3 {
				t.Error("no GitHub App JWT")
				w.WriteHeader(401)
				return
			}
			payload, err := base64.RawURLEncoding.DecodeString(parts[1])
			if err != nil {
				t.Error(err)
				return
			}
			var claims map[string]any
			if err := json.Unmarshal(payload, &claims); err != nil || claims["iss"] != "12345" {
				t.Errorf("invalid app claims: %v %v", claims, err)
			}
			sig, err := base64.RawURLEncoding.DecodeString(parts[2])
			if err != nil {
				t.Error(err)
				return
			}
			digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
			if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], sig); err != nil {
				t.Error("invalid JWT signature")
			}
			var request struct {
				RepositoryIDs []int64           `json:"repository_ids"`
				Permissions   map[string]string `json:"permissions"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			if len(request.RepositoryIDs) != 1 || request.RepositoryIDs[0] != 1001 || request.Permissions["contents"] != "read" || len(request.Permissions) != 2 {
				t.Errorf("token over-scoped: %+v", request)
			}
			checked++
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"token":"scoped-test","repositories":[{"id":1001}]}`))
		case "/repositories/1001":
			if r.Header.Get("Authorization") != "Bearer scoped-test" {
				t.Error("repository request has wrong credentials")
			}
			checked++
			_, _ = w.Write([]byte(`{"id":1001,"full_name":"org/site","default_branch":"main"}`))
		default:
			t.Errorf("unexpected GitHub request %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer remote.Close()
	client := NewClient(12345, key, remote.URL, remote.Client())
	repo, err := client.VerifyRepository(context.Background(), 42, 1001)
	if err != nil || repo.ID != 1001 || repo.FullName != "org/site" || repo.DefaultBranch != "main" {
		t.Fatalf("verified repo: %+v %v", repo, err)
	}
	if checked != 2 {
		t.Fatalf("expected two read-only requests, got %d", checked)
	}
}

func TestVerifyRepositoryRejectsWrongProviderIdentity(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "access_tokens") {
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"token":"scoped-test","repositories":[{"id":9999}]}`))
			return
		}
		t.Fatal("repository endpoint must not be called when token is scoped to another repo")
	}))
	defer remote.Close()
	if _, err := NewClient(12345, key, remote.URL, remote.Client()).VerifyRepository(context.Background(), 42, 1001); err == nil {
		t.Fatal("unrelated repository accepted")
	}
}

func TestCurrentBranchHeadUsesScopedRepositoryIdentity(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const sha = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/installations/42/access_tokens":
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"token":"read-one-repo","repositories":[{"id":1001}]}`))
		case "/repositories/1001":
			_, _ = w.Write([]byte(`{"id":1001,"full_name":"org/site","default_branch":"main"}`))
		case "/repos/org/site/git/ref/heads/main":
			if r.Header.Get("Authorization") != "Bearer read-one-repo" {
				t.Error("branch checked without scoped installation token")
			}
			_, _ = w.Write([]byte(`{"ref":"refs/heads/main","object":{"sha":"` + sha + `"}}`))
		default:
			t.Errorf("unexpected Github endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer remote.Close()
	got, err := NewClient(12345, key, remote.URL, remote.Client()).CurrentBranchHead(context.Background(), 42, 1001, "main")
	if err != nil || got != sha {
		t.Fatalf("branch head: %q %v", got, err)
	}
}
