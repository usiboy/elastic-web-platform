package github

import (
	"context"
	"crypto/hmac"
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

// OAuthClient exchanges short-lived GitHub App user authorization codes and
// checks the intersection of a user's and installation's repository access.
// User tokens are returned only to the caller, never stored by this client.
type OAuthClient struct {
	clientID, clientSecret, callbackURL string
	pkceKey                             []byte
	authorizeBase, tokenBase, apiBase   string
	http                                *http.Client
}

var ErrUserRepoDenied = errors.New("GitHub user cannot read the requested repository")

func NewOAuthClient(clientID, clientSecret, callbackURL string, pkceKey []byte, authorizeBase, tokenBase, apiBase string, transport *http.Client) *OAuthClient {
	if transport == nil {
		transport = &http.Client{}
	}
	copy := *transport
	if copy.Timeout == 0 {
		copy.Timeout = 10 * time.Second
	}
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &OAuthClient{clientID: clientID, clientSecret: clientSecret, callbackURL: callbackURL, pkceKey: append([]byte(nil), pkceKey...), authorizeBase: strings.TrimRight(authorizeBase, "/"), tokenBase: strings.TrimRight(tokenBase, "/"), apiBase: strings.TrimRight(apiBase, "/"), http: &copy}
}

func (c *OAuthClient) validate() error {
	callback, err := url.Parse(c.callbackURL)
	if c.clientID == "" || c.clientSecret == "" || len(c.pkceKey) < 32 || c.authorizeBase == "" || c.tokenBase == "" || c.apiBase == "" || err != nil || callback.Scheme != "https" || callback.Host == "" || callback.User != nil || callback.RawQuery != "" || callback.Fragment != "" || callback.Path != "/v1/oauth/github/callback" {
		return errors.New("GitHub user authorization is not configured")
	}
	return nil
}

func (c *OAuthClient) verifier(state string) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	if len(state) < 16 || len(state) > 128 || !strings.HasPrefix(state, "ghs_") {
		return "", errors.New("invalid OAuth state")
	}
	mac := hmac.New(sha256.New, c.pkceKey)
	_, _ = mac.Write([]byte("github-pkce-v1:" + state))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (c *OAuthClient) AuthorizeURL(state string) (string, error) {
	verifier, err := c.verifier(state)
	if err != nil {
		return "", err
	}
	challenge := sha256.Sum256([]byte(verifier))
	query := url.Values{"client_id": {c.clientID}, "redirect_uri": {c.callbackURL}, "state": {state}, "code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}, "code_challenge_method": {"S256"}}
	return c.authorizeBase + "/login/oauth/authorize?" + query.Encode(), nil
}

func (c *OAuthClient) ExchangeUserCode(ctx context.Context, code, state string, repositoryID int64) (string, error) {
	verifier, err := c.verifier(state)
	if err != nil || code == "" || len(code) > 512 || repositoryID <= 0 {
		return "", errors.New("invalid OAuth exchange")
	}
	form := url.Values{"client_id": {c.clientID}, "client_secret": {c.clientSecret}, "code": {code}, "redirect_uri": {c.callbackURL}, "code_verifier": {verifier}, "repository_id": {strconv.FormatInt(repositoryID, 10)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenBase+"/login/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub OAuth HTTP %d", resp.StatusCode)
	}
	var result struct {
		Token     string `json:"access_token"`
		TokenType string `json:"token_type"`
		Error     string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&result); err != nil {
		return "", errors.New("invalid GitHub OAuth response")
	}
	if result.Error != "" || result.Token == "" || !strings.EqualFold(result.TokenType, "bearer") {
		return "", errors.New("GitHub user authorization was denied")
	}
	return result.Token, nil
}

// UserIdentity returns only the numeric actor ID required for revocation.
// The short-lived user access token is never persisted or returned by the API.
func (c *OAuthClient) UserIdentity(ctx context.Context, userToken string) (int64, error) {
	if err := c.validate(); err != nil {
		return 0, err
	}
	if userToken == "" {
		return 0, errors.New("missing GitHub user token")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBase+"/user", nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+userToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("GitHub user identity HTTP %d", resp.StatusCode)
	}
	var actor struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&actor); err != nil || actor.ID <= 0 || actor.Login == "" {
		return 0, errors.New("invalid GitHub user identity")
	}
	return actor.ID, nil
}

func (c *OAuthClient) UserCanReadRepository(ctx context.Context, userToken string, installationID, repositoryID int64) (Repository, error) {
	if err := c.validate(); err != nil {
		return Repository{}, err
	}
	if userToken == "" || installationID <= 0 || repositoryID <= 0 {
		return Repository{}, errors.New("invalid repository authorization")
	}
	for page := 1; page <= 20; page++ {
		endpoint := fmt.Sprintf("%s/user/installations/%d/repositories?per_page=100&page=%d", c.apiBase, installationID, page)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return Repository{}, err
		}
		req.Header.Set("Authorization", "Bearer "+userToken)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		resp, err := c.http.Do(req)
		if err != nil {
			return Repository{}, err
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
				return Repository{}, ErrUserRepoDenied
			}
			return Repository{}, fmt.Errorf("GitHub user repositories HTTP %d", resp.StatusCode)
		}
		var listing struct {
			Total        int `json:"total_count"`
			Repositories []struct {
				Repository
				Permissions struct {
					Pull bool `json:"pull"`
				} `json:"permissions"`
			} `json:"repositories"`
		}
		decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&listing)
		_ = resp.Body.Close()
		if decodeErr != nil || listing.Total < 0 || len(listing.Repositories) > 100 {
			return Repository{}, errors.New("invalid GitHub user repository list")
		}
		for _, entry := range listing.Repositories {
			if entry.ID == repositoryID {
				if !entry.Permissions.Pull || entry.FullName == "" {
					return Repository{}, ErrUserRepoDenied
				}
				return entry.Repository, nil
			}
		}
		if len(listing.Repositories) == 0 || page*100 >= listing.Total {
			break
		}
	}
	return Repository{}, ErrUserRepoDenied
}
