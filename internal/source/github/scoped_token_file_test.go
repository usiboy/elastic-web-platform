package github

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScopedGitTokenFileRejectsWrongScopeAndPublicFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(root, "read-only.json")
	tokens := ScopedTokenFile{InstallationID: 42, Credentials: []ScopedToken{
		{InstallationID: 42, RepositoryID: 1001, Token: "temporary-repo-one", ExpiresAt: time.Now().Add(40 * time.Minute)},
		{InstallationID: 42, RepositoryID: 1002, Token: "temporary-repo-two", ExpiresAt: time.Now().Add(40 * time.Minute)},
	}}
	data, err := json.Marshal(tokens)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, data, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadScopedTokenFile(name, 42, 1001, 1002)
	if err != nil || len(loaded.Credentials) != 2 {
		t.Fatalf("two fixed private credentials unavailable: %v", err)
	}
	if _, err := LoadScopedTokenFile(name, 42, 1001, 9999); err == nil {
		t.Fatal("wrong repository allowlist accepted")
	}
	if err := os.Chmod(name, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadScopedTokenFile(name, 42, 1001, 1002); err == nil {
		t.Fatal("world-readable token file accepted")
	}
	if err := os.Chmod(name, 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "linked.json")
	if err := os.Symlink(name, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadScopedTokenFile(alias, 42, 1001, 1002); err == nil {
		t.Fatal("token symlink accepted")
	}
}
