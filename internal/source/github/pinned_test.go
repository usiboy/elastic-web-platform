package github

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"elastic-web-platform/internal/source/uploads"
)

func blobID(data []byte) string {
	message := append([]byte(fmt.Sprintf("blob %d\x00", len(data))), data...)
	h := sha1.Sum(message)
	return hex.EncodeToString(h[:])
}

func pinnedProvider(t *testing.T, commit, mode string, content []byte, tamper ...bool) (*Client, func()) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const tree = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	blob := blobID(content)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			t.Error("missing scoped provider identity")
		}
		switch r.URL.Path {
		case "/app/installations/42/access_tokens":
			w.WriteHeader(201)
			_, _ = io.WriteString(w, `{"token":"scoped-test","repositories":[{"id":1001}]}`)
		case "/repositories/1001":
			if r.Header.Get("Authorization") != "Bearer scoped-test" {
				t.Error("unscoped repository read")
			}
			_, _ = io.WriteString(w, `{"id":1001,"full_name":"org/site","default_branch":"main"}`)
		case "/repos/org/site/git/commits/" + commit:
			_, _ = io.WriteString(w, fmt.Sprintf(`{"sha":%q,"tree":{"sha":%q}}`, commit, tree))
		case "/repos/org/site/git/trees/" + tree:
			_, _ = io.WriteString(w, fmt.Sprintf(`{"sha":%q,"truncated":false,"tree":[{"path":"index.html","type":"blob","mode":%q,"sha":%q,"size":%d}]}`, tree, mode, blob, len(content)))
		case "/repos/org/site/git/blobs/" + blob:
			served := content
			if len(tamper) > 0 && tamper[0] {
				served = bytes.Repeat([]byte("X"), len(content))
			}
			_, _ = io.WriteString(w, fmt.Sprintf(`{"sha":%q,"encoding":"base64","size":%d,"content":%q}`, blob, len(content), base64.StdEncoding.EncodeToString(served)))
		default:
			t.Errorf("unexpected GitHub request: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	return NewClient(42, key, remote.URL, remote.Client()), remote.Close
}

func TestFetchPinnedBuildsVerifiedDeterministicSourceBytes(t *testing.T) {
	commit := strings.Repeat("a", 40)
	client, closeRemote := pinnedProvider(t, commit, "100644", []byte("<h1>hello</h1>"))
	defer closeRemote()
	first, err := client.FetchPinned(context.Background(), 42, 1001, commit)
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.FetchPinned(context.Background(), 42, 1001, commit)
	if err != nil {
		t.Fatal(err)
	}
	if first.CommitSHA != commit || first.TreeSHA != strings.Repeat("b", 40) || first.ArchiveSHA256 == "" || !bytes.Equal(first.Archive, second.Archive) {
		t.Fatalf("non-deterministic pinned archive: %+v", first)
	}
	manifest, err := uploads.Validate(first.Archive)
	if err != nil || manifest.Digest != first.ArchiveSHA256 || manifest.Files != 1 {
		t.Fatalf("archive not verified: %+v %v", manifest, err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(first.Archive))
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	h, err := tar.NewReader(gz).Next()
	if err != nil || h.Name != "index.html" {
		t.Fatalf("wrong archive entry: %+v %v", h, err)
	}
}

func TestFetchPinnedRejectsUnsafeGitTree(t *testing.T) {
	commit := strings.Repeat("a", 40)
	client, closeRemote := pinnedProvider(t, commit, "120000", []byte("../escape"))
	defer closeRemote()
	if _, err := client.FetchPinned(context.Background(), 42, 1001, commit); err == nil {
		t.Fatal("symlink git object accepted")
	}
	if _, err := client.FetchPinned(context.Background(), 42, 1001, strings.Repeat("z", 40)); err == nil {
		t.Fatal("non-SHA commit accepted")
	}
}

func TestFetchPinnedRejectsBlobWhoseActualBytesDifferFromGitObject(t *testing.T) {
	commit := strings.Repeat("a", 40)
	client, closeRemote := pinnedProvider(t, commit, "100644", []byte("source"), true)
	defer closeRemote()
	if _, err := client.FetchPinned(context.Background(), 42, 1001, commit); err == nil {
		t.Fatal("mutated blob bytes accepted as fixed commit")
	}
}

func TestPublicPilotFetchesOnlyApprovedGitObjectsWithoutCredentials(t *testing.T) {
	commit, tree := strings.Repeat("a", 40), strings.Repeat("b", 40)
	content := []byte("<h1>new commit</h1>")
	blob := blobID(content)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("public fixed-source reader sent a GitHub credential")
		}
		switch r.URL.Path {
		case "/repositories/1404606559":
			_, _ = io.WriteString(w, `{"id":1404606559,"full_name":"usiboy/testA","default_branch":"main"}`)
		case "/repos/usiboy/testA/git/ref/heads/main":
			_, _ = io.WriteString(w, fmt.Sprintf(`{"ref":"refs/heads/main","object":{"sha":%q}}`, commit))
		case "/repos/usiboy/testA/git/commits/" + commit:
			_, _ = io.WriteString(w, fmt.Sprintf(`{"sha":%q,"tree":{"sha":%q}}`, commit, tree))
		case "/repos/usiboy/testA/git/trees/" + tree:
			_, _ = io.WriteString(w, fmt.Sprintf(`{"sha":%q,"truncated":false,"tree":[{"path":"index.html","type":"blob","mode":"100644","sha":%q,"size":%d}]}`, tree, blob, len(content)))
		case "/repos/usiboy/testA/git/blobs/" + blob:
			_, _ = io.WriteString(w, fmt.Sprintf(`{"sha":%q,"encoding":"base64","size":%d,"content":%q}`, blob, len(content), base64.StdEncoding.EncodeToString(content)))
		default:
			t.Errorf("unexpected public Git path: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer remote.Close()
	client := NewClient(0, nil, remote.URL, remote.Client())
	if head, err := client.PublicPilotBranchHead(context.Background(), 1404606559, "main"); err != nil || head != commit {
		t.Fatalf("public branch head unavailable: %s %v", head, err)
	}
	archive, err := client.FetchPublicPilotPinned(context.Background(), 1404606559, commit)
	if err != nil || archive.CommitSHA != commit || archive.Repository.ID != 1404606559 {
		t.Fatalf("public pinned source rejected: %+v %v", archive.Repository, err)
	}
	if _, err := client.FetchPublicPilotPinned(context.Background(), 999, commit); err == nil {
		t.Fatal("unknown repository could use public pinned source")
	}
	if _, err := client.PublicPilotBranchHead(context.Background(), 1404606559, "feature"); err == nil {
		t.Fatal("feature branch bypassed main-only source policy")
	}
}
