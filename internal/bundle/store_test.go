package bundle

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDiskStoreReloadsImmutableBundleAndRejectsCorruption(t *testing.T) {
	root := t.TempDir()
	store := NewDiskStore(root)
	b, err := CaptureStatic("t_alpha", "p_beta", "source-sha", "builtin/static-copy:v1", map[string][]byte{"/index.html": []byte("hello")})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	loaded, err := NewDiskStore(root).Get(context.Background(), "t_alpha", "p_beta", b.Manifest.Digest)
	if err != nil || loaded.Verify() != nil {
		t.Fatalf("reloaded: %v", err)
	}
	if _, err := store.Get(context.Background(), "t_other", "p_beta", b.Manifest.Digest); !os.IsNotExist(err) {
		t.Fatalf("cross-team artifact visible: %v", err)
	}
	file := filepath.Join(root, "t_alpha", "p_beta", b.Manifest.Digest+".json")
	if err := os.WriteFile(file, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(context.Background(), "t_alpha", "p_beta", b.Manifest.Digest); err == nil {
		t.Fatal("corrupt bundle loaded")
	}
}
