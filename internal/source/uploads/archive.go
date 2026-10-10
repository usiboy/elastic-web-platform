package uploads

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

const MaxArchiveBytes = 1 << 20

type Manifest struct {
	Digest string
	Size   int
	Files  int
}

// Validate inspects a gzip-compressed tar without extracting it or running code.
func Validate(data []byte) (Manifest, error) {
	if len(data) == 0 || len(data) > MaxArchiveBytes {
		return Manifest{}, errors.New("archive size outside 1 MiB limit")
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return Manifest{}, fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	seen := make(map[string]struct{})
	count, headers, total := 0, 0, int64(0)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Manifest{}, fmt.Errorf("tar header: %w", err)
		}
		headers++
		if headers > 128 {
			return Manifest{}, errors.New("archive entry limit exceeded")
		}
		name := h.Name
		if h.Typeflag == tar.TypeDir {
			name = strings.TrimSuffix(name, "/")
		}
		if name == "" || len(name) > 256 || strings.ContainsAny(name, "\\\x00") || strings.HasPrefix(name, "/") || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") {
			return Manifest{}, errors.New("unsafe archive path")
		}
		if _, duplicate := seen[name]; duplicate {
			return Manifest{}, errors.New("duplicate archive path")
		}
		seen[name] = struct{}{}
		if h.Typeflag == tar.TypeDir {
			continue
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
			return Manifest{}, errors.New("only regular files and directories are accepted")
		}
		count++
		if count > 128 || h.Size < 0 || h.Size > MaxArchiveBytes || total+h.Size > 4<<20 {
			return Manifest{}, errors.New("archive content limit exceeded")
		}
		if _, err := io.CopyN(io.Discard, tr, h.Size); err != nil {
			return Manifest{}, fmt.Errorf("truncated archive: %w", err)
		}
		total += h.Size
	}
	if count == 0 {
		return Manifest{}, errors.New("archive has no files")
	}
	// tar.Reader stops at the end marker before gzip has checked its trailer.
	// Drain a bounded remainder so CRC errors and appended decompression bombs fail.
	remainder, err := io.Copy(io.Discard, io.LimitReader(gz, (8<<20)+1))
	if err != nil {
		return Manifest{}, fmt.Errorf("gzip trailer: %w", err)
	}
	if remainder > 8<<20 {
		return Manifest{}, errors.New("archive trailing content limit exceeded")
	}
	digest := sha256.Sum256(data)
	return Manifest{Digest: hex.EncodeToString(digest[:]), Size: len(data), Files: count}, nil
}
