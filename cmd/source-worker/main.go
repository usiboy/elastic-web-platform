package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"elastic-web-platform/db/migrations"
	"elastic-web-platform/internal/bundle"
	"elastic-web-platform/internal/pipeline"
	"elastic-web-platform/internal/source/github"
	"elastic-web-platform/internal/source/uploads"

	"github.com/jackc/pgx/v5/pgxpool"
)

type config struct {
	databaseURL, sourceDir, artifactDir, previewSuffix, githubAppID, githubKeyFile string
	productionSuffix                                                               string
	pollInterval                                                                   string
	scopedTokenFile                                                                string
	oneShot                                                                        bool
}

var suffixPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*[a-z0-9]$`)

func validateConfig(c config) error {
	if c.databaseURL == "" || c.sourceDir == "" || c.artifactDir == "" {
		return errors.New("worker requires DB and independent source/artifact stores")
	}
	if c.scopedTokenFile != "" {
		if c.githubAppID != "" || c.githubKeyFile != "" || !c.oneShot || c.productionSuffix != "" {
			return errors.New("temporary Git identity only permits one-shot source verification without production promotion")
		}
	} else {
		id, err := strconv.ParseInt(c.githubAppID, 10, 64)
		if c.githubKeyFile == "" || err != nil || id <= 0 {
			return errors.New("worker requires GitHub App ID/private key or protected temporary identity")
		}
	}
	if len(c.previewSuffix) > 180 || !suffixPattern.MatchString(c.previewSuffix) || strings.Contains(c.previewSuffix, "..") {
		return errors.New("invalid Preview suffix")
	}
	if c.productionSuffix != "" && (len(c.productionSuffix) > 180 || !suffixPattern.MatchString(c.productionSuffix) || strings.Contains(c.productionSuffix, "..")) {
		return errors.New("invalid Production suffix")
	}
	if c.pollInterval != "" {
		period, err := time.ParseDuration(c.pollInterval)
		if err != nil || period < 30*time.Second || period > 24*time.Hour {
			return errors.New("Git polling interval must be between 30 seconds and 24 hours")
		}
	}
	return nil
}

func main() {
	c := config{databaseURL: os.Getenv("DATABASE_URL"), sourceDir: os.Getenv("SOURCE_STORE_DIR"), artifactDir: os.Getenv("ARTIFACT_STORE_DIR"), previewSuffix: os.Getenv("SITE_PREVIEW_SUFFIX"), productionSuffix: os.Getenv("SITE_PRODUCTION_SUFFIX"), githubAppID: os.Getenv("GITHUB_APP_ID"), githubKeyFile: os.Getenv("GITHUB_APP_PRIVATE_KEY_FILE"), pollInterval: os.Getenv("EWP_GITHUB_POLL_INTERVAL"), scopedTokenFile: os.Getenv("EWP_GITHUB_SCOPED_TOKEN_FILE"), oneShot: os.Getenv("EWP_SOURCE_WORKER_ONCE") == "1"}
	if err := validateConfig(c); err != nil {
		log.Fatal("source worker configuration: ", err)
	}
	var fetcher *github.Client
	if c.scopedTokenFile != "" {
		file, err := github.LoadScopedTokenFile(c.scopedTokenFile, 167883648, 1404606559, 1404606859)
		if err != nil {
			log.Fatal("protected temporary Git identity rejected: ", err)
		}
		fetcher, err = github.NewScopedTokenClient(file.InstallationID, file.Credentials, "https://api.github.com", nil)
		if err != nil {
			log.Fatal("single-repo identity rejected: ", err)
		}
	} else {
		key, err := github.LoadPrivateKey(c.githubKeyFile)
		if err != nil {
			log.Fatal("GitHub App key rejected: ", err)
		}
		appID, _ := strconv.ParseInt(c.githubAppID, 10, 64)
		fetcher = github.NewClient(appID, key, "https://api.github.com", nil)
	}
	startup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := pgxpool.New(startup, c.databaseURL)
	if err != nil {
		log.Fatal("database connection: ", err)
	}
	defer db.Close()
	if err := migrations.Apply(startup, db); err != nil {
		log.Fatal("migration failed: ", err)
	}
	processor := pipeline.NewProcessor(db, uploads.NewDiskStore(c.sourceDir), bundle.NewDiskStore(c.artifactDir), fetcher, c.previewSuffix, c.productionSuffix)
	if c.oneShot {
		oneShotCtx, done := context.WithTimeout(context.Background(), 120*time.Second)
		defer done()
		polled, verified := 0, 0
		for i := 0; i < 2; i++ {
			found, err := processor.PollOne(oneShotCtx)
			if err != nil {
				log.Fatal("one-shot Git poll failed: ", err)
			}
			if !found {
				break
			}
			polled++
		}
		for i := 0; i < 2; i++ {
			found, err := processor.ProcessOne(oneShotCtx)
			if err != nil {
				log.Fatal("one-shot fixed Git verification failed: ", err)
			}
			if !found {
				break
			}
			verified++
		}
		log.Printf("one-shot fixed Git sources processed: polled=%d verified=%d (Vite remains awaiting isolated build)", polled, verified)
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	var pollEvery time.Duration
	if c.pollInterval != "" {
		pollEvery, _ = time.ParseDuration(c.pollInterval)
	}
	var lastPoll time.Time
	for {
		attempt, done := context.WithTimeout(ctx, 30*time.Second)
		worked, err := processor.ProcessOne(attempt)
		done()
		if err != nil {
			log.Printf("Git source processing deferred: %v", err)
		}
		if worked && err == nil {
			continue
		}
		if err == nil {
			expireCtx, expireCancel := context.WithTimeout(ctx, 10*time.Second)
			expired, expireErr := processor.ExpireOneBuildJob(expireCtx)
			expireCancel()
			if expireErr != nil {
				log.Printf("Git build job expiry deferred: %v", expireErr)
			}
			if expired && expireErr == nil {
				continue
			}
			promoteCtx, promoteCancel := context.WithTimeout(ctx, 30*time.Second)
			promoted, promoteErr := processor.PromoteOne(promoteCtx)
			promoteCancel()
			if promoteErr != nil {
				log.Printf("Git production promotion deferred: %v", promoteErr)
			}
			if promoted && promoteErr == nil {
				continue
			}
		}
		if pollEvery > 0 && time.Since(lastPoll) >= pollEvery {
			pollCtx, pollCancel := context.WithTimeout(ctx, 45*time.Second)
			polled, pollErr := processor.PollOne(pollCtx)
			pollCancel()
			lastPoll = time.Now()
			if pollErr != nil {
				log.Printf("Git branch polling deferred: %v", pollErr)
			}
			if polled && pollErr == nil {
				continue
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
