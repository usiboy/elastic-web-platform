package github

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Repository struct {
	ID            int64  `json:"id"`
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch"`
}

type Client struct {
	appID          int64
	privateKey     *rsa.PrivateKey
	baseURL        string
	http           *http.Client
	installationID int64
	scopedTokens   map[int64]ScopedToken
}

// ScopedToken is a short-lived, repository-specific installation credential.
// It must be minted on a trusted operator machine; never print or commit Token.
type ScopedToken struct {
	InstallationID int64     `json:"installationId"`
	RepositoryID   int64     `json:"repositoryId"`
	Token          string    `json:"token"`
	ExpiresAt      time.Time `json:"expiresAt"`
}

func NewClient(appID int64, privateKey *rsa.PrivateKey, baseURL string, transport *http.Client) *Client {
	if transport == nil {
		transport = &http.Client{}
	}
	copy := *transport
	if copy.Timeout == 0 {
		copy.Timeout = 10 * time.Second
	}
	// Never forward the app JWT or installation token to a redirected host.
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{appID: appID, privateKey: privateKey, baseURL: strings.TrimRight(baseURL, "/"), http: &copy}
}

func NewScopedTokenClient(installationID int64, credentials []ScopedToken, baseURL string, transport *http.Client) (*Client, error) {
	if installationID <= 0 || len(credentials) == 0 || len(credentials) > 2 {
		return nil, errors.New("only explicit, bounded repository credentials are accepted")
	}
	c := NewClient(0, nil, baseURL, transport)
	c.installationID = installationID
	c.scopedTokens = make(map[int64]ScopedToken, len(credentials))
	for _, item := range credentials {
		if item.InstallationID != installationID || item.RepositoryID <= 0 || item.ExpiresAt.Before(time.Now().Add(time.Minute)) || item.ExpiresAt.After(time.Now().Add(75*time.Minute)) || len(item.Token) < 16 || len(item.Token) > 1024 || strings.TrimSpace(item.Token) != item.Token || strings.ContainsAny(item.Token, "\r\n") {
			return nil, errors.New("invalid short-lived single-repo installation identity")
		}
		if _, exists := c.scopedTokens[item.RepositoryID]; exists {
			return nil, errors.New("duplicate repository credential")
		}
		c.scopedTokens[item.RepositoryID] = item
	}
	return c, nil
}

// MintScopedRepositoryToken keeps the App private key on the invoking machine.
// The caller must write the returned token only to a private, expiring file.
func (c *Client) MintScopedRepositoryToken(ctx context.Context, installationID, repositoryID int64) (ScopedToken, error) {
	token, expires, permissions, err := c.issueScopedToken(ctx, installationID, repositoryID)
	if err != nil {
		return ScopedToken{}, err
	}
	scoped := ScopedToken{InstallationID: installationID, RepositoryID: repositoryID, Token: token, ExpiresAt: expires}
	if expires.Before(time.Now().Add(5*time.Minute)) || expires.After(time.Now().Add(75*time.Minute)) || permissions["contents"] != "read" || permissions["metadata"] != "read" || len(permissions) != 2 {
		return scoped, errors.New("single-repo token expiry or read-only permissions not confirmed")
	}
	return scoped, nil
}

// RevokeScopedToken invalidates a temporary installation token without ever
// putting the credential in a URL or logging it. The caller clears its file.
func (c *Client) RevokeScopedToken(ctx context.Context, token string) error {
	if len(token) < 16 || len(token) > 1024 || strings.TrimSpace(token) != token || strings.ContainsAny(token, "\r\n") || c.baseURL == "" {
		return errors.New("invalid temporary Git identity")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+"/installation/token", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("GitHub token revocation returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) VerifyRepository(ctx context.Context, installationID, repositoryID int64) (Repository, error) {
	repo, _, err := c.scopedRepository(ctx, installationID, repositoryID)
	return repo, err
}

// CurrentBranchHead re-reads the moving production branch just before an
// automatic promotion. A signed old webhook alone is not enough.
func (c *Client) CurrentBranchHead(ctx context.Context, installationID, repositoryID int64, branch string) (string, error) {
	if branch == "" || len(branch) > 128 {
		return "", errors.New("invalid Git branch")
	}
	segments := strings.Split(branch, "/")
	for i, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return "", errors.New("invalid Git branch")
		}
		segments[i] = url.PathEscape(segment)
	}
	repo, token, err := c.scopedRepository(ctx, installationID, repositoryID)
	if err != nil {
		return "", err
	}
	return c.branchHead(ctx, repo, token, branch, segments)
}

func (c *Client) PublicPilotBranchHead(ctx context.Context, repositoryID int64, branch string) (string, error) {
	if branch != "main" {
		return "", errors.New("only main of an approved public pilot repository can be read")
	}
	repo, err := c.publicPilotRepository(ctx, repositoryID)
	if err != nil {
		return "", err
	}
	return c.branchHead(ctx, repo, "", branch, []string{"main"})
}

func (c *Client) publicPilotRepository(ctx context.Context, repositoryID int64) (Repository, error) {
	want := map[int64]string{1404606559: "usiboy/testA", 1404606859: "usiboy/testB"}[repositoryID]
	if want == "" || c == nil || c.baseURL == "" {
		return Repository{}, errors.New("repository is not an approved public pilot")
	}
	var repo Repository
	if err := c.request(ctx, http.MethodGet, fmt.Sprintf("/repositories/%d", repositoryID), "", nil, 200, &repo); err != nil {
		return Repository{}, err
	}
	if repo.ID != repositoryID || repo.FullName != want || repo.DefaultBranch != "main" {
		return Repository{}, errors.New("public Git repository identity changed")
	}
	return repo, nil
}

func (c *Client) branchHead(ctx context.Context, repo Repository, token, branch string, segments []string) (string, error) {
	var reference struct {
		Ref    string `json:"ref"`
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	path := "/repos/" + repo.FullName + "/git/ref/heads/" + strings.Join(segments, "/")
	if err := c.request(ctx, http.MethodGet, path, token, nil, 200, &reference); err != nil {
		return "", err
	}
	if reference.Ref != "refs/heads/"+branch || !validGitSHA(reference.Object.SHA) {
		return "", errors.New("Git ref identity mismatch")
	}
	return reference.Object.SHA, nil
}

func (c *Client) scopedRepository(ctx context.Context, installationID, repositoryID int64) (Repository, string, error) {
	var token string
	if c.scopedTokens != nil {
		item, ok := c.scopedTokens[repositoryID]
		if installationID != c.installationID || !ok || time.Now().Add(time.Minute).After(item.ExpiresAt) {
			return Repository{}, "", errors.New("repository-scoped identity unavailable or expired")
		}
		var scope struct {
			TotalCount   int `json:"total_count"`
			Repositories []struct {
				ID int64 `json:"id"`
			} `json:"repositories"`
		}
		if err := c.request(ctx, http.MethodGet, "/installation/repositories?per_page=100", item.Token, nil, 200, &scope); err != nil {
			return Repository{}, "", err
		}
		if scope.TotalCount != 1 || len(scope.Repositories) != 1 || scope.Repositories[0].ID != repositoryID {
			return Repository{}, "", errors.New("installation token is not limited to the requested repository")
		}
		token = item.Token
	} else {
		issued, _, _, err := c.issueScopedToken(ctx, installationID, repositoryID)
		if err != nil {
			return Repository{}, "", err
		}
		token = issued
	}
	var repo Repository
	if err := c.request(ctx, http.MethodGet, fmt.Sprintf("/repositories/%d", repositoryID), token, nil, 200, &repo); err != nil {
		return Repository{}, "", err
	}
	if repo.ID != repositoryID || repo.FullName == "" || strings.Count(repo.FullName, "/") != 1 || repo.DefaultBranch == "" {
		return Repository{}, "", errors.New("repository metadata does not match requested identity")
	}
	return repo, token, nil
}

func (c *Client) issueScopedToken(ctx context.Context, installationID, repositoryID int64) (string, time.Time, map[string]string, error) {
	if c.appID <= 0 || installationID <= 0 || repositoryID <= 0 || c.privateKey == nil || c.baseURL == "" {
		return "", time.Time{}, nil, errors.New("github app not configured")
	}
	jwt, err := c.appJWT()
	if err != nil {
		return "", time.Time{}, nil, err
	}
	body, err := json.Marshal(map[string]any{
		"repository_ids": []int64{repositoryID},
		"permissions":    map[string]string{"contents": "read", "metadata": "read"},
	})
	if err != nil {
		return "", time.Time{}, nil, err
	}
	var scoped struct {
		Token        string            `json:"token"`
		ExpiresAt    time.Time         `json:"expires_at"`
		Permissions  map[string]string `json:"permissions"`
		Repositories []struct {
			ID int64 `json:"id"`
		} `json:"repositories"`
	}
	path := fmt.Sprintf("/app/installations/%d/access_tokens", installationID)
	if err := c.request(ctx, http.MethodPost, path, jwt, body, 201, &scoped); err != nil {
		return "", time.Time{}, nil, err
	}
	if scoped.Token == "" || len(scoped.Repositories) != 1 || scoped.Repositories[0].ID != repositoryID {
		return "", time.Time{}, nil, errors.New("scoped installation token did not grant the requested repository")
	}
	return scoped.Token, scoped.ExpiresAt, scoped.Permissions, nil
}

func (c *Client) appJWT() (string, error) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	now := time.Now()
	claims, err := json.Marshal(map[string]any{"iat": now.Add(-time.Minute).Unix(), "exp": now.Add(8 * time.Minute).Unix(), "iss": strconv.FormatInt(c.appID, 10)})
	if err != nil {
		return "", err
	}
	unsigned := header + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(unsigned))
	sig, err := rsa.SignPKCS1v15(rand.Reader, c.privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func (c *Client) request(ctx context.Context, method, path, bearer string, body []byte, wanted int, out any) error {
	return c.requestLimited(ctx, method, path, bearer, body, wanted, out, 64<<10)
}

func (c *Client) requestLimited(ctx context.Context, method, path, bearer string, body []byte, wanted int, out any, limit int64) error {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != wanted {
		return fmt.Errorf("github API returned HTTP %d", resp.StatusCode)
	}
	limited := io.LimitReader(resp.Body, limit)
	if err := json.NewDecoder(limited).Decode(out); err != nil {
		return fmt.Errorf("github API response: %w", err)
	}
	return nil
}
