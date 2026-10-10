package uploads

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

var objectID = regexp.MustCompile(`^[a-z]+_[a-z0-9]{3,64}$`)
var objectDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

type Store interface {
	Put(ctx context.Context, teamID, projectID, digest string, data []byte) error
	Get(ctx context.Context, teamID, projectID, digest string) ([]byte, error)
}

// DiskStore is for an isolated single-node development environment. Replace it
// with a versioned object store before distributed builds or public onboarding.
type DiskStore struct{ root string }

func NewDiskStore(root string) *DiskStore { return &DiskStore{root: root} }

func (s *DiskStore) objectPath(teamID, projectID, digest string) (string, error) {
	if !objectID.MatchString(teamID) || !objectID.MatchString(projectID) || !objectDigest.MatchString(digest) || s.root == "" {
		return "", errors.New("invalid object scope")
	}
	return filepath.Join(s.root, teamID, projectID, digest), nil
}

func (s *DiskStore) Put(ctx context.Context, teamID, projectID, digest string, data []byte) error {
	file, err := s.objectPath(teamID, projectID, digest)
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if len(data) == 0 || len(data) > MaxArchiveBytes {
		return errors.New("invalid object size")
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != digest {
		return errors.New("object digest mismatch")
	}
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), ".upload-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := os.Link(tmp.Name(), file); errors.Is(err, os.ErrExist) {
		existing, readErr := s.Get(ctx, teamID, projectID, digest)
		if readErr != nil {
			return readErr
		}
		if string(existing) != string(data) {
			return errors.New("existing object differs")
		}
		return nil
	} else if err != nil {
		return err
	}
	return nil
}

func (s *DiskStore) Get(ctx context.Context, teamID, projectID, digest string) ([]byte, error) {
	file, err := s.objectPath(teamID, projectID, digest)
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxArchiveBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxArchiveBytes {
		return nil, errors.New("stored object too large")
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != digest {
		return nil, fmt.Errorf("stored object digest mismatch")
	}
	return data, nil
}
