package bundle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"path"
	"sort"
	"strings"
)

type Asset struct {
	Path        string `json:"path"`
	SHA256      string `json:"sha256"`
	Size        int    `json:"size"`
	ContentType string `json:"contentType"`
}

type Manifest struct {
	SchemaVersion int     `json:"schemaVersion"`
	TeamID        string  `json:"teamId"`
	ProjectID     string  `json:"projectId"`
	SourceSHA256  string  `json:"sourceSha256"`
	BuilderDigest string  `json:"builderDigest"`
	Files         []Asset `json:"files"`
	Digest        string  `json:"digest"`
}

type Static struct {
	Manifest Manifest
	data     map[string][]byte
}

func validPath(name string) bool {
	if !strings.HasPrefix(name, "/") || name == "/" || len(name) > 257 || strings.ContainsAny(name, "\\\x00?#") || path.Clean(name) != name {
		return false
	}
	for _, segment := range strings.Split(strings.TrimPrefix(name, "/"), "/") {
		lower := strings.ToLower(segment)
		if strings.HasPrefix(segment, ".") || lower == "dockerfile" || lower == "package.json" || lower == "package-lock.json" || strings.HasSuffix(lower, ".map") || strings.HasSuffix(lower, ".pem") || strings.HasSuffix(lower, ".key") {
			return false
		}
	}
	return true
}

// CaptureStatic snapshots real output bytes. A manifest without independent
// builder provenance is not a security attestation for tenant build scripts.
func CaptureStatic(teamID, projectID, sourceSHA, builderDigest string, files map[string][]byte) (*Static, error) {
	if teamID == "" || projectID == "" || sourceSHA == "" || builderDigest == "" || len(files) == 0 || len(files) > 256 {
		return nil, errors.New("invalid static output metadata")
	}
	if len(files["/index.html"]) == 0 {
		return nil, errors.New("index.html is required")
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	b := &Static{Manifest: Manifest{SchemaVersion: 1, TeamID: teamID, ProjectID: projectID, SourceSHA256: sourceSHA, BuilderDigest: builderDigest, Files: make([]Asset, 0, len(files))}, data: make(map[string][]byte, len(files))}
	total := 0
	for _, name := range names {
		content := files[name]
		if !validPath(name) || len(content) > 4<<20 {
			return nil, fmt.Errorf("invalid asset %q", name)
		}
		total += len(content)
		if total > 16<<20 {
			return nil, errors.New("static output too large")
		}
		copied := append([]byte(nil), content...)
		b.data[name] = copied
		digest := sha256.Sum256(copied)
		kind := mime.TypeByExtension(path.Ext(name))
		if kind == "" {
			kind = "application/octet-stream"
		}
		b.Manifest.Files = append(b.Manifest.Files, Asset{Path: name, SHA256: hex.EncodeToString(digest[:]), Size: len(copied), ContentType: kind})
	}
	b.Manifest.Digest = manifestDigest(b.Manifest)
	return b, nil
}

func manifestDigest(manifest Manifest) string {
	manifest.Digest = ""
	data, _ := json.Marshal(manifest)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func (b *Static) Verify() error {
	if b == nil || len(b.Manifest.Files) != len(b.data) || b.Manifest.Digest != manifestDigest(b.Manifest) {
		return errors.New("static manifest mismatch")
	}
	for _, entry := range b.Manifest.Files {
		data, ok := b.data[entry.Path]
		if !ok || !validPath(entry.Path) || len(data) != entry.Size {
			return errors.New("asset missing or size changed")
		}
		hash := sha256.Sum256(data)
		if hex.EncodeToString(hash[:]) != entry.SHA256 {
			return errors.New("asset digest mismatch")
		}
	}
	return nil
}

func (b *Static) Read(name string) ([]byte, bool) {
	if b == nil || !validPath(name) {
		return nil, false
	}
	data, ok := b.data[name]
	if !ok {
		return nil, false
	}
	return append([]byte(nil), data...), true
}

// Files returns independent bytes for persistence or a trusted snapshot copy.
func (b *Static) Files() map[string][]byte {
	if b == nil {
		return nil
	}
	files := make(map[string][]byte, len(b.data))
	for path, data := range b.data {
		files[path] = append([]byte(nil), data...)
	}
	return files
}
