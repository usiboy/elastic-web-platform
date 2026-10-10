package github

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

type ScopedTokenFile struct {
	InstallationID int64         `json:"installationId"`
	Credentials    []ScopedToken `json:"credentials"`
}

// LoadScopedTokenFile requires a private regular file under a private parent.
// It never returns the secret in an error message. Only the exact operator
// allowlist is accepted; expiry and token shape are checked by the Client.
func LoadScopedTokenFile(name string, installationID int64, allowed ...int64) (ScopedTokenFile, error) {
	var empty ScopedTokenFile
	if installationID <= 0 || len(allowed) == 0 || len(allowed) > 2 || name == "" {
		return empty, errors.New("scoped Git identity is not configured")
	}
	parent, parentErr := os.Lstat(filepath.Dir(name))
	info, err := os.Lstat(name)
	if parentErr != nil || !parent.IsDir() || parent.Mode().Perm()&0077 != 0 || err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() <= 0 || info.Size() > 8192 {
		return empty, errors.New("Git token file must be a bounded private regular file")
	}
	f, err := os.Open(name)
	if err != nil {
		return empty, errors.New("Git token file unavailable")
	}
	defer f.Close()
	decoder := json.NewDecoder(io.LimitReader(f, 8193))
	decoder.DisallowUnknownFields()
	var result ScopedTokenFile
	if err := decoder.Decode(&result); err != nil || decoder.Decode(new(any)) != io.EOF || result.InstallationID != installationID || len(result.Credentials) != len(allowed) {
		return empty, errors.New("Git token file structure invalid")
	}
	seen := make(map[int64]bool, len(allowed))
	for _, item := range result.Credentials {
		valid := false
		for _, repoID := range allowed {
			if item.RepositoryID == repoID {
				valid = true
			}
		}
		if !valid || seen[item.RepositoryID] || item.InstallationID != installationID {
			return empty, errors.New("Git token scope does not match the fixed repository allowlist")
		}
		seen[item.RepositoryID] = true
	}
	return result, nil
}
