package main

import (
	"context"
	"crypto/tls"
	"errors"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"elastic-web-platform/db/migrations"
	"elastic-web-platform/internal/bundle"
	"elastic-web-platform/internal/httpapi"
	"elastic-web-platform/internal/source/github"
	"elastic-web-platform/internal/source/githubactions"
	"elastic-web-platform/internal/source/uploads"

	"github.com/jackc/pgx/v5/pgxpool"
)

type config struct {
	address, databaseURL, bootstrapToken, tlsCert, tlsKey            string
	githubAppID, githubKeyFile, githubWebhookSecret                  string
	artifactStoreDir, previewSuffix                                  string
	productionSuffix                                                 string
	oauthClientID, oauthClientSecret, oauthCallbackURL, oauthPKCEKey string
}

var validPreviewSuffix = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*[a-z0-9]$`)

func validateConfig(c config) error {
	if c.databaseURL == "" || c.bootstrapToken == "" {
		return errors.New("DATABASE_URL and BOOTSTRAP_TOKEN are required")
	}
	host, _, err := net.SplitHostPort(c.address)
	if err != nil {
		return err
	}
	if (c.tlsCert == "") != (c.tlsKey == "") {
		return errors.New("TLS_CERT_FILE and TLS_KEY_FILE must be set together")
	}
	if c.githubAppID != "" || c.githubKeyFile != "" || c.githubWebhookSecret != "" {
		id, err := strconv.ParseInt(c.githubAppID, 10, 64)
		if err != nil || id <= 0 || c.githubKeyFile == "" || c.githubWebhookSecret == "" {
			return errors.New("GITHUB_APP_ID, GITHUB_APP_PRIVATE_KEY_FILE and GITHUB_WEBHOOK_SECRET must all be configured")
		}
	}
	if c.oauthClientID != "" || c.oauthClientSecret != "" || c.oauthCallbackURL != "" || c.oauthPKCEKey != "" {
		callback, err := url.Parse(c.oauthCallbackURL)
		if c.githubAppID == "" || c.githubKeyFile == "" || c.githubWebhookSecret == "" || c.oauthClientID == "" || c.oauthClientSecret == "" || len(c.oauthPKCEKey) < 32 || c.tlsCert == "" || c.tlsKey == "" || err != nil || callback.Scheme != "https" || callback.Host == "" || callback.User != nil || callback.Path != "/v1/oauth/github/callback" || callback.RawQuery != "" || callback.Fragment != "" {
			return errors.New("GitHub OAuth requires App credentials, a private PKCE key and an exact HTTPS callback")
		}
	}
	if (c.artifactStoreDir == "") != (c.previewSuffix == "") {
		return errors.New("ARTIFACT_STORE_DIR and SITE_PREVIEW_SUFFIX must be set together")
	}
	if c.previewSuffix != "" && (len(c.previewSuffix) > 180 || !validPreviewSuffix.MatchString(c.previewSuffix) || strings.Contains(c.previewSuffix, "..")) {
		return errors.New("invalid SITE_PREVIEW_SUFFIX")
	}
	if c.productionSuffix != "" && (c.artifactStoreDir == "" || c.previewSuffix == "" || len(c.productionSuffix) > 180 || !validPreviewSuffix.MatchString(c.productionSuffix) || strings.Contains(c.productionSuffix, "..")) {
		return errors.New("SITE_PRODUCTION_SUFFIX requires valid static delivery configuration")
	}
	ip := net.ParseIP(host)
	if c.tlsCert == "" && host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("plain HTTP listener must bind to loopback")
	}
	return nil
}

func main() {
	address := os.Getenv("API_LISTEN_ADDR")
	if address == "" {
		address = "127.0.0.1:8097"
	}
	c := config{
		address:             address,
		databaseURL:         os.Getenv("DATABASE_URL"),
		bootstrapToken:      os.Getenv("BOOTSTRAP_TOKEN"),
		tlsCert:             os.Getenv("TLS_CERT_FILE"),
		tlsKey:              os.Getenv("TLS_KEY_FILE"),
		githubAppID:         os.Getenv("GITHUB_APP_ID"),
		githubKeyFile:       os.Getenv("GITHUB_APP_PRIVATE_KEY_FILE"),
		githubWebhookSecret: os.Getenv("GITHUB_WEBHOOK_SECRET"),
		artifactStoreDir:    os.Getenv("ARTIFACT_STORE_DIR"),
		previewSuffix:       os.Getenv("SITE_PREVIEW_SUFFIX"),
		productionSuffix:    os.Getenv("SITE_PRODUCTION_SUFFIX"),
		oauthClientID:       os.Getenv("GITHUB_OAUTH_CLIENT_ID"),
		oauthClientSecret:   os.Getenv("GITHUB_OAUTH_CLIENT_SECRET"),
		oauthCallbackURL:    os.Getenv("GITHUB_OAUTH_CALLBACK_URL"),
		oauthPKCEKey:        os.Getenv("GITHUB_OAUTH_PKCE_KEY"),
	}
	if err := validateConfig(c); err != nil {
		log.Fatal("invalid API configuration: ", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := pgxpool.New(ctx, c.databaseURL)
	if err != nil {
		log.Fatal("database connection: ", err)
	}
	defer db.Close()
	if err := migrations.Apply(ctx, db); err != nil {
		log.Fatal("database migration: ", err)
	}
	var options []httpapi.Option
	if dir := os.Getenv("SOURCE_STORE_DIR"); dir != "" {
		options = append(options, httpapi.WithSourceStore(uploads.NewDiskStore(dir)))
	}
	if c.artifactStoreDir != "" {
		options = append(options, httpapi.WithArtifactStore(bundle.NewDiskStore(c.artifactStoreDir)), httpapi.WithPreviewSuffix(c.previewSuffix))
		if c.productionSuffix != "" {
			options = append(options, httpapi.WithProductionSuffix(c.productionSuffix))
		}
	}
	if c.githubAppID != "" {
		key, err := github.LoadPrivateKey(c.githubKeyFile)
		if err != nil {
			log.Fatal("invalid GitHub App key: ", err)
		}
		id, _ := strconv.ParseInt(c.githubAppID, 10, 64)
		options = append(options, httpapi.WithGitHubVerifier(github.NewClient(id, key, "https://api.github.com", nil)), httpapi.WithGitHubWebhookSecret(c.githubWebhookSecret))
		if c.oauthClientID != "" {
			options = append(options, httpapi.WithGitHubOAuth(github.NewOAuthClient(c.oauthClientID, c.oauthClientSecret, c.oauthCallbackURL, []byte(c.oauthPKCEKey), "https://github.com", "https://github.com", "https://api.github.com", nil)))
		}
	}
	if os.Getenv("EWP_ACTIONS_OIDC_PILOT") == "1" {
		options = append(options, httpapi.WithGitHubActionsPilot(githubactions.NewVerifier(), func(installationID, repositoryID int64, token string) (httpapi.GitHubActionsSource, error) {
			return github.NewScopedTokenClient(installationID, []github.ScopedToken{{InstallationID: installationID, RepositoryID: repositoryID,
				Token: token, ExpiresAt: time.Now().Add(20 * time.Minute)}}, "https://api.github.com", nil)
		}))
	}
	srv := &http.Server{
		Addr: c.address, Handler: httpapi.NewServer(db, c.bootstrapToken, options...),
		ReadHeaderTimeout: 5 * time.Second, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}
	if c.tlsCert != "" {
		err = srv.ListenAndServeTLS(c.tlsCert, c.tlsKey)
	} else {
		err = srv.ListenAndServe()
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal("serve API: ", err)
	}
}
