package bundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestCaptureStaticCopiesAndVerifiesActualAssetBytes(t *testing.T) {
	index := []byte("<h1>one</h1>")
	b, err := CaptureStatic("t_alpha", "p_beta", "source-sha", "builtin/static-copy:v1", map[string][]byte{"/index.html": index, "/assets/site.js": []byte("console.log(1)")})
	if err != nil {
		t.Fatal(err)
	}
	index[4] = 'X'
	if err := b.Verify(); err != nil {
		t.Fatal(err)
	}
	got, ok := b.Read("/index.html")
	if !ok || !bytes.Equal(got, []byte("<h1>one</h1>")) {
		t.Fatalf("source mutated deployed asset: %q", got)
	}
	got[0] = 'X'
	if err := b.Verify(); err != nil {
		t.Fatalf("read exposed mutable bundle: %v", err)
	}
	if b.Manifest.Digest == "" || b.Manifest.Files[0].SHA256 == "" {
		t.Fatalf("incomplete manifest: %+v", b.Manifest)
	}
	b.data["/index.html"][0] = 'X'
	if err := b.Verify(); err == nil {
		t.Fatal("tampered bundle passed verification")
	}
}

func TestCaptureStaticHTMLSourceIntoBundleWithoutExecutingCode(t *testing.T) {
	var source bytes.Buffer
	gz := gzip.NewWriter(&source)
	tw := tar.NewWriter(gz)
	for _, asset := range []struct{ name, body string }{{"index.html", "<h1>Website</h1>"}, {"app.js", "console.log(1)"}} {
		if err := tw.WriteHeader(&tar.Header{Name: asset.name, Mode: 0644, Size: int64(len(asset.body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(asset.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := CaptureStaticSource("t_alpha", "p_beta", source.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	sha := sha256.Sum256(source.Bytes())
	if b.Manifest.SourceSHA256 != hex.EncodeToString(sha[:]) || b.Manifest.BuilderDigest != "builtin/static-copy:v1" {
		t.Fatalf("provenance mismatch: %+v", b.Manifest)
	}
	if got, ok := b.Read("/index.html"); !ok || string(got) != "<h1>Website</h1>" {
		t.Fatalf("website bytes: %q", got)
	}
}

func TestCaptureStaticSourceRejectsBuildRequiredProject(t *testing.T) {
	var source bytes.Buffer
	gz := gzip.NewWriter(&source)
	tw := tar.NewWriter(gz)
	for _, asset := range []struct{ name, body string }{
		{"index.html", "<h1>not yet built</h1>"},
		{"package.json", `{"scripts":{"build":"vite build"},"dependencies":{"vue":"3.5.0","vite":"6.0.0"}}`},
		{"package-lock.json", `{"lockfileVersion":3,"packages":{}}`},
	} {
		if err := tw.WriteHeader(&tar.Header{Name: asset.name, Mode: 0644, Size: int64(len(asset.body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(asset.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := CaptureStaticSource("t_alpha", "p_beta", source.Bytes()); err == nil {
		t.Fatal("Vite source treated as ready static output")
	}
}

func TestStaticBundleDoesNotPublishHiddenOrBuildConfigurationFiles(t *testing.T) {
	for _, asset := range []string{"/.env", "/assets/.secret", "/package-lock.json", "/Dockerfile", "/app.js.map"} {
		if _, err := CaptureStatic("t_alpha", "p_beta", "source-sha", "builtin/static-copy:v1", map[string][]byte{"/index.html": []byte("<h1>Site</h1>"), asset: []byte("private")}); err == nil {
			t.Fatalf("sensitive static path %s was published", asset)
		}
	}
}

func TestCaptureStaticRejectsUnsafeOrMissingIndex(t *testing.T) {
	for _, assets := range []map[string][]byte{
		{"/assets/a.js": []byte("x")},
		{"/index.html": []byte("x"), "/../secret": []byte("oops")},
		{"/index.html": []byte("x"), "/link\\escape": []byte("x")},
		{"/index.html": []byte("x"), "/big": bytes.Repeat([]byte("x"), 5<<20)},
	} {
		if _, err := CaptureStatic("t_alpha", "p_beta", "source-sha", "builtin/static-copy:v1", assets); err == nil {
			t.Fatal("invalid asset bundle accepted")
		}
	}
}
