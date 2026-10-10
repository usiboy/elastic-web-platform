package githubactions

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const issuer = "https://token.actions.githubusercontent.com"
const jwksURL = issuer + "/.well-known/jwks"

type Pilot struct {
	RepositoryID string
	Repository   string
	OwnerID      string
	WorkflowRef  string
	Audience     string
}

type Claims struct {
	Issuer            string `json:"iss"`
	Audience          string `json:"aud"`
	Subject           string `json:"sub"`
	Repository        string `json:"repository"`
	RepositoryID      string `json:"repository_id"`
	RepositoryOwnerID string `json:"repository_owner_id"`
	WorkflowRef       string `json:"workflow_ref"`
	Ref               string `json:"ref"`
	SHA               string `json:"sha"`
	EventName         string `json:"event_name"`
	RunID             string `json:"run_id"`
	RunAttempt        string `json:"run_attempt"`
	JTI               string `json:"jti"`
	IssuedAt          int64  `json:"iat"`
	NotBefore         int64  `json:"nbf"`
	ExpiresAt         int64  `json:"exp"`
}

// Verifier has no GitHub App private key. Only the two explicitly approved
// public repositories can pass its trust policy; OIDC alone does not attest
// the bytes in an uploaded artifact.
type Verifier struct {
	client *http.Client
	jwks   string
	now    func() time.Time
}

func NewVerifier() *Verifier {
	client := &http.Client{Timeout: 5 * time.Second}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Verifier{client: client, jwks: jwksURL, now: time.Now}
}

func (v *Verifier) Verify(ctx context.Context, token string, pilot Pilot) (Claims, error) {
	var claims Claims
	if v == nil || v.client == nil || pilot.RepositoryID == "" || pilot.OwnerID == "" || pilot.Repository == "" || pilot.Audience == "" ||
		pilot.WorkflowRef != pilot.Repository+"/.github/workflows/ewp-preview.yml@refs/heads/main" || len(token) < 100 || len(token) > 8192 {
		return claims, errors.New("OIDC verifier is not configured")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || len(parts[0]) > 1024 || len(parts[1]) > 4096 || len(parts[2]) > 1024 {
		return claims, errors.New("invalid OIDC token format")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return claims, errors.New("invalid OIDC header")
	}
	var header struct{ Alg, Kid, Typ string }
	if json.Unmarshal(headerBytes, &header) != nil || header.Alg != "RS256" || header.Kid == "" || len(header.Kid) > 256 || header.Typ != "JWT" {
		return claims, errors.New("unsupported OIDC signing algorithm")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwks, nil)
	if err != nil {
		return claims, err
	}
	response, err := v.client.Do(req)
	if err != nil {
		return claims, errors.New("GitHub OIDC signing keys unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Request.URL.String() != v.jwks || response.Header.Get("Content-Type") == "text/html" {
		return claims, errors.New("GitHub OIDC signing keys unavailable")
	}
	keyBytes, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil || len(keyBytes) > 64<<10 {
		return claims, errors.New("GitHub OIDC key set too large")
	}
	var keys struct {
		Keys []struct {
			Kty string `json:"kty"`
			Use string `json:"use"`
			Alg string `json:"alg"`
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if json.Unmarshal(keyBytes, &keys) != nil || len(keys.Keys) == 0 || len(keys.Keys) > 16 {
		return claims, errors.New("invalid GitHub OIDC key set")
	}
	var verified bool
	for _, key := range keys.Keys {
		if key.Kid != header.Kid || key.Kty != "RSA" || key.Use != "sig" || key.Alg != "RS256" {
			continue
		}
		modulus, e1 := base64.RawURLEncoding.DecodeString(key.N)
		exponent, e2 := base64.RawURLEncoding.DecodeString(key.E)
		if e1 != nil || e2 != nil || len(modulus) < 256 || len(modulus) > 512 || len(exponent) == 0 || len(exponent) > 4 {
			continue
		}
		e := new(big.Int).SetBytes(exponent)
		if !e.IsInt64() || e.Int64() < 3 || e.Int64() > 65537 || e.Int64()%2 == 0 {
			continue
		}
		signature, err := base64.RawURLEncoding.DecodeString(parts[2])
		if err != nil {
			break
		}
		digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		if rsa.VerifyPKCS1v15(&rsa.PublicKey{N: new(big.Int).SetBytes(modulus), E: int(e.Int64())}, crypto.SHA256, digest[:], signature) == nil {
			verified = true
			break
		}
	}
	if !verified {
		return claims, errors.New("GitHub OIDC signature verification failed")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || json.Unmarshal(payload, &claims) != nil {
		return Claims{}, errors.New("invalid OIDC claims")
	}
	now := v.now().Unix()
	if claims.Issuer != issuer || claims.Audience != pilot.Audience || claims.RepositoryID != pilot.RepositoryID ||
		claims.RepositoryOwnerID != pilot.OwnerID || claims.Repository != pilot.Repository || claims.WorkflowRef != pilot.WorkflowRef ||
		claims.Subject != "repo:"+strings.Replace(pilot.Repository, "/", "@"+pilot.OwnerID+"/", 1)+"@"+pilot.RepositoryID+":ref:refs/heads/main" || claims.Ref != "refs/heads/main" ||
		claims.EventName != "push" || !validSHA(claims.SHA) || claims.IssuedAt > now+30 || claims.IssuedAt < now-600 ||
		claims.ExpiresAt <= now || claims.ExpiresAt > now+600 || claims.NotBefore > now+30 || (claims.JTI != "" && (len(claims.JTI) < 8 || len(claims.JTI) > 128)) ||
		!positiveID(claims.RunID) || !positiveID(claims.RunAttempt) {
		return Claims{}, errors.New("GitHub Actions job is outside the approved pilot scope")
	}
	return claims, nil
}

func validSHA(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, ch := range value {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

func positiveID(value string) bool {
	id, err := strconv.ParseInt(value, 10, 64)
	return err == nil && id > 0 && len(value) < 20 && fmt.Sprint(id) == value
}
