package bundle

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

var storeID = regexp.MustCompile(`^[a-z]+_[a-z0-9]{3,64}$`)
var storeDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

type Store interface {
	Put(context.Context, *Static) error
	Get(context.Context, string, string, string) (*Static, error)
}

// DiskStore is a single-node development artifact store, not distributed
// object storage or a production durability guarantee.
type DiskStore struct{ root string }

func NewDiskStore(root string) *DiskStore { return &DiskStore{root: root} }

func (s *DiskStore) filename(teamID, projectID, digest string) (string, error) {
	if s.root == "" || !storeID.MatchString(teamID) || !storeID.MatchString(projectID) || !storeDigest.MatchString(digest) {
		return "", errors.New("invalid artifact scope")
	}
	return filepath.Join(s.root, teamID, projectID, digest+".json"), nil
}

func (s *DiskStore) Put(ctx context.Context, b *Static) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if b == nil || b.Verify() != nil {
		return errors.New("invalid bundle")
	}
	file, err := s.filename(b.Manifest.TeamID, b.Manifest.ProjectID, b.Manifest.Digest)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), ".artifact-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err = json.NewEncoder(tmp).Encode(struct {
		Manifest Manifest          `json:"manifest"`
		Files    map[string][]byte `json:"files"`
	}{b.Manifest, b.Files()}); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := os.Link(tmp.Name(), file); errors.Is(err, os.ErrExist) {
		other, readErr := s.Get(ctx, b.Manifest.TeamID, b.Manifest.ProjectID, b.Manifest.Digest)
		if readErr != nil {
			return readErr
		}
		return other.Verify()
	} else {
		return err
	}
}

func (s *DiskStore) Get(ctx context.Context, teamID, projectID, digest string) (*Static, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	file, err := s.filename(teamID, projectID, digest)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (32<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 32<<20 {
		return nil, errors.New("stored bundle is too large")
	}
	var saved struct {
		Manifest Manifest          `json:"manifest"`
		Files    map[string][]byte `json:"files"`
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		return nil, err
	}
	if saved.Manifest.TeamID != teamID || saved.Manifest.ProjectID != projectID || saved.Manifest.Digest != digest {
		return nil, errors.New("artifact ownership mismatch")
	}
	b, err := CaptureStatic(teamID, projectID, saved.Manifest.SourceSHA256, saved.Manifest.BuilderDigest, saved.Files)
	if err != nil || b.Manifest.Digest != digest {
		return nil, errors.New("stored bundle changed")
	}
	return b, nil
}
