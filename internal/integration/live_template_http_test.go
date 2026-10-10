package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"testing"
	"time"
)

// Opt-in: 固定测试租户的公网 HTTP 无凭据静态交付，不调用公网快麦业务接口。
func TestLiveTemplateStaticHTTPAfterTrustedFixtureAndExplicitPromotion(t *testing.T) {
	if os.Getenv("EWP_LIVE_TEMPLATE_IP") == "" {
		t.Skip("set EWP_LIVE_TEMPLATE_IP, private test CA and scoped Owner identity")
	}
	ip := os.Getenv("EWP_LIVE_TEMPLATE_IP")
	if ip != "114.55.237.62" {
		t.Fatal("unexpected ECS address")
	}
	ca, err := os.ReadFile(os.Getenv("EWP_LIVE_ECS_CA_FILE"))
	if err != nil {
		t.Fatal("test CA unavailable")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("invalid test CA")
	}
	client := &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: &http.Transport{
		Proxy: nil,
		TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip, "443"))
		},
	}}
	plain := &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip, "80"))
		},
	}}
	request := func(c *http.Client, method, target, owner, origin string, body []byte, cookie *http.Cookie) (*http.Response, []byte) {
		t.Helper()
		r, err := http.NewRequest(method, target, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if owner != "" {
			r.Header.Set("Authorization", "Bearer "+owner)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		response, err := c.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		payload, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
		_ = response.Body.Close()
		if err != nil || len(payload) > 1<<20 {
			t.Fatal("HTTP response exceeded verification budget")
		}
		return response, payload
	}

	const team = "t_f9cf963718a72612c48940af8a8296e4"
	const project = "p_34401f5c05af1079010cb95ee3ec1275"
	const previewHost = "a-e6062c7.preview.ecp.lingtong.com"
	const publicHost = "p-34401f5c05af1079010cb95ee3ec1275.sites.ecp.lingtong.com"
	const expectedHTML = "e1026657cf7807381ea12f21325a8638decf0b1989eb42d4e549c3fb98a32ff6"
	owner := protectedToken(t, os.Getenv("EWP_LIVE_ECS_OWNER_A_FILE"))
	api := fmt.Sprintf("https://console.ecp.lingtong.com/v1/teams/%s/projects/%s", team, project)
	list, payload := request(client, "GET", api+"/deployments", owner, "", nil, nil)
	if list.StatusCode != 200 {
		t.Fatalf("deployment list: %d", list.StatusCode)
	}
	var entries struct {
		Deployments []struct {
			ID string `json:"deploymentId"`
			Host string `json:"desiredHost"`
		} `json:"deployments"`
	}
	if err := json.Unmarshal(payload, &entries); err != nil {
		t.Fatal(err)
	}
	var deployment string
	for _, entry := range entries.Deployments {
		if entry.Host == previewHost {
			if deployment != "" {
				t.Fatal("ambiguous fixed preview Host")
			}
			deployment = entry.ID
		}
	}
	if deployment == "" {
		t.Fatal("template fixture is not registered")
	}
	previewURL := "https://" + previewHost + "/"
	deadline := time.Now().Add(12 * time.Second)
	for {
		response, _ := request(client, "GET", previewURL, "", "", nil, nil)
		if response.StatusCode == 401 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("private preview Host not reloaded: %d", response.StatusCode)
		}
		time.Sleep(200 * time.Millisecond)
	}
	issued, payload := request(client, "POST", api+"/deployments/"+deployment+"/preview-exchanges", owner, "", nil, nil)
	if issued.StatusCode != 201 {
		t.Fatalf("preview code issue: %d", issued.StatusCode)
	}
	var code struct { Code string `json:"code"`; Host string `json:"desiredHost"` }
	if err := json.Unmarshal(payload, &code); err != nil || code.Code == "" || code.Host != previewHost {
		t.Fatal("preview code is not scoped")
	}
	exchange, _ := request(client, "POST", "https://"+previewHost+"/session/exchange", "", "https://console.ecp.lingtong.com", []byte("code="+url.QueryEscape(code.Code)), nil)
	if exchange.StatusCode != 303 || len(exchange.Cookies()) != 1 || !exchange.Cookies()[0].Secure || !exchange.Cookies()[0].HttpOnly {
		t.Fatalf("private preview session invalid: %d", exchange.StatusCode)
	}
	preview, html := request(client, "GET", previewURL, "", "", nil, exchange.Cookies()[0])
	indexSHA := sha256.Sum256(html)
	if preview.StatusCode != 200 || hex.EncodeToString(indexSHA[:]) != expectedHTML {
		t.Fatal("preview did not serve the reviewed CI index")
	}
	if os.Getenv("EWP_LIVE_TEMPLATE_PUBLISH") != "1" {
		t.Logf("preview=%s deployment=%s HTML_SHA256=%s; production promotion not requested", previewHost, deployment, expectedHTML)
		return
	}
	aliasURL := api + "/aliases/production"
	alias, payload := request(client, "GET", aliasURL, owner, "", nil, nil)
	if alias.StatusCode == 404 {
		input, _ := json.Marshal(map[string]any{"deploymentId": deployment, "expectedGeneration": 0})
		promotion, body := request(client, "POST", aliasURL+":promote", owner, "", input, nil)
		if promotion.StatusCode != 202 || !bytes.Contains(body, []byte(publicHost)) {
			t.Fatalf("production promotion rejected: %d", promotion.StatusCode)
		}
	} else {
		var current struct { Host string `json:"desiredHost"`; DeploymentID string `json:"deploymentId"` }
		if alias.StatusCode != 200 || json.Unmarshal(payload, &current) != nil || current.Host != publicHost || current.DeploymentID == "" {
			t.Fatalf("existing production alias conflicts: %d", alias.StatusCode)
		}
		if current.DeploymentID != deployment {
			candidate, raw := request(client, "GET", api+"/deployments/"+current.DeploymentID, owner, "", nil, nil)
			if candidate.StatusCode != 200 || !bytes.Contains(raw, []byte(`"observedState":"preview_ready"`)) {
				t.Fatal("existing production was not the observed approved Git version")
			}
		}
	}
	publicURL := "http://" + publicHost + "/"
	deadline = time.Now().Add(12 * time.Second)
	for {
		page, body := request(plain, "GET", publicURL, "", "", nil, nil)
		digest := sha256.Sum256(body)
		if page.StatusCode == 200 && hex.EncodeToString(digest[:]) == expectedHTML {
			if page.Header.Get("Cache-Control") != "no-cache" {
				t.Fatal("public HTML cache contract drifted")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("public HTTP alias not observed: %d", page.StatusCode)
		}
		time.Sleep(200 * time.Millisecond)
	}
	asset := regexp.MustCompile(`src="(/assets/[^" ]+\.js)"`).FindSubmatch(html)
	if len(asset) < 2 {
		t.Fatal("CI build JS reference missing")
	}
	javascript, js := request(plain, "GET", "http://"+publicHost+string(asset[1]), "", "", nil, nil)
	if javascript.StatusCode != 200 || len(js) < 1000 {
		t.Fatal("public HTTP JavaScript unavailable")
	}
	for _, suffix := range []string{"api/shops", "v1/me", "session/exchange"} {
		response, _ := request(plain, "GET", "http://"+publicHost+"/"+suffix, "", "", nil, nil)
		if response.StatusCode != 404 {
			t.Fatalf("public static Host exposed %s: %d", suffix, response.StatusCode)
		}
	}
	writable, _ := request(plain, "POST", publicURL, "", "", nil, nil)
	if writable.StatusCode < 400 {
		t.Fatal("public static Host accepted write method")
	}
	privateOverHTTP, privateBytes := request(plain, "GET", "http://"+previewHost+"/", "", "", nil, nil)
	privateHash := sha256.Sum256(privateBytes)
	if privateOverHTTP.StatusCode == 200 && hex.EncodeToString(privateHash[:]) == expectedHTML {
		t.Fatal("private Preview leaked through public HTTP")
	}
	observed, data := request(client, "GET", aliasURL, owner, "", nil, nil)
	if observed.StatusCode != 200 || !bytes.Contains(data, []byte(`"state":"active"`)) {
		t.Fatal("production HTTP alias not observed active")
	}
	old := &http.Client{Timeout: 8 * time.Second, Transport: &http.Transport{Proxy: nil}}
	legacy, err := old.Get("https://" + ip + "/")
	if err != nil {
		t.Fatal("old IP TLS regression: ", err)
	}
	_ = legacy.Body.Close()
	if legacy.StatusCode != 401 {
		t.Fatalf("old IP changed: %d", legacy.StatusCode)
	}
	t.Logf("publicHost=%s protocol=HTTP production=active HTML_SHA256=%s JS_bytes=%d", publicHost, expectedHTML, len(js))
}
