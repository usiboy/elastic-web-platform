package main

import "testing"

func TestWorkerFailsClosedWithoutScopedGitAndStorageConfig(t *testing.T) {
	valid := config{databaseURL: "postgres://localhost/test", sourceDir: "/tmp/sources", artifactDir: "/tmp/artifacts", previewSuffix: "preview.test", githubAppID: "42", githubKeyFile: "/tmp/app.pem"}
	if err := validateConfig(valid); err != nil {
		t.Fatal(err)
	}
	cases := []config{
		{databaseURL: valid.databaseURL, sourceDir: valid.sourceDir, artifactDir: valid.artifactDir, previewSuffix: valid.previewSuffix, githubAppID: valid.githubAppID},
		{databaseURL: valid.databaseURL, sourceDir: valid.sourceDir, artifactDir: valid.artifactDir, previewSuffix: "*.preview.test", githubAppID: valid.githubAppID, githubKeyFile: valid.githubKeyFile},
		{databaseURL: valid.databaseURL, sourceDir: "", artifactDir: valid.artifactDir, previewSuffix: valid.previewSuffix, githubAppID: valid.githubAppID, githubKeyFile: valid.githubKeyFile},
	}
	for _, tc := range cases {
		if err := validateConfig(tc); err == nil {
			t.Fatalf("unsafe worker config accepted: %+v", tc)
		}
	}
}

func TestGitPollingRequiresExplicitBoundedInterval(t *testing.T) {
	base := config{databaseURL: "postgres://localhost/test", sourceDir: "/tmp/sources", artifactDir: "/tmp/artifacts", previewSuffix: "preview.test", githubAppID: "42", githubKeyFile: "/tmp/app.pem"}
	base.pollInterval = "60s"
	if err := validateConfig(base); err != nil {
		t.Fatalf("bounded poll rejected: %v", err)
	}
	for _, value := range []string{"1ms", "not-a-duration", "0s", "48h"} {
		base.pollInterval = value
		if err := validateConfig(base); err == nil {
			t.Fatalf("unsafe poll interval %q accepted", value)
		}
	}
}

func TestTemporaryScopedTokenModeCannotRunDaemonOrAutoPromote(t *testing.T) {
	c := config{databaseURL: "postgres://localhost/test", sourceDir: "/tmp/sources", artifactDir: "/tmp/artifacts", previewSuffix: "preview.test", scopedTokenFile: "/tmp/private/token.json", oneShot: true}
	if err := validateConfig(c); err != nil {
		t.Fatalf("short-lived protected token mode rejected: %v", err)
	}
	c.oneShot = false
	if err := validateConfig(c); err == nil {
		t.Fatal("temporary Git token admitted persistent worker")
	}
	c.oneShot = true
	c.productionSuffix = "sites.test"
	if err := validateConfig(c); err == nil {
		t.Fatal("temporary Git token admitted automatic production")
	}
	c.productionSuffix = ""
	c.githubKeyFile = "/tmp/app.pem"
	if err := validateConfig(c); err == nil {
		t.Fatal("temporary and private App identities combined")
	}
}
