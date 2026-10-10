package github

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strings"

	"elastic-web-platform/internal/source/uploads"
)

type PinnedSource struct {
	Archive       []byte
	ArchiveSHA256 string
	CommitSHA     string
	TreeSHA       string
	Repository    Repository
}

func validGitSHA(sha string) bool {
	if len(sha) != 40 {
		return false
	}
	for _, r := range sha {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func validGitPath(name string) bool {
	return name != "" && len(name) <= 256 && !strings.HasPrefix(name, "/") && !strings.ContainsAny(name, "\\\x00") && path.Clean(name) == name && name != ".." && !strings.HasPrefix(name, "../")
}

// FetchPinned materializes a bounded source archive from an exact Git commit.
// The GitHub API is read with a token restricted to this single repository;
// the resulting archive hash describes real bytes, not a caller's declaration.
func (c *Client) FetchPinned(ctx context.Context, installationID, repositoryID int64, commitSHA string) (PinnedSource, error) {
	if !validGitSHA(commitSHA) {
		return PinnedSource{}, errors.New("invalid full Git commit SHA")
	}
	repo, token, err := c.scopedRepository(ctx, installationID, repositoryID)
	if err != nil {
		return PinnedSource{}, err
	}
	return c.fetchPinned(ctx, repo, token, commitSHA)
}

// FetchPublicPilotPinned re-reads all Git objects from GitHub for the two
// approved public test repositories. OIDC verifies who triggered the request;
// it does not make arbitrary uploaded archive bytes trustworthy.
func (c *Client) FetchPublicPilotPinned(ctx context.Context, repositoryID int64, commitSHA string) (PinnedSource, error) {
	if !validGitSHA(commitSHA) {
		return PinnedSource{}, errors.New("invalid full Git commit SHA")
	}
	repo, err := c.publicPilotRepository(ctx, repositoryID)
	if err != nil {
		return PinnedSource{}, err
	}
	return c.fetchPinned(ctx, repo, "", commitSHA)
}

func (c *Client) fetchPinned(ctx context.Context, repo Repository, token, commitSHA string) (PinnedSource, error) {
	base := "/repos/" + repo.FullName
	var commit struct {
		SHA  string `json:"sha"`
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if err := c.request(ctx, http.MethodGet, base+"/git/commits/"+commitSHA, token, nil, 200, &commit); err != nil {
		return PinnedSource{}, err
	}
	if commit.SHA != commitSHA || !validGitSHA(commit.Tree.SHA) {
		return PinnedSource{}, errors.New("commit identity mismatch")
	}
	var tree struct {
		SHA       string `json:"sha"`
		Truncated bool   `json:"truncated"`
		Entries   []struct {
			Path string `json:"path"`
			Type string `json:"type"`
			Mode string `json:"mode"`
			SHA  string `json:"sha"`
			Size int64  `json:"size"`
		} `json:"tree"`
	}
	if err := c.requestLimited(ctx, http.MethodGet, base+"/git/trees/"+commit.Tree.SHA+"?recursive=1", token, nil, 200, &tree, 512<<10); err != nil {
		return PinnedSource{}, err
	}
	if tree.SHA != commit.Tree.SHA || tree.Truncated || len(tree.Entries) == 0 || len(tree.Entries) > 128 {
		return PinnedSource{}, errors.New("incomplete or oversized Git tree")
	}
	sort.Slice(tree.Entries, func(i, j int) bool { return tree.Entries[i].Path < tree.Entries[j].Path })
	var result bytes.Buffer
	gz := gzip.NewWriter(&result)
	tw := tar.NewWriter(gz)
	files := 0
	var total int64
	previous := ""
	for _, entry := range tree.Entries {
		if !validGitPath(entry.Path) || previous == entry.Path {
			return PinnedSource{}, errors.New("unsafe or duplicate Git path")
		}
		previous = entry.Path
		if entry.Type == "tree" && entry.Mode == "040000" {
			continue
		}
		if entry.Type != "blob" || (entry.Mode != "100644" && entry.Mode != "100755") || !validGitSHA(entry.SHA) || entry.Size < 0 || entry.Size > uploads.MaxArchiveBytes {
			return PinnedSource{}, errors.New("unsupported or oversized Git object")
		}
		files++
		total += entry.Size
		if total > 4<<20 {
			return PinnedSource{}, errors.New("Git source exceeds unpacked size limit")
		}
		var blob struct {
			SHA      string `json:"sha"`
			Encoding string `json:"encoding"`
			Size     int64  `json:"size"`
			Content  string `json:"content"`
		}
		if err := c.requestLimited(ctx, http.MethodGet, base+"/git/blobs/"+entry.SHA, token, nil, 200, &blob, 2<<20); err != nil {
			return PinnedSource{}, err
		}
		if blob.SHA != entry.SHA || blob.Encoding != "base64" || blob.Size != entry.Size {
			return PinnedSource{}, errors.New("Git blob metadata mismatch")
		}
		data, err := base64.StdEncoding.DecodeString(strings.Map(func(r rune) rune {
			if r == '\n' || r == '\r' {
				return -1
			}
			return r
		}, blob.Content))
		if err != nil || int64(len(data)) != entry.Size {
			return PinnedSource{}, errors.New("Git blob content mismatch")
		}
		object := sha1.Sum(append([]byte(fmt.Sprintf("blob %d\x00", len(data))), data...))
		if hex.EncodeToString(object[:]) != entry.SHA {
			return PinnedSource{}, errors.New("Git blob digest mismatch")
		}
		if err := tw.WriteHeader(&tar.Header{Name: entry.Path, Typeflag: tar.TypeReg, Mode: 0644, Size: int64(len(data))}); err != nil {
			return PinnedSource{}, err
		}
		if _, err := tw.Write(data); err != nil {
			return PinnedSource{}, err
		}
	}
	if files == 0 {
		return PinnedSource{}, errors.New("Git tree contains no regular files")
	}
	if err := tw.Close(); err != nil {
		return PinnedSource{}, err
	}
	if err := gz.Close(); err != nil {
		return PinnedSource{}, err
	}
	if _, err := uploads.Validate(result.Bytes()); err != nil {
		return PinnedSource{}, fmt.Errorf("source archive: %w", err)
	}
	digest := sha256.Sum256(result.Bytes())
	return PinnedSource{Archive: result.Bytes(), ArchiveSHA256: hex.EncodeToString(digest[:]), CommitSHA: commitSHA, TreeSHA: tree.SHA, Repository: repo}, nil
}
