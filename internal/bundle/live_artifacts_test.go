package bundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLiveOperatorFixtureOutputsFromTwoRealRepositories(t *testing.T) {
	root := os.Getenv("EWP_LIVE_GITHUB_OUTPUT_DIR")
	if root == "" {
		t.Skip("set EWP_LIVE_GITHUB_OUTPUT_DIR for explicit, trusted fixed-artifact validation")
	}
	cases := []struct{ project, filename, sourceSHA, builder string }{
		{"p_alpha", "testA-built-b7dc40fd3655.tar.gz", "06eb9696ea468930721f5b4f3eff2d90819ef3553385ca2a0a342b4585c0eaa7", "operator-fixture:node22-vite7"},
		{"p_beta", "testB-built-no-treeshake-9ea1fc0c7037.tar.gz", "044e7ed94cdcb02c1b1ff6e6654a12ba8e89d46d9eda7e55ce7eaf5cd30d906a", "operator-fixture:node22-vite7-no-treeshake"},
	}
	for _, tc := range cases {
		t.Run(tc.project, func(t *testing.T) {
			archive, err := os.ReadFile(filepath.Join(root, tc.filename))
			if err != nil {
				t.Fatal(err)
			}
			b, err := CaptureBuiltArchive("t_"+strings.TrimPrefix(tc.project, "p_"), tc.project, tc.sourceSHA, tc.builder, archive)
			if err != nil || b.Verify() != nil || len(b.Manifest.Files) != 3 {
				t.Fatalf("real build output verification: %v", err)
			}
			index, ok := b.Read("/index.html")
			if !ok || !strings.Contains(string(index), "/assets/") {
				t.Fatal("built HTML does not reference captured static assets")
			}
			for _, asset := range b.Manifest.Files {
				t.Logf("asset=%s bytes=%d sha256=%s", asset.Path, asset.Size, asset.SHA256)
			}
			t.Logf("bundleDigest=%s sourceSha256=%s builderRef=%s", b.Manifest.Digest, b.Manifest.SourceSHA256, b.Manifest.BuilderDigest)
		})
	}
}
