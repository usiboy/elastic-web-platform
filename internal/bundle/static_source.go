package bundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"

	"elastic-web-platform/internal/framework"
)

// CaptureStaticSource uses only a bounded file copy for plain HTML projects.
// Frameworks requiring install/build cannot enter this path without an
// independently isolated and attested builder.
func CaptureStaticSource(teamID, projectID string, archive []byte) (*Static, error) {
	preset, err := framework.Detect(archive)
	if err != nil {
		return nil, err
	}
	if preset.Name != "static-html" || preset.RequiresBuild {
		return nil, errors.New("source requires an isolated build")
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	files := make(map[string][]byte)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if header.Typeflag == tar.TypeDir {
			continue
		}
		content, err := io.ReadAll(io.LimitReader(tr, 1<<20+1))
		if err != nil || len(content) > 1<<20 {
			return nil, errors.New("static source file exceeds limit")
		}
		files["/"+header.Name] = content
	}
	hash := sha256.Sum256(archive)
	return CaptureStatic(teamID, projectID, hex.EncodeToString(hash[:]), "builtin/static-copy:v1", files)
}
