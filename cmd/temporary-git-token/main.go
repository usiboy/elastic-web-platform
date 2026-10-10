// temporary-git-token 将 Mac 上的 App 身份换成两个独立、只读、短期单仓库令牌。
// Token 仅写入 0600 文件；revoke 调用 GitHub 撤销后清理本地文件。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"elastic-web-platform/internal/source/github"
)

const (
	installationID = int64(167883648)
	repoA          = int64(1404606559)
	repoB          = int64(1404606859)
)

func privateRoot(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir() && info.Mode().Perm()&0077 == 0
}

func main() {
	mode := flag.String("mode", "", "mint or revoke")
	root := flag.String("private-dir", "", "existing private output directory")
	flag.Parse()
	if !privateRoot(*root) || (*mode != "mint" && *mode != "revoke") {
		log.Fatal("private directory and mint/revoke mode required")
	}
	file := filepath.Join(*root, "temporary-git-ab.json")
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if *mode == "revoke" {
		stored, err := github.LoadScopedTokenFile(file, installationID, repoA, repoB)
		if err != nil {
			log.Fatal("protected scoped identity unavailable: ", err)
		}
		client, err := github.NewScopedTokenClient(installationID, stored.Credentials, "https://api.github.com", nil)
		if err != nil {
			log.Fatal("scoped identity invalid: ", err)
		}
		for _, item := range stored.Credentials {
			if err := client.RevokeScopedToken(ctx, item.Token); err != nil {
				log.Fatal("remote token revocation failed: ", err)
			}
			fmt.Printf("revoked_repository=%d\n", item.RepositoryID)
		}
		if err := os.Remove(file); err != nil {
			log.Fatal("revoked token file could not be removed")
		}
		return
	}
	if _, err := os.Lstat(file); !errors.Is(err, os.ErrNotExist) {
		log.Fatal("temporary token output already exists")
	}
	appID, err := strconv.ParseInt(os.Getenv("GITHUB_APP_ID"), 10, 64)
	if err != nil || appID != 5187675 || os.Getenv("GITHUB_INSTALLATION_ID") != strconv.FormatInt(installationID, 10) {
		log.Fatal("wrong approved GitHub App installation scope")
	}
	key, err := github.LoadPrivateKey(os.Getenv("GITHUB_APP_PRIVATE_KEY_FILE"))
	if err != nil {
		log.Fatal("protected Mac-only App private key unavailable")
	}
	client := github.NewClient(appID, key, "https://api.github.com", nil)
	if err := mintAndVerify(ctx, client, file, "https://api.github.com", nil); err != nil {
		log.Fatal("temporary single-repo mint failed: ", err)
	}
}

func mintAndVerify(ctx context.Context, client *github.Client, file, baseURL string, transport *http.Client) error {
	credentials := make([]github.ScopedToken, 0, 2)
	persisted := false
	defer func() {
		if !persisted {
			for _, item := range credentials {
				revokeContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				_ = client.RevokeScopedToken(revokeContext, item.Token)
				cancel()
			}
		}
	}()
	for _, item := range []struct {
		id   int64
		name string
	}{{repoA, "usiboy/testA"}, {repoB, "usiboy/testB"}} {
		scoped, err := client.MintScopedRepositoryToken(ctx, installationID, item.id)
		if scoped.Token != "" {
			credentials = append(credentials, scoped)
		}
		if err != nil {
			return err
		}
		check, err := github.NewScopedTokenClient(installationID, []github.ScopedToken{scoped}, baseURL, transport)
		if err != nil {
			return err
		}
		repo, err := check.VerifyRepository(ctx, installationID, item.id)
		if err != nil || repo.ID != item.id || repo.FullName != item.name {
			return fmt.Errorf("single-repository ownership could not be read back: repo_id=%d name=%s provider_error=%v", repo.ID, repo.FullName, err)
		}
	}
	output, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("protected scoped token file cannot be created")
	}
	if err := json.NewEncoder(output).Encode(github.ScopedTokenFile{InstallationID: installationID, Credentials: credentials}); err != nil {
		_ = output.Close()
		_ = os.Remove(file)
		return errors.New("temporary token file write failed")
	}
	if err := output.Sync(); err != nil {
		_ = output.Close()
		_ = os.Remove(file)
		return errors.New("temporary token file sync failed")
	}
	if err := output.Close(); err != nil {
		_ = os.Remove(file)
		return errors.New("temporary token file close failed")
	}
	persisted = true
	fmt.Printf("temporary_scoped_repositories=%d,%d expires_at=%s file=%s\n", repoA, repoB, credentials[0].ExpiresAt.UTC().Format(time.RFC3339), file)
	return nil
}
