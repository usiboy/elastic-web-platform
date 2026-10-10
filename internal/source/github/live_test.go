package github

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"elastic-web-platform/internal/framework"
	"elastic-web-platform/internal/source/uploads"
)

// Opt-in, read-only integration evidence. It never writes to GitHub, prints
// GitHub tokens or copies the App private key into the project workspace.
func TestLiveScopedRepositories(t *testing.T) {
	keyFile := os.Getenv("EWP_LIVE_GITHUB_KEY_FILE")
	if keyFile == "" {
		t.Skip("set EWP_LIVE_GITHUB_KEY_FILE for explicit read-only App verification")
	}
	appID, err := strconv.ParseInt(os.Getenv("EWP_LIVE_GITHUB_APP_ID"), 10, 64)
	if err != nil || appID <= 0 {
		t.Fatal("invalid EWP_LIVE_GITHUB_APP_ID")
	}
	installationID, err := strconv.ParseInt(os.Getenv("EWP_LIVE_GITHUB_INSTALLATION_ID"), 10, 64)
	if err != nil || installationID <= 0 {
		t.Fatal("invalid EWP_LIVE_GITHUB_INSTALLATION_ID")
	}
	ids := strings.Split(os.Getenv("EWP_LIVE_GITHUB_REPOSITORY_IDS"), ",")
	if len(ids) == 0 || (len(ids) == 1 && ids[0] == "") {
		t.Fatal("repository ID allowlist required")
	}
	key, err := LoadPrivateKey(keyFile)
	if err != nil {
		t.Fatal("protected App key unavailable: ", err)
	}
	client := NewClient(appID, key, "https://api.github.com", nil)
	var outputDir string
	if root := os.Getenv("EWP_LIVE_GITHUB_OUTPUT_ROOT"); root != "" {
		outputDir, err = os.MkdirTemp(root, "ewp-source-")
		if err != nil {
			t.Fatal("private archive directory: ", err)
		}
		t.Logf("sourceArchiveDirectory=%s", outputDir)
	}
	for _, raw := range ids {
		repoID, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil || repoID <= 0 {
			t.Fatal("invalid repository ID")
		}
		t.Run(strconv.FormatInt(repoID, 10), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			repo, err := client.VerifyRepository(ctx, installationID, repoID)
			if err != nil {
				t.Fatal("scoped installation repository verification: ", err)
			}
			if owner := os.Getenv("EWP_LIVE_GITHUB_OWNER"); owner != "" && !strings.HasPrefix(strings.ToLower(repo.FullName), strings.ToLower(owner)+"/") {
				t.Fatalf("unexpected repository owner: %s", repo.FullName)
			}
			sha, err := client.CurrentBranchHead(ctx, installationID, repoID, repo.DefaultBranch)
			if err != nil {
				t.Fatal("production branch head: ", err)
			}
			source, err := client.FetchPinned(ctx, installationID, repoID, sha)
			if err != nil {
				t.Fatal("pinned Git tree/blob byte verification: ", err)
			}
			manifest, err := uploads.Validate(source.Archive)
			if err != nil || manifest.Digest != source.ArchiveSHA256 {
				t.Fatal("actual source bytes inconsistent: ", err)
			}
			preset, err := framework.Detect(source.Archive)
			if err != nil {
				t.Fatalf("supported framework detection: %v", err)
			}
			gz, err := gzip.NewReader(bytes.NewReader(source.Archive))
			if err != nil {
				t.Fatal(err)
			}
			tr := tar.NewReader(gz)
			var names []string
			for {
				header, err := tr.Next()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				names = append(names, header.Name)
			}
			_ = gz.Close()
			t.Logf("repository=%s id=%d branch=%s commit=%s sourceSha256=%s files=%d preset=%s requiresBuild=%t", repo.FullName, repoID, repo.DefaultBranch, sha, manifest.Digest, manifest.Files, preset.Name, preset.RequiresBuild)
			t.Logf("sourcePaths=%v", names)
			if outputDir != "" {
				file := filepath.Join(outputDir, fmt.Sprintf("repo-%d-%s.tar.gz", repoID, sha[:12]))
				if err := os.WriteFile(file, source.Archive, 0600); err != nil {
					t.Fatal("private source archive: ", err)
				}
			}
		})
	}
}
