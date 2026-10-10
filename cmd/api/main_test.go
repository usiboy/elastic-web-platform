package main

import (
	"strings"
	"testing"
)

func TestConfigRejectsPublicPlaintextAndMissingSecrets(t *testing.T) {
	cases := []struct {
		name   string
		config config
		valid  bool
	}{
		{"local", config{address: "127.0.0.1:8097", databaseURL: "postgres://localhost/test", bootstrapToken: "local-token"}, true},
		{"public plaintext", config{address: "0.0.0.0:8097", databaseURL: "postgres://localhost/test", bootstrapToken: "local-token"}, false},
		{"missing database", config{address: "127.0.0.1:8097", bootstrapToken: "local-token"}, false},
		{"missing bootstrap", config{address: "127.0.0.1:8097", databaseURL: "postgres://localhost/test"}, false},
		{"half TLS", config{address: "0.0.0.0:8097", databaseURL: "postgres://localhost/test", bootstrapToken: "local-token", tlsCert: "cert.pem"}, false},
		{"TLS", config{address: "0.0.0.0:8097", databaseURL: "postgres://localhost/test", bootstrapToken: "local-token", tlsCert: "cert.pem", tlsKey: "key.pem"}, true},
		{"app id without private key", config{address: "127.0.0.1:8097", databaseURL: "postgres://localhost/test", bootstrapToken: "local-token", githubAppID: "42"}, false},
		{"webhook without app identity", config{address: "127.0.0.1:8097", databaseURL: "postgres://localhost/test", bootstrapToken: "local-token", githubWebhookSecret: "secret"}, false},
		{"complete github config", config{address: "127.0.0.1:8097", databaseURL: "postgres://localhost/test", bootstrapToken: "local-token", githubAppID: "42", githubKeyFile: "app.pem", githubWebhookSecret: "secret"}, true},
		{"OAuth without App", config{address: "127.0.0.1:8097", databaseURL: "postgres://localhost/test", bootstrapToken: "local-token", tlsCert: "cert.pem", tlsKey: "key.pem", oauthClientID: "Iv1.test", oauthClientSecret: "client-secret", oauthCallbackURL: "https://control.test/v1/oauth/github/callback", oauthPKCEKey: strings.Repeat("K", 32)}, false},
		{"OAuth missing PKCE key", config{address: "127.0.0.1:8097", databaseURL: "postgres://localhost/test", bootstrapToken: "local-token", tlsCert: "cert.pem", tlsKey: "key.pem", githubAppID: "42", githubKeyFile: "app.pem", githubWebhookSecret: "secret", oauthClientID: "Iv1.test", oauthClientSecret: "client-secret", oauthCallbackURL: "https://control.test/v1/oauth/github/callback"}, false},
		{"OAuth callback without TLS", config{address: "127.0.0.1:8097", databaseURL: "postgres://localhost/test", bootstrapToken: "local-token", githubAppID: "42", githubKeyFile: "app.pem", githubWebhookSecret: "secret", oauthClientID: "Iv1.test", oauthClientSecret: "client-secret", oauthCallbackURL: "https://control.test/v1/oauth/github/callback", oauthPKCEKey: strings.Repeat("K", 32)}, false},
		{"complete OAuth", config{address: "127.0.0.1:8097", databaseURL: "postgres://localhost/test", bootstrapToken: "local-token", tlsCert: "cert.pem", tlsKey: "key.pem", githubAppID: "42", githubKeyFile: "app.pem", githubWebhookSecret: "secret", oauthClientID: "Iv1.test", oauthClientSecret: "client-secret", oauthCallbackURL: "https://control.test/v1/oauth/github/callback", oauthPKCEKey: strings.Repeat("K", 32)}, true},
		{"artifact without preview suffix", config{address: "127.0.0.1:8097", databaseURL: "postgres://localhost/test", bootstrapToken: "local-token", artifactStoreDir: "/tmp/artifacts"}, false},
		{"suffix without artifact", config{address: "127.0.0.1:8097", databaseURL: "postgres://localhost/test", bootstrapToken: "local-token", previewSuffix: "preview.test"}, false},
		{"wildcard suffix", config{address: "127.0.0.1:8097", databaseURL: "postgres://localhost/test", bootstrapToken: "local-token", artifactStoreDir: "/tmp/artifacts", previewSuffix: "*.preview.test"}, false},
		{"complete static candidate config", config{address: "127.0.0.1:8097", databaseURL: "postgres://localhost/test", bootstrapToken: "local-token", artifactStoreDir: "/tmp/artifacts", previewSuffix: "preview.test"}, true},
		{"production without artifacts", config{address: "127.0.0.1:8097", databaseURL: "postgres://localhost/test", bootstrapToken: "local-token", productionSuffix: "sites.test"}, false},
		{"unsafe production suffix", config{address: "127.0.0.1:8097", databaseURL: "postgres://localhost/test", bootstrapToken: "local-token", artifactStoreDir: "/tmp/artifacts", previewSuffix: "preview.test", productionSuffix: "*.sites.test"}, false},
		{"complete production suffix", config{address: "127.0.0.1:8097", databaseURL: "postgres://localhost/test", bootstrapToken: "local-token", artifactStoreDir: "/tmp/artifacts", previewSuffix: "preview.test", productionSuffix: "sites.test"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := validateConfig(tc.config) == nil; got != tc.valid {
				t.Fatalf("valid=%v, expected %v", got, tc.valid)
			}
		})
	}
}
