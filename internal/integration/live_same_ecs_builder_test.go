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

// Opt-in: reviewed public Git sources built under runsc on the original ECS
// via a separate network-namespace Docker daemon. Optional signed candidates
// verify the bounded one-shot operator job path, not general tenant admission.
func TestLiveOriginalECSBuilderABPrivatePreviewAndLegacySafety(t *testing.T) {
	ip := os.Getenv("EWP_LIVE_ORIGINAL_ECS_BUILDER_IP")
	if ip == "" {
		t.Skip("set explicit original-ECS Builder preview test scope")
	}
	if ip != "114.55.237.62" {
		t.Fatal("unexpected ECS destination")
	}
	ca, err := os.ReadFile(os.Getenv("EWP_LIVE_ECS_CA_FILE"))
	if err != nil {
		t.Fatal("test CA unavailable")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("invalid CA")
	}
	client := &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: &http.Transport{
		Proxy:           nil,
		TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip, "443"))
		}}}
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
		payload, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
		_ = response.Body.Close()
		if err != nil || len(payload) > 1<<20 {
			t.Fatal("private Builder preview exceeded test limit")
		}
		return response, payload
	}
	fixtures := []struct {
		team, project, host, htmlSHA, jsSHA, ownerFile string
		signed                                         bool
	}{
		{"t_f9cf963718a72612c48940af8a8296e4", "p_34401f5c05af1079010cb95ee3ec1275", "a-ecsrunsc.preview.ecp.lingtong.com",
			"e1026657cf7807381ea12f21325a8638decf0b1989eb42d4e549c3fb98a32ff6", "78d59d5fb71dc694944b2f47c49b2190cb4b2a5116827295ccce49093dd5272a", os.Getenv("EWP_LIVE_ECS_OWNER_A_FILE"), false},
		{"t_eee8046ca49c271f5bea06acf285be66", "p_e5123bae12388454bb82fc1590750ab9", "b-ecsrunsc.preview.ecp.lingtong.com",
			"b0bf964b1808620fe848549c939140411499dc472e7540ebe5e0f4a278d0348a", "0d4f51df202ba1f460cfc674d5c3212ef7856cc9f1c7a3eb5f4e6c7a38f5f07b", os.Getenv("EWP_LIVE_ECS_OWNER_B_FILE"), false},
	}
	if signedIP := os.Getenv("EWP_LIVE_SIGNED_ECS_IP"); signedIP != "" {
		if signedIP != ip {
			t.Fatal("signed candidate target differs from original ECS")
		}
		fixtures = append(fixtures,
			struct {
				team, project, host, htmlSHA, jsSHA, ownerFile string
				signed                                         bool
			}{"t_f9cf963718a72612c48940af8a8296e4", "p_34401f5c05af1079010cb95ee3ec1275", "d-f243de7290f4ae2f40c0bcaa626185f5.preview.ecp.lingtong.com", "e1026657cf7807381ea12f21325a8638decf0b1989eb42d4e549c3fb98a32ff6", "78d59d5fb71dc694944b2f47c49b2190cb4b2a5116827295ccce49093dd5272a", os.Getenv("EWP_LIVE_ECS_OWNER_A_FILE"), true},
			struct {
				team, project, host, htmlSHA, jsSHA, ownerFile string
				signed                                         bool
			}{"t_eee8046ca49c271f5bea06acf285be66", "p_e5123bae12388454bb82fc1590750ab9", "d-d207e6a36f7c27e58a5b3463e634c897.preview.ecp.lingtong.com", "b0bf964b1808620fe848549c939140411499dc472e7540ebe5e0f4a278d0348a", "0d4f51df202ba1f460cfc674d5c3212ef7856cc9f1c7a3eb5f4e6c7a38f5f07b", os.Getenv("EWP_LIVE_ECS_OWNER_B_FILE"), true})
	}
	cookies := make([]*http.Cookie, len(fixtures))
	for i, item := range fixtures {
		owner := protectedToken(t, item.ownerFile)
		other := protectedToken(t, fixtures[1-(i%2)].ownerFile)
		api := fmt.Sprintf("https://console.ecp.lingtong.com/v1/teams/%s/projects/%s", item.team, item.project)
		listed, body := request(http.MethodGet, api+"/deployments", owner, "", nil, nil)
		if listed.StatusCode != 200 {
			t.Fatalf("%s deployment list unavailable: %d", item.host, listed.StatusCode)
		}
		var page struct {
			Deployments []struct {
				ID         string `json:"deploymentId"`
				Host       string `json:"desiredHost"`
				SourceKind string `json:"sourceKind"`
			} `json:"deployments"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			t.Fatal(err)
		}
		var deployment string
		for _, entry := range page.Deployments {
			if entry.Host == item.host {
				if deployment != "" {
					t.Fatal("duplicate private Host")
				}
				if item.signed && entry.SourceKind != "github" {
					t.Fatalf("%s signed build is not Git-sourced: %s", item.host, entry.SourceKind)
				}
				deployment = entry.ID
			}
		}
		if deployment == "" {
			t.Fatalf("%s fixed source candidate missing", item.host)
		}
		preview := "https://" + item.host + "/"
		deadline := time.Now().Add(12 * time.Second)
		for {
			anonymous, _ := request(http.MethodGet, preview, "", "", nil, nil)
			if anonymous.StatusCode == 401 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s anonymous Preview not protected: %d", item.host, anonymous.StatusCode)
			}
			time.Sleep(200 * time.Millisecond)
		}
		issueURL := api + "/deployments/" + deployment + "/preview-exchanges"
		if cross, _ := request(http.MethodPost, issueURL, other, "", nil, nil); cross.StatusCode != 404 {
			t.Fatalf("%s cross-Team session issued: %d", item.host, cross.StatusCode)
		}
		issued, raw := request(http.MethodPost, issueURL, owner, "", nil, nil)
		var code struct {
			Code string `json:"code"`
			Host string `json:"desiredHost"`
		}
		if issued.StatusCode != 201 || json.Unmarshal(raw, &code) != nil || code.Code == "" || code.Host != item.host {
			t.Fatalf("%s scoped code invalid: %d", item.host, issued.StatusCode)
		}
		exchange, _ := request(http.MethodPost, "https://"+item.host+"/session/exchange", "", "https://console.ecp.lingtong.com", []byte("code="+url.QueryEscape(code.Code)), nil)
		if exchange.StatusCode != 303 || len(exchange.Cookies()) != 1 || !exchange.Cookies()[0].Secure || !exchange.Cookies()[0].HttpOnly || exchange.Cookies()[0].Domain != "" {
			t.Fatalf("%s session invalid: %d", item.host, exchange.StatusCode)
		}
		cookies[i] = exchange.Cookies()[0]
		response, html := request(http.MethodGet, preview, "", "", nil, cookies[i])
		hash := sha256.Sum256(html)
		if response.StatusCode != 200 || hex.EncodeToString(hash[:]) != item.htmlSHA {
			t.Fatalf("%s HTML differs from fixed CI output", item.host)
		}
		asset := regexp.MustCompile(`src="(/assets/[^" ]+\.js)"`).FindSubmatch(html)
		if len(asset) < 2 {
			t.Fatal("built JavaScript missing")
		}
		jsResponse, js := request(http.MethodGet, "https://"+item.host+string(asset[1]), "", "", nil, cookies[i])
		jsHash := sha256.Sum256(js)
		if jsResponse.StatusCode != 200 || hex.EncodeToString(jsHash[:]) != item.jsSHA {
			t.Fatalf("%s JS differs from fixed CI output", item.host)
		}
		if item.signed {
			statusResponse, statusBody := request(http.MethodGet, api+"/github-pushes", owner, "", nil, nil)
			var history struct {
				Pushes []struct {
					DeploymentID string `json:"deploymentId"`
					Status       string `json:"status"`
					Origin       string `json:"origin"`
				} `json:"pushes"`
			}
			if statusResponse.StatusCode != 200 || json.Unmarshal(statusBody, &history) != nil {
				t.Fatalf("%s Git history unavailable", item.host)
			}
			found := false
			for _, push := range history.Pushes {
				if push.DeploymentID == deployment && push.Origin == "app_poll" && push.Status == "preview_candidate" {
					found = true
				}
			}
			if !found {
				t.Fatalf("%s fixed Git status not connected to signed candidate", item.host)
			}
		}
		t.Logf("host=%s TLS=verified private=1 deployment=%s html_sha256=%s js_sha256=%s", item.host, deployment, item.htmlSHA, item.jsSHA)
	}
	if cross, _ := request(http.MethodGet, "https://"+fixtures[1].host+"/", "", "", nil, cookies[0]); cross.StatusCode != 401 {
		t.Fatalf("A Builder cookie crossed to B: %d", cross.StatusCode)
	}
	if len(fixtures) == 4 {
		if cross, _ := request(http.MethodGet, "https://"+fixtures[3].host+"/", "", "", nil, cookies[2]); cross.StatusCode != 401 {
			t.Fatalf("A signed Preview cookie crossed to B: %d", cross.StatusCode)
		}
	}
	legacy := &http.Client{Timeout: 8 * time.Second, Transport: &http.Transport{Proxy: nil}}
	old, err := legacy.Get("https://" + ip + "/")
	if err != nil {
		t.Fatal("old IP TLS regressed: ", err)
	}
	_ = old.Body.Close()
	if old.StatusCode != 401 {
		t.Fatalf("old IP changed: %d", old.StatusCode)
	}
}
