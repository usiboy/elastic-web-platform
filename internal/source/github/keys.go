package github

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
)

// LoadPrivateKey never logs or returns PEM bytes. The file must be readable
// only by its owner, so neither a build worker nor a tenant runtime needs it.
func LoadPrivateKey(file string) (*rsa.PrivateKey, error) {
	info, err := os.Stat(file)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("GitHub App key must be a private regular file")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("GitHub App key is not PEM")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("invalid RSA private key")
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("GitHub App key must be RSA")
	}
	return key, nil
}
