package bundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"testing"
)

func fixtureBuildArchive(t *testing.T, entries []struct {
	name string
	kind byte
	body string
}) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		kind := entry.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		if err := tw.WriteHeader(&tar.Header{Name: entry.name, Typeflag: kind, Mode: 0644, Size: int64(len(entry.body))}); err != nil {
			t.Fatal(err)
		}
		if len(entry.body) > 0 {
			if _, err := tw.Write([]byte(entry.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestCaptureBuiltArchiveAcceptsOnlyActualStaticOutputBytes(t *testing.T) {
	data := fixtureBuildArchive(t, []struct {
		name string
		kind byte
		body string
	}{
		{name: "./", kind: tar.TypeDir},
		{name: "./assets/", kind: tar.TypeDir},
		{name: "./index.html", body: `<script src="/assets/app.js"></script>`},
		{name: "./assets/app.js", body: "console.log(42)"},
	})
	b, err := CaptureBuiltArchive("t_alpha", "p_beta", "source-sha", "operator-fixture:node22-vite7", data)
	if err != nil || b.Verify() != nil {
		t.Fatalf("trusted fixture byte capture: %v", err)
	}
	if got, _ := b.Read("/assets/app.js"); string(got) != "console.log(42)" {
		t.Fatalf("actual JS bytes changed: %q", got)
	}
	if len(b.Manifest.Files) != 2 {
		t.Fatalf("unexpected files: %+v", b.Manifest.Files)
	}
}

func TestCaptureBuiltArchiveRejectsUnsafePathsAndLinks(t *testing.T) {
	for _, entry := range []struct {
		name string
		kind byte
		body string
	}{
		{name: "../secret", body: "key"},
		{name: "./.env", body: "key"},
		{name: "./assets/alias", kind: tar.TypeSymlink},
		{name: "./assets/alias", kind: tar.TypeLink},
	} {
		data := fixtureBuildArchive(t, []struct {
			name string
			kind byte
			body string
		}{{name: "./index.html", body: "<h1>safe</h1>"}, entry})
		if _, err := CaptureBuiltArchive("t_alpha", "p_beta", "source-sha", "operator-fixture", data); err == nil {
			t.Fatalf("unsafe build entry %q accepted", entry.name)
		}
	}
}
