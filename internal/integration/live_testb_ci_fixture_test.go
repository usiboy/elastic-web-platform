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

// Opt-in evidence for a fixed public testB GitHub Actions artifact imported
// through the operator fixture path. It does not claim automatic Builder output.
func TestLiveTestBCIFixtureRemainsPrivateAndTenantScoped(t *testing.T) {
	ip := os.Getenv("EWP_LIVE_TESTB_CFAA_IP")
	if ip == "" {
		t.Skip("explicit testB CI fixture ECS opt-in required")
	}
	if ip != "114.55.237.62" {
		t.Fatal("unexpected ECS destination")
	}
	ca, err := os.ReadFile(os.Getenv("EWP_LIVE_ECS_CA_FILE"))
	if err != nil {
		t.Fatal("private test CA unavailable")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("invalid test CA")
	}
	client := &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: &http.Transport{
		Proxy:           nil,
		TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip, "443"))
		},
	}}
	request := func(method, target, owner, origin string, body []byte, cookie *http.Cookie) (*http.Response, []byte) {
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
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
		_ = response.Body.Close()
		if err != nil || len(data) > 1<<20 {
			t.Fatal("CI fixture response exceeded test limit")
		}
		return response, data
	}
	const team = "t_eee8046ca49c271f5bea06acf285be66"
	const project = "p_e5123bae12388454bb82fc1590750ab9"
	const host = "b-cfaa2f2.preview.ecp.lingtong.com"
	const expectedHTML = "b0bf964b1808620fe848549c939140411499dc472e7540ebe5e0f4a278d0348a"
	const expectedJS = "0d4f51df202ba1f460cfc674d5c3212ef7856cc9f1c7a3eb5f4e6c7a38f5f07b"
	owner := protectedToken(t, os.Getenv("EWP_LIVE_ECS_OWNER_B_FILE"))
	aOwner := protectedToken(t, os.Getenv("EWP_LIVE_ECS_OWNER_A_FILE"))
	api := fmt.Sprintf("https://console.ecp.lingtong.com/v1/teams/%s/projects/%s", team, project)
	response, data := request(http.MethodGet, api+"/deployments", owner, "", nil, nil)
	if response.StatusCode != 200 {
		t.Fatalf("testB deployment history unavailable: %d", response.StatusCode)
	}
	var list struct {
		Deployments []struct {
			ID   string `json:"deploymentId"`
			Host string `json:"desiredHost"`
		} `json:"deployments"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatal(err)
	}
	var deployment string
	for _, item := range list.Deployments {
		if item.Host == host {
			if deployment != "" {
				t.Fatal("multiple candidates share one fixed Host")
			}
			deployment = item.ID
		}
	}
	if deployment == "" {
		t.Fatal("fixed GitHub CI fixture not found")
	}
	previewURL := "https://" + host + "/"
	deadline := time.Now().Add(12 * time.Second)
	for {
		anonymous, _ := request(http.MethodGet, previewURL, "", "", nil, nil)
		if anonymous.StatusCode == 401 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("private B Host not protected: %d", anonymous.StatusCode)
		}
		time.Sleep(200 * time.Millisecond)
	}
	issueURL := api + "/deployments/" + deployment + "/preview-exchanges"
	if cross, _ := request(http.MethodPost, issueURL, aOwner, "", nil, nil); cross.StatusCode != 404 {
		t.Fatalf("A Owner issued B Preview session: %d", cross.StatusCode)
	}
	issued, data := request(http.MethodPost, issueURL, owner, "", nil, nil)
	var code struct {
		Code string `json:"code"`
		Host string `json:"desiredHost"`
	}
	if issued.StatusCode != 201 || json.Unmarshal(data, &code) != nil || code.Code == "" || code.Host != host {
		t.Fatalf("B Preview exchange code unavailable: %d", issued.StatusCode)
	}
	exchange, _ := request(http.MethodPost, "https://"+host+"/session/exchange", "", "https://console.ecp.lingtong.com", []byte("code="+url.QueryEscape(code.Code)), nil)
	if exchange.StatusCode != 303 || len(exchange.Cookies()) != 1 || !exchange.Cookies()[0].Secure || !exchange.Cookies()[0].HttpOnly {
		t.Fatalf("B Preview cookie invalid: %d", exchange.StatusCode)
	}
	page, html := request(http.MethodGet, previewURL, "", "", nil, exchange.Cookies()[0])
	hash := sha256.Sum256(html)
	if page.StatusCode != 200 || hex.EncodeToString(hash[:]) != expectedHTML {
		t.Fatalf("B private CI index differs: %d", page.StatusCode)
	}
	asset := regexp.MustCompile(`src="(/assets/[^" ]+\.js)"`).FindSubmatch(html)
	if len(asset) < 2 {
		t.Fatal("CI JavaScript reference missing")
	}
	jsPage, js := request(http.MethodGet, "https://"+host+string(asset[1]), "", "", nil, exchange.Cookies()[0])
	jsHash := sha256.Sum256(js)
	if jsPage.StatusCode != 200 || hex.EncodeToString(jsHash[:]) != expectedJS {
		t.Fatalf("B private CI JavaScript differs: %d", jsPage.StatusCode)
	}
	if cross, _ := request(http.MethodGet, "https://b.preview.ecp.lingtong.com/", "", "", nil, exchange.Cookies()[0]); cross.StatusCode != 401 {
		t.Fatalf("B new Host cookie crossed to old Host: %d", cross.StatusCode)
	}
	t.Logf("testB_commit=cfaa2f283f5ced67b85899919fe10a4a9f99d014 fixture=%s host=%s TLS=verified HTML_SHA256=%s JS_SHA256=%s", deployment, host, expectedHTML, expectedJS)
}
