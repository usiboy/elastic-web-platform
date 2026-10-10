package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestLiveR2SyntheticPrivatePreviewThroughGateway(t *testing.T) {
	ip, id := os.Getenv("EWP_LIVE_R2_ECS_IP"), os.Getenv("EWP_LIVE_R2_DEPLOYMENT_ID")
	if ip == "" || id == "" {
		t.Skip("set explicit ECS R2 pilot scope")
	}
	if ip != "114.55.237.62" || !regexp.MustCompile(`^dep_[0-9a-f]{32}$`).MatchString(id) {
		t.Fatal("unexpected R2 pilot target")
	}
	ca, err := os.ReadFile(os.Getenv("EWP_LIVE_ECS_CA_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("test CA unavailable")
	}
	client := &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: &http.Transport{
		Proxy: nil, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			_, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			return (&net.Dialer{Timeout: 4 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip, port))
		},
	}}
	request := func(method, target, token, origin string, body []byte, cookie *http.Cookie) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequest(method, target, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		payload, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		return response, payload
	}
	ownerA := protectedToken(t, os.Getenv("EWP_LIVE_ECS_OWNER_A_FILE"))
	ownerB := protectedToken(t, os.Getenv("EWP_LIVE_ECS_OWNER_B_FILE"))
	api := "https://console.ecp.lingtong.com/v1/teams/t_f9cf963718a72612c48940af8a8296e4/projects/p_34401f5c05af1079010cb95ee3ec1275/deployments/" + id + "/preview-exchanges"
	preview := "https://d-" + strings.TrimPrefix(id, "dep_") + ".preview.ecp.lingtong.com"
	if response, _ := request(http.MethodGet, preview+"/api/hello", "", "", nil, nil); response.StatusCode != 401 {
		t.Fatalf("anonymous dynamic API=%d", response.StatusCode)
	}
	if response, _ := request(http.MethodPost, api, ownerB, "", nil, nil); response.StatusCode != 404 {
		t.Fatalf("cross-Team exchange=%d", response.StatusCode)
	}
	issued, raw := request(http.MethodPost, api, ownerA, "", nil, nil)
	var code struct {
		Code string `json:"code"`
		Host string `json:"desiredHost"`
	}
	if issued.StatusCode != 201 || json.Unmarshal(raw, &code) != nil || code.Code == "" || preview != "https://"+code.Host {
		t.Fatalf("R2 Preview exchange rejected: %d", issued.StatusCode)
	}
	exchanged, _ := request(http.MethodPost, preview+"/session/exchange", "", "https://console.ecp.lingtong.com", []byte("code="+url.QueryEscape(code.Code)), nil)
	if exchanged.StatusCode != 303 || len(exchanged.Cookies()) != 1 {
		t.Fatalf("R2 Preview session=%d", exchanged.StatusCode)
	}
	cookie := exchanged.Cookies()[0]
	apiResponse, payload := request(http.MethodGet, preview+"/api/hello", "", "", nil, cookie)
	var actual struct {
		Service, Node string
		Variant       string `json:"variant"`
	}
	if apiResponse.StatusCode != 200 || json.Unmarshal(payload, &actual) != nil || actual.Service != "r2-next-synthetic" || actual.Node != "22.22.0" {
		t.Fatalf("Next API mismatch: %d", apiResponse.StatusCode)
	}
	if expected := os.Getenv("EWP_LIVE_R2_VARIANT"); expected != "" && actual.Variant != expected {
		t.Fatalf("wrong immutable runtime version: got=%q want=%q", actual.Variant, expected)
	}
	page1, body1 := request(http.MethodGet, preview+"/", "", "", nil, cookie)
	_, body2 := request(http.MethodGet, preview+"/", "", "", nil, cookie)
	marker := regexp.MustCompile(`data-request-id="([0-9a-f-]{36})"`)
	first, second := marker.FindSubmatch(body1), marker.FindSubmatch(body2)
	if page1.StatusCode != 200 || len(first) != 2 || len(second) != 2 || bytes.Equal(first[1], second[1]) {
		t.Fatal("Next SSR was not evaluated per request")
	}
	streamReq, _ := http.NewRequest(http.MethodGet, preview+"/api/stream", nil)
	streamReq.AddCookie(cookie)
	stream, err := client.Do(streamReq)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	firstChunk := make([]byte, len("start\n"))
	if _, err := io.ReadFull(stream.Body, firstChunk); err != nil || string(firstChunk) != "start\n" {
		t.Fatalf("stream start=%q %v", firstChunk, err)
	}
	rest, err := io.ReadAll(stream.Body)
	if err != nil || string(rest) != "done\n" {
		t.Fatalf("dynamic Preview stream bytes changed: %q %v", rest, err)
	}
	if response, _ := request(http.MethodGet, "https://r2-stage.preview.ecp.lingtong.com/", "", "", nil, nil); response.StatusCode != 200 {
		t.Fatalf("baseline stage unavailable: %d", response.StatusCode)
	}
	t.Logf("r2_private_preview=verified host=%s deployment=%s SSR=per_request stream=two_chunks", code.Host, id)
}

func TestLiveR2StageStreamsThroughExactNginxHost(t *testing.T) {
	ip := os.Getenv("EWP_LIVE_R2_ECS_IP")
	if ip == "" {
		t.Skip("set explicit R2 ECS scope")
	}
	if ip != "114.55.237.62" {
		t.Fatal("unexpected R2 target")
	}
	ca, err := os.ReadFile(os.Getenv("EWP_LIVE_ECS_CA_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("R2 test CA unavailable")
	}
	client := &http.Client{Timeout: 8 * time.Second, Transport: &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 4 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip, "443"))
	}}}
	base := "https://r2-stage.preview.ecp.lingtong.com"
	api, err := client.Get(base + "/api/hello")
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Service, Node string
		Variant       string `json:"variant"`
	}
	if api.StatusCode != 200 || json.NewDecoder(io.LimitReader(api.Body, 4096)).Decode(&result) != nil || result.Service != "r2-next-synthetic" || result.Node != "22.22.0" {
		api.Body.Close()
		t.Fatalf("staged runtime API not ready: %d", api.StatusCode)
	}
	api.Body.Close()
	if expected := os.Getenv("EWP_LIVE_R2_STAGE_VARIANT"); expected != "" && result.Variant != expected {
		t.Fatalf("stage points at wrong runtime version: got=%q want=%q", result.Variant, expected)
	}
	stream, err := client.Get(base + "/api/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	first := make([]byte, len("start\n"))
	if _, err := io.ReadFull(stream.Body, first); err != nil || string(first) != "start\n" {
		t.Fatalf("first chunk missing: %q %v", first, err)
	}
	start := time.Now()
	last, err := io.ReadAll(stream.Body)
	if err != nil || string(last) != "done\n" || time.Since(start) < 40*time.Millisecond {
		t.Fatalf("stage buffering not disabled: %q %v", last, err)
	}
	t.Log("r2_stage=Next15 SSR/API/stream via TLS Nginx→Gateway→Unix relay→runsc")
}

// Explicitly opt in: this one-time test changes only the synthetic stage
// alias from generation 2 back to the previously verified static generation 3.
func TestLiveR2StageTimeoutRestoresStaticAndClosesFailedPreview(t *testing.T) {
	if os.Getenv("EWP_LIVE_R2_FAILOVER") != "1" {
		t.Skip("requires explicit synthetic failover intent")
	}
	ip := os.Getenv("EWP_LIVE_R2_ECS_IP")
	if ip != "114.55.237.62" {
		t.Fatal("unexpected failover target")
	}
	ca, err := os.ReadFile(os.Getenv("EWP_LIVE_ECS_CA_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("invalid test CA")
	}
	client := &http.Client{Timeout: 8 * time.Second, Transport: &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 4 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip, "443"))
	}}}
	stage := "https://r2-stage.preview.ecp.lingtong.com"
	hung, err := client.Get(stage + "/api/hang")
	if err != nil {
		t.Fatal(err)
	}
	hung.Body.Close()
	if hung.StatusCode != 504 {
		t.Fatalf("hung runtime did not time out: %d", hung.StatusCode)
	}
	var hash string
	deadline := time.Now().Add(8 * time.Second)
	for {
		response, err := client.Get(stage + "/")
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		response.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		digest := sha256.Sum256(body)
		hash = hex.EncodeToString(digest[:])
		if response.StatusCode == 200 && hash == "e1026657cf7807381ea12f21325a8638decf0b1989eb42d4e549c3fb98a32ff6" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("known-good static fallback not restored: status=%d hash=%s", response.StatusCode, hash)
		}
		time.Sleep(200 * time.Millisecond)
	}
	fixed := "https://d-fae3b413a77f3334325cef72f332378c.preview.ecp.lingtong.com/api/hello"
	closed, err := client.Get(fixed)
	if err != nil {
		t.Fatal(err)
	}
	closed.Body.Close()
	if closed.StatusCode != 404 {
		t.Fatalf("failed private runtime remained routable: %d", closed.StatusCode)
	}
	t.Logf("r2_timeout=504 rollback_static_sha256=%s failed_preview=404", hash)
}

// One-time opt-in: v3 fails while v2 is kept warm as the preceding version.
func TestLiveR2V3TimeoutRestoresPreviousDynamicVersion(t *testing.T) {
	if os.Getenv("EWP_LIVE_R2_V3_FAILOVER") != "1" {
		t.Skip("requires explicit synthetic v3 failure intent")
	}
	ip := os.Getenv("EWP_LIVE_R2_ECS_IP")
	if ip != "114.55.237.62" {
		t.Fatal("unexpected R2 v3 target")
	}
	ca, err := os.ReadFile(os.Getenv("EWP_LIVE_ECS_CA_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("invalid test CA")
	}
	client := &http.Client{Timeout: 8 * time.Second, Transport: &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 4 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip, "443"))
	}}}
	stage := "https://r2-stage.preview.ecp.lingtong.com"
	readVariant := func() (int, string) {
		t.Helper()
		response, err := client.Get(stage + "/api/hello")
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var message struct{ Service, Variant string }
		if response.StatusCode == 200 && json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&message) != nil {
			t.Fatal("malformed Next API")
		}
		if response.StatusCode == 200 && message.Service != "r2-next-synthetic" {
			t.Fatal("wrong Next service")
		}
		return response.StatusCode, message.Variant
	}
	if status, version := readVariant(); status != 200 || version != "v3" {
		t.Fatalf("v3 not active before failure: %d %q", status, version)
	}
	hung, err := client.Get(stage + "/api/hang")
	if err != nil {
		t.Fatal(err)
	}
	hung.Body.Close()
	if hung.StatusCode != 504 {
		t.Fatalf("hung v3 did not time out: %d", hung.StatusCode)
	}
	deadline := time.Now().Add(8 * time.Second)
	for {
		status, version := readVariant()
		if status == 200 && version == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("v2 not restored: status=%d version=%q", status, version)
		}
		time.Sleep(200 * time.Millisecond)
	}
	fixed, err := client.Get("https://d-0a8092587d1798386092cda3cccffc14.preview.ecp.lingtong.com/api/hello")
	if err != nil {
		t.Fatal(err)
	}
	fixed.Body.Close()
	if fixed.StatusCode != 404 {
		t.Fatalf("failed v3 fixed Preview remained routable: %d", fixed.StatusCode)
	}
	t.Log("r2_v3_timeout=504 stage_v2_restored=200 fixed_v3=404")
}
