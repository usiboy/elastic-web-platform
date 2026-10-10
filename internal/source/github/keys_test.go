package github

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPrivateKeyRequiresProtectedRSAFile(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "app.pem")
	block := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := os.WriteFile(file, block, 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadPrivateKey(file); err != nil || got.PublicKey.N.Cmp(key.PublicKey.N) != 0 {
		t.Fatalf("private RSA key: %v", err)
	}
	if err := os.Chmod(file, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPrivateKey(file); err == nil {
		t.Fatal("world-readable GitHub App key accepted")
	}
	if err := os.Chmod(file, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("not a PEM"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPrivateKey(file); err == nil {
		t.Fatal("malformed App key accepted")
	}
}
