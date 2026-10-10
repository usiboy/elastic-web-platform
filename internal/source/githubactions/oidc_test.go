package githubactions

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPilotRequiresSignatureAndExactRepositoryWorkflowBranch(t *testing.T) {
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	encode := func(value any) string {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	keys := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "use": "sig", "alg": "RS256", "kid": "pilot-key", "n": base64.RawURLEncoding.EncodeToString(private.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(private.E)).Bytes()),
		}}})
	}))
	defer keys.Close()
	verifier := NewVerifier()
	verifier.jwks = keys.URL
	verifier.now = func() time.Time { return time.Unix(1750000000, 0) }
	policy := Pilot{RepositoryID: "1404606559", Repository: "usiboy/testA", OwnerID: "1673411",
		WorkflowRef: "usiboy/testA/.github/workflows/ewp-preview.yml@refs/heads/main", Audience: "ewp-ecs-testA"}
	claims := Claims{Issuer: issuer, Audience: policy.Audience, Subject: "repo:usiboy@1673411/testA@1404606559:ref:refs/heads/main",
		Repository: policy.Repository, RepositoryID: policy.RepositoryID, RepositoryOwnerID: policy.OwnerID,
		WorkflowRef: policy.WorkflowRef, Ref: "refs/heads/main", EventName: "push", SHA: strings.Repeat("a", 40),
		RunID: "555", RunAttempt: "1", JTI: "some-unique-jti", IssuedAt: verifier.now().Unix(), ExpiresAt: verifier.now().Unix() + 300}
	sign := func(value Claims) string {
		header := encode(map[string]string{"alg": "RS256", "kid": "pilot-key", "typ": "JWT"})
		body := encode(value)
		digest := sha256.Sum256([]byte(header + "." + body))
		signature, err := rsa.SignPKCS1v15(rand.Reader, private, crypto.SHA256, digest[:])
		if err != nil {
			t.Fatal(err)
		}
		return header + "." + body + "." + base64.RawURLEncoding.EncodeToString(signature)
	}
	if result, err := verifier.Verify(context.Background(), sign(claims), policy); err != nil || result.SHA != claims.SHA {
		t.Fatalf("valid A push rejected: %v", err)
	}
	noJTI := claims
	noJTI.JTI = ""
	if _, err := verifier.Verify(context.Background(), sign(noJTI), policy); err != nil {
		t.Fatalf("GitHub discovery does not require a jti claim: %v", err)
	}
	for name, mutate := range map[string]func(*Claims){
		"default_subject": func(c *Claims) { c.Subject = "repo:usiboy/testA:ref:refs/heads/main" },
		"repo_id":         func(c *Claims) { c.RepositoryID = "1404606859" },
		"workflow":        func(c *Claims) { c.WorkflowRef = "usiboy/testA/.github/workflows/verify.yml@refs/heads/main" },
		"audience":        func(c *Claims) { c.Audience = "other-audience" },
		"PR_event":        func(c *Claims) { c.EventName = "pull_request" },
		"branch":          func(c *Claims) { c.Ref = "refs/heads/feature" },
		"expiry":          func(c *Claims) { c.ExpiresAt = verifier.now().Unix() - 1 },
		"future":          func(c *Claims) { c.IssuedAt = verifier.now().Unix() + 120 },
	} {
		candidate := claims
		mutate(&candidate)
		if _, err := verifier.Verify(context.Background(), sign(candidate), policy); err == nil {
			t.Errorf("%s bypassed pilot policy", name)
		}
	}
	forged := strings.Split(sign(claims), ".")
	forged[1] = encode(func() Claims { copy := claims; copy.SHA = strings.Repeat("b", 40); return copy }())
	if _, err := verifier.Verify(context.Background(), strings.Join(forged, "."), policy); err == nil {
		t.Fatal("tampered SHA without new signature was accepted")
	}
}
