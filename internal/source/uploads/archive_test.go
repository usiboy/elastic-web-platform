package uploads

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type entry struct {
	name     string
	typeFlag byte
	data     string
}

func archive(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		kind := e.typeFlag
		if kind == 0 {
			kind = tar.TypeReg
		}
		if err := tw.WriteHeader(&tar.Header{Name: e.name, Typeflag: kind, Size: int64(len(e.data)), Mode: 0644}); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tw, e.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestValidateArchiveRejectsUnsafeEntries(t *testing.T) {
	cases := []struct {
		name     string
		files    []entry
		accepted bool
	}{
		{"normal project", []entry{{name: "index.html", data: "<h1>hello</h1>"}, {name: "src/app.js", data: "ok"}}, true},
		{"traversal", []entry{{name: "../secret", data: "x"}}, false},
		{"absolute path", []entry{{name: "/tmp/secret", data: "x"}}, false},
		{"backslash", []entry{{name: `src\app.js`, data: "x"}}, false},
		{"symlink", []entry{{name: "escape", typeFlag: tar.TypeSymlink}}, false},
		{"duplicate", []entry{{name: "index.html", data: "a"}, {name: "index.html", data: "b"}}, false},
		{"empty", nil, false},
		{"inflated size", []entry{{name: "huge.txt", data: strings.Repeat("x", 4<<20+1)}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := archive(t, tc.files...)
			manifest, err := Validate(data)
			if (err == nil) != tc.accepted {
				t.Fatalf("accepted=%v, err=%v", err == nil, err)
			}
			if tc.accepted {
				digest := sha256.Sum256(data)
				if manifest.Digest != hex.EncodeToString(digest[:]) || manifest.Files != len(tc.files) {
					t.Fatalf("wrong manifest: %+v", manifest)
				}
			}
		})
	}
}

func TestDiskStoreReadsBackImmutableArchive(t *testing.T) {
	root := t.TempDir()
	store := NewDiskStore(root)
	data := archive(t, entry{name: "index.html", data: "hello"})
	digest := sha256.Sum256(data)
	key := hex.EncodeToString(digest[:])
	if err := store.Put(context.Background(), "t_aaa", "p_bbb", key, data); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(context.Background(), "t_aaa", "p_bbb", key)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("read back: %v", err)
	}
	if err := store.Put(context.Background(), "t_aaa", "p_bbb", key, []byte("different")); err == nil {
		t.Fatal("same digest accepted altered bytes")
	}
	if _, err := store.Get(context.Background(), "t_aaa", "p_other", key); !os.IsNotExist(err) {
		t.Fatalf("cross-project object visible: %v", err)
	}
	if matches, err := filepath.Glob(filepath.Join(root, "t_aaa", "p_bbb", "*")); err != nil || len(matches) != 1 {
		t.Fatalf("unexpected stored files: %v %v", matches, err)
	}
}

func TestValidateArchiveChecksGzipChecksum(t *testing.T) {
	data := archive(t, entry{name: "index.html", data: "hello"})
	data[len(data)-1] ^= 0xff
	if _, err := Validate(data); err == nil {
		t.Fatal("tampered gzip footer accepted")
	}
}

func TestValidateArchiveLimitsDirectoryEntriesToo(t *testing.T) {
	entries := make([]entry, 129)
	for i := range entries {
		entries[i] = entry{name: fmt.Sprintf("dir-%d/", i), typeFlag: tar.TypeDir}
	}
	entries = append(entries, entry{name: "index.html", data: "hello"})
	if _, err := Validate(archive(t, entries...)); err == nil {
		t.Fatal("unbounded directory headers accepted")
	}
}
