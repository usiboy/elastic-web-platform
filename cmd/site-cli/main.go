package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"elastic-web-platform/internal/source/uploads"
)

var platformID = regexp.MustCompile(`^[a-z]+_[a-z0-9]+$`)
var safeRequestID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,110}$`)

func main() {
	api := os.Getenv("EWP_CONTROL_URL")
	if api == "" {
		api = "http://127.0.0.1:8097"
	}
	// An explicit --api overrides EWP_CONTROL_URL in run.
	args := append([]string{}, os.Args[1:]...)
	if len(args) > 0 {
		args = append(args, "--default-api", api)
	}
	if err := run(context.Background(), args, os.Getenv("EWP_TOKEN"), nil, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "site-cli:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, token string, client *http.Client, out io.Writer) error {
	if token == "" {
		return errors.New("EWP_TOKEN is required")
	}
	if len(args) == 0 {
		return errors.New("command required: deploy, status, preview, promote, rollback")
	}
	command := args[0]
	if command != "deploy" && command != "status" && command != "preview" && command != "promote" && command != "rollback" {
		return errors.New("unsupported command")
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	team := flags.String("team", "", "Team ID")
	project := flags.String("project", "", "Project ID")
	archive := flags.String("archive", "", "tar.gz source")
	deployment := flags.String("deployment", "", "Deployment ID")
	api := flags.String("api", "", "control API URL")
	defaultAPI := flags.String("default-api", "http://127.0.0.1:8097", "default control API URL")
	controlOrigin := flags.String("control-origin", "", "HTTPS origin allowed by Preview gateway")
	requestID := flags.String("request-id", "", "stable retry ID")
	expected := flags.Int64("expected-generation", -1, "alias generation")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if len(flags.Args()) != 0 || !platformID.MatchString(*team) || !platformID.MatchString(*project) {
		return errors.New("valid --team and --project are required")
	}
	if *requestID != "" && !safeRequestID.MatchString(*requestID) {
		return errors.New("invalid request ID")
	}
	base := *api
	if base == "" {
		base = *defaultAPI
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" {
		return errors.New("invalid API base URL")
	}
	if parsed.Scheme != "https" {
		ip := net.ParseIP(parsed.Hostname())
		if parsed.Scheme != "http" || (parsed.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback())) {
			return errors.New("remote API requires HTTPS")
		}
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	copy := *client
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if copy.Timeout == 0 {
		copy.Timeout = 30 * time.Second
	}
	client = &copy
	path := "/v1/teams/" + url.PathEscape(*team) + "/projects/" + url.PathEscape(*project)
	var result map[string]any
	switch command {
	case "deploy":
		if *archive == "" {
			return errors.New("--archive is required")
		}
		data, err := os.ReadFile(*archive)
		if err != nil {
			return err
		}
		manifest, err := uploads.Validate(data)
		if err != nil {
			return fmt.Errorf("archive rejected: %w", err)
		}
		upload, err := call(ctx, client, base, token, http.MethodPost, path+"/sources/uploads", "upload-"+manifest.Digest, "application/gzip", data)
		if err != nil {
			return err
		}
		sourceID, ok := upload["sourceId"].(string)
		if !ok || sourceID == "" {
			return errors.New("API did not return sourceId")
		}
		key := *requestID
		if key == "" {
			random := make([]byte, 16)
			if _, err := rand.Read(random); err != nil {
				return err
			}
			key = hex.EncodeToString(random)
		}
		body, _ := json.Marshal(map[string]string{"sourceId": sourceID})
		result, err = call(ctx, client, base, token, http.MethodPost, path+"/deployments", "deploy-"+key, "application/json", body)
		if err != nil {
			return err
		}
	case "status":
		if !platformID.MatchString(*deployment) {
			return errors.New("valid --deployment required")
		}
		result, err = call(ctx, client, base, token, http.MethodGet, path+"/deployments/"+url.PathEscape(*deployment), "", "", nil)
		if err != nil {
			return err
		}
	case "preview":
		if !platformID.MatchString(*deployment) {
			return errors.New("valid --deployment required")
		}
		origin := *controlOrigin
		if origin == "" && parsed.Scheme == "https" {
			origin = base
		}
		originURL, parseErr := url.Parse(origin)
		if parseErr != nil || originURL.Scheme != "https" || originURL.Host == "" || originURL.Path != "" || originURL.RawQuery != "" || originURL.Fragment != "" || originURL.User != nil {
			return errors.New("--control-origin must be an exact HTTPS origin")
		}
		issued, callErr := call(ctx, client, base, token, http.MethodPost, path+"/deployments/"+url.PathEscape(*deployment)+"/preview-exchanges", "", "", nil)
		if callErr != nil {
			return callErr
		}
		code, ok := issued["code"].(string)
		if !ok || len(code) < 20 || len(code) > 128 {
			return errors.New("API did not return a valid Preview code")
		}
		host, ok := issued["desiredHost"].(string)
		if !ok || host == "" {
			return errors.New("API did not return a Preview Host")
		}
		siteURL, parseErr := url.Parse("https://" + host)
		if parseErr != nil || siteURL.Scheme != "https" || siteURL.Hostname() == "" || siteURL.Path != "" || siteURL.User != nil || siteURL.RawQuery != "" {
			return errors.New("invalid Preview Host")
		}
		form := url.Values{"code": {code}}.Encode()
		exchangeReq, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, siteURL.String()+"/session/exchange", strings.NewReader(form))
		if reqErr != nil {
			return reqErr
		}
		exchangeReq.Header.Set("Origin", origin)
		exchangeReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		exchangeResp, reqErr := client.Do(exchangeReq)
		if reqErr != nil {
			return reqErr
		}
		_ = exchangeResp.Body.Close()
		if exchangeResp.StatusCode != http.StatusSeeOther {
			return fmt.Errorf("Preview exchange HTTP %d", exchangeResp.StatusCode)
		}
		var cookie *http.Cookie
		for _, candidate := range exchangeResp.Cookies() {
			if candidate.Name == "__Host-ewp_preview" && candidate.Secure && candidate.HttpOnly && candidate.Domain == "" && candidate.Path == "/" {
				cookie = candidate
				break
			}
		}
		if cookie == nil {
			return errors.New("Preview gateway did not issue a secure Host-only session")
		}
		indexReq, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, siteURL.String()+"/", nil)
		if reqErr != nil {
			return reqErr
		}
		indexReq.AddCookie(cookie)
		indexResp, reqErr := client.Do(indexReq)
		if reqErr != nil {
			return reqErr
		}
		defer indexResp.Body.Close()
		if indexResp.StatusCode != http.StatusOK {
			return fmt.Errorf("Preview HTTP %d", indexResp.StatusCode)
		}
		body, readErr := io.ReadAll(io.LimitReader(indexResp.Body, (4<<20)+1))
		if readErr != nil || len(body) > 4<<20 {
			return errors.New("Preview response exceeds local inspection limit")
		}
		bodyHash := sha256.Sum256(body)
		result = map[string]any{"previewHost": host, "httpStatus": indexResp.StatusCode, "bodySha256": hex.EncodeToString(bodyHash[:])}
	case "promote", "rollback":
		if !platformID.MatchString(*deployment) || *expected < 0 {
			return errors.New("--deployment and --expected-generation are required")
		}
		body, _ := json.Marshal(map[string]any{"deploymentId": *deployment, "expectedGeneration": *expected})
		result, err = call(ctx, client, base, token, http.MethodPost, path+"/aliases/production:"+command, "", "application/json", body)
		if err != nil {
			return err
		}
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func call(ctx context.Context, client *http.Client, base, token, method, path, key, contentType string, data []byte) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 || resp.StatusCode < 200 {
		var apiError struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&apiError)
		if apiError.Error.Code == "" {
			apiError.Error.Code = "unexpected_response"
		}
		return nil, fmt.Errorf("API HTTP %d: %s", resp.StatusCode, apiError.Error.Code)
	}
	var result map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&result); err != nil {
		return nil, err
	}
	return result, nil
}
