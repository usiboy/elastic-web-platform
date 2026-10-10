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

// Opt-in: original Express code with a fresh synthetic SQLite, no business
// credential or source DB. Never POST a real accessToken during this test.
func TestLiveOriginalXianlixiuSyntheticPrivatePreview(t *testing.T) {
	ip := os.Getenv("EWP_LIVE_XIANLIXIU_SYNTH_IP")
	id := os.Getenv("EWP_LIVE_XIANLIXIU_SYNTH_DEPLOYMENT_ID")
	if ip == "" || id == "" {
		t.Skip("set explicit synthetic original-app pilot scope")
	}
	if ip != "114.55.237.62" || !regexp.MustCompile(`^dep_[0-9a-f]{32}$`).MatchString(id) {
		t.Fatal("unexpected synthetic target")
	}
	ca, err := os.ReadFile(os.Getenv("EWP_LIVE_ECS_CA_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("invalid test CA")
	}
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip, "443"))
	}}}
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
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		return resp, data
	}
	preview := "https://d-" + strings.TrimPrefix(id, "dep_") + ".preview.ecp.lingtong.com"
	for _, path := range []string{"/", "/api/health", "/api/tools/vip-jit-stock"} {
		if resp, _ := request(http.MethodGet, preview+path, "", "", nil, nil); resp.StatusCode != 401 {
			t.Fatalf("anonymous %s=%d", path, resp.StatusCode)
		}
	}
	owner := protectedToken(t, os.Getenv("EWP_LIVE_ECS_OWNER_A_FILE"))
	other := protectedToken(t, os.Getenv("EWP_LIVE_ECS_OWNER_B_FILE"))
	issue := "https://console.ecp.lingtong.com/v1/teams/t_f9cf963718a72612c48940af8a8296e4/projects/p_34401f5c05af1079010cb95ee3ec1275/deployments/" + id + "/preview-exchanges"
	if resp, _ := request(http.MethodPost, issue, other, "", nil, nil); resp.StatusCode != 404 {
		t.Fatalf("cross-Team exchange=%d", resp.StatusCode)
	}
	issued, raw := request(http.MethodPost, issue, owner, "", nil, nil)
	var code struct {
		Code string `json:"code"`
		Host string `json:"desiredHost"`
	}
	if issued.StatusCode != 201 || json.Unmarshal(raw, &code) != nil || code.Code == "" || preview != "https://"+code.Host {
		t.Fatalf("synthetic exchange rejected=%d", issued.StatusCode)
	}
	exchanged, _ := request(http.MethodPost, preview+"/session/exchange", "", "https://console.ecp.lingtong.com", []byte("code="+url.QueryEscape(code.Code)), nil)
	if exchanged.StatusCode != 303 || len(exchanged.Cookies()) != 1 || !exchanged.Cookies()[0].Secure {
		t.Fatalf("session exchange=%d", exchanged.StatusCode)
	}
	cookie := exchanged.Cookies()[0]
	health, data := request(http.MethodGet, preview+"/api/health", "", "", nil, cookie)
	var result struct {
		Ok   bool                               `json:"ok"`
		Data struct{ Service, Database string } `json:"data"`
	}
	if health.StatusCode != 200 || json.Unmarshal(data, &result) != nil || !result.Ok || result.Data.Database != "ready" {
		t.Fatalf("synthetic SQLite health=%d", health.StatusCode)
	}
	page, html := request(http.MethodGet, preview+"/", "", "", nil, cookie)
	hash := sha256.Sum256(html)
	if page.StatusCode != 200 || hex.EncodeToString(hash[:]) != "43a94bf2c6364836446087345904f6799aa559234eefac913cfeeea867e9c698" {
		t.Fatalf("synthetic UI bytes differ: %d", page.StatusCode)
	}
	asset := regexp.MustCompile(`src="(/assets/[^" ]+[.]js)"`).FindSubmatch(html)
	if len(asset) != 2 {
		t.Fatal("UI JavaScript missing")
	}
	jsResponse, js := request(http.MethodGet, preview+string(asset[1]), "", "", nil, cookie)
	jsHash := sha256.Sum256(js)
	if jsResponse.StatusCode != 200 || hex.EncodeToString(jsHash[:]) != "80352eabdb4521ad94476585c570a3e7c5f0c694901804bb616650d4c1b3cbe4" {
		t.Fatal("synthetic UI asset differs")
	}
	if resp, _ := request(http.MethodGet, preview+"/api/tools/vip-jit-stock", "", "", nil, cookie); resp.StatusCode != 401 {
		t.Fatalf("business tool bypassed session: %d", resp.StatusCode)
	}
	loginReq, err := http.NewRequest(http.MethodPost, preview+"/api/auth/login", strings.NewReader(`{"accessToken":"DEMO-ONLY"}`))
	if err != nil {
		t.Fatal(err)
	}
	loginReq.Header.Set("Content-Type", "application/json")
	loginReq.AddCookie(cookie)
	login, err := client.Do(loginReq)
	if err != nil {
		t.Fatal(err)
	}
	defer login.Body.Close()
	var disabled struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if login.StatusCode != 403 || json.NewDecoder(io.LimitReader(login.Body, 4096)).Decode(&disabled) != nil || disabled.Error.Code != "SYNTHETIC_LOGIN_DISABLED" {
		t.Fatalf("synthetic login guard=%d", login.StatusCode)
	}
	t.Logf("original_app_synthetic=verified host=%s deployment=%s health=200 UI_SHA256=%x tools=401 login=403", code.Host, id, hash)
}
