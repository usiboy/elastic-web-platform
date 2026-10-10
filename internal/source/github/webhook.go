package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// VerifySignature authenticates exactly the received webhook bytes before parsing.
func VerifySignature(secret string, body []byte, header string) bool {
	if secret == "" || !strings.HasPrefix(header, "sha256=") || len(header) != len("sha256=")+64 {
		return false
	}
	actual, err := hex.DecodeString(strings.TrimPrefix(header, "sha256="))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return hmac.Equal(actual, mac.Sum(nil))
}
