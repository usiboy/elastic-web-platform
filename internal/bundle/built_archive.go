package bundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

// CaptureBuiltArchive imports real static bytes from an operator-controlled
// build output tar.gz. It does not prove which builder executed or authorize
// arbitrary tenant build scripts; that admission requires a separate signer.
func CaptureBuiltArchive(teamID, projectID, sourceSHA, builderRef string, archive []byte) (*Static, error) {
	if len(archive) == 0 || len(archive) > 16<<20 {
		return nil, errors.New("built archive exceeds compressed size limit")
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	files := make(map[string][]byte)
	seen := make(map[string]bool)
	var total int64
	entries := 0
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("built tar: %w", err)
		}
		entries++
		if entries > 512 {
			return nil, errors.New("too many build output entries")
		}
		name := strings.TrimPrefix(h.Name, "./")
		if h.Typeflag == tar.TypeDir {
			name = strings.TrimSuffix(name, "/")
		}
		if name == "" && h.Typeflag == tar.TypeDir {
			continue
		}
		if name == "" || len(name) > 256 || strings.HasPrefix(name, "/") || strings.ContainsAny(name, "\\\x00") || path.Clean(name) != name || !validPath("/"+name) {
			return nil, errors.New("unsafe build output path")
		}
		if seen[name] {
			return nil, errors.New("duplicate build output path")
		}
		seen[name] = true
		if h.Typeflag == tar.TypeDir {
			continue
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
			return nil, errors.New("build output must contain regular files only")
		}
		if h.Size < 0 || h.Size > 4<<20 || total+h.Size > 16<<20 || len(files) >= 256 {
			return nil, errors.New("build output exceeds file or size budget")
		}
		content, err := io.ReadAll(io.LimitReader(tr, h.Size+1))
		if err != nil || int64(len(content)) != h.Size {
			return nil, errors.New("truncated build output file")
		}
		files["/"+name] = content
		total += h.Size
	}
	// tar EOF precedes the gzip CRC; read the bounded tail before trusting bytes.
	remainder, err := io.Copy(io.Discard, io.LimitReader(gz, (16<<20)+1))
	if err != nil || remainder > 16<<20 {
		return nil, errors.New("invalid compressed build trailer")
	}
	return CaptureStatic(teamID, projectID, sourceSHA, builderRef, files)
}
