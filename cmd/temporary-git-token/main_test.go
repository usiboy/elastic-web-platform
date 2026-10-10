package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"elastic-web-platform/internal/source/github"
)

func TestFailedSecondRepoMintRevokesBothIssuedTokens(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	firstToken := "ghs_" + strings.Repeat("x", 386)
	secondToken := "ghs_" + strings.Repeat("y", 386)
	expires := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	var revoked int
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/installations/167883648/access_tokens":
			var body struct {
				RepositoryIDs []int64 `json:"repository_ids"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.RepositoryIDs) != 1 {
				t.Error("unscoped mint request")
				w.WriteHeader(422)
				return
			}
			repoID := body.RepositoryIDs[0]
			// The second repo intentionally lacks read-only contents permission.
			permissions := `"contents":"read","metadata":"read"`
			token := firstToken
			if repoID == repoB {
				permissions = `"contents":"write","metadata":"read"`
				token = secondToken
			}
			w.WriteHeader(201)
			_, _ = w.Write([]byte(fmt.Sprintf(`{"token":%q,"expires_at":%q,"permissions":{%s},"repositories":[{"id":%d}]}`, token, expires, permissions, repoID)))
		case "/installation/repositories":
			_, _ = w.Write([]byte(fmt.Sprintf(`{"total_count":1,"repositories":[{"id":%d}]}`, repoA)))
		case "/repositories/1404606559":
			_, _ = w.Write([]byte(`{"id":1404606559,"full_name":"usiboy/testA","default_branch":"main"}`))
		case "/installation/token":
			bearer := r.Header.Get("Authorization")
			if r.Method != http.MethodDelete || (bearer != "Bearer "+firstToken && bearer != "Bearer "+secondToken) {
				t.Error("scoped token not revoked")
			}
			revoked++
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
		}
	}))
	defer remote.Close()
	client := github.NewClient(5187675, key, remote.URL, remote.Client())
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "temporary-git-ab.json")
	if err := mintAndVerify(context.Background(), client, file, remote.URL, remote.Client()); err == nil || revoked != 2 {
		t.Fatalf("bad second Repo did not revoke minted token: revoked=%d err=%v", revoked, err)
	}
	if _, err := os.Lstat(file); !os.IsNotExist(err) {
		t.Fatalf("failed mint wrote token file: %v", err)
	}
}
