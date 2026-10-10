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
	"strings"
	"testing"
	"time"
)

func TestLiveActionsOIDCPilotPrivateCandidateIsActuallyServed(t *testing.T) {
	ip := os.Getenv("EWP_LIVE_ACTIONS_ECS_IP")
	if ip == "" { t.Skip("set approved OIDC candidate and ECS identities explicitly") }
	if ip != "114.55.237.62" { t.Fatal("unexpected OIDC pilot ECS") }
	ca, err := os.ReadFile(os.Getenv("EWP_LIVE_ECS_CA_FILE"))
	if err != nil { t.Fatal("test CA unavailable") }
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) { t.Fatal("test CA invalid") }
	client := &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{Timeout: 5*time.Second}).DialContext(ctx, network, net.JoinHostPort(ip,"443"))
			}}}
	request := func(method, addr, bearer, origin string, body []byte, cookie *http.Cookie) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequest(method, addr, bytes.NewReader(body))
		if err != nil { t.Fatal(err) }
		if bearer != "" { req.Header.Set("Authorization", "Bearer "+bearer) }
		if origin != "" { req.Header.Set("Origin", origin); req.Header.Set("Content-Type","application/x-www-form-urlencoded") }
		if cookie != nil { req.AddCookie(cookie) }
		response, err := client.Do(req)
		if err != nil { t.Fatal(err) }
		payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		response.Body.Close()
		if err != nil { t.Fatal(err) }
		return response, payload
	}
	for _, item := range []struct{ label,team,project,dep,owner,other,htmlSHA string }{
		{"testA","t_f9cf963718a72612c48940af8a8296e4","p_34401f5c05af1079010cb95ee3ec1275",os.Getenv("EWP_LIVE_ACTIONS_A_DEPLOYMENT"),os.Getenv("EWP_LIVE_ECS_OWNER_A_FILE"),os.Getenv("EWP_LIVE_ECS_OWNER_B_FILE"),"e1026657cf7807381ea12f21325a8638decf0b1989eb42d4e549c3fb98a32ff6"},
		{"testB","t_eee8046ca49c271f5bea06acf285be66","p_e5123bae12388454bb82fc1590750ab9",os.Getenv("EWP_LIVE_ACTIONS_B_DEPLOYMENT"),os.Getenv("EWP_LIVE_ECS_OWNER_B_FILE"),os.Getenv("EWP_LIVE_ECS_OWNER_A_FILE"),"b0bf964b1808620fe848549c939140411499dc472e7540ebe5e0f4a278d0348a"},
	} {
		if item.dep == "" { continue }
		if !strings.HasPrefix(item.dep,"dep_") || len(item.dep)!=36 { t.Fatal("invalid OIDC candidate") }
		host := "d-"+strings.TrimPrefix(item.dep,"dep_")+".preview.ecp.lingtong.com"
		preview := "https://"+host+"/"
		owner, other := protectedToken(t,item.owner), protectedToken(t,item.other)
		api := fmt.Sprintf("https://console.ecp.lingtong.com/v1/teams/%s/projects/%s/deployments/%s",item.team,item.project,item.dep)
		if anonymous,_ := request(http.MethodGet,preview,"","",nil,nil); anonymous.StatusCode!=401 { t.Fatalf("%s anonymous Preview exposed: %d",item.label,anonymous.StatusCode) }
		if cross,_ := request(http.MethodPost,api+"/preview-exchanges",other,"",nil,nil); cross.StatusCode!=404 { t.Fatalf("%s cross Team issued session: %d",item.label,cross.StatusCode) }
		issued,raw := request(http.MethodPost,api+"/preview-exchanges",owner,"",nil,nil)
		var code struct{ Code string `json:"code"`; Host string `json:"desiredHost"` }
		if issued.StatusCode!=201 || json.Unmarshal(raw,&code)!=nil || code.Code=="" || code.Host!=host { t.Fatalf("%s one-use Preview code unavailable: %d",item.label,issued.StatusCode) }
		exchanged,_ := request(http.MethodPost,"https://"+host+"/session/exchange","","https://console.ecp.lingtong.com",[]byte("code="+url.QueryEscape(code.Code)),nil)
		if exchanged.StatusCode!=303 || len(exchanged.Cookies())!=1 || !exchanged.Cookies()[0].Secure || !exchanged.Cookies()[0].HttpOnly { t.Fatalf("%s private session unavailable: %d",item.label,exchanged.StatusCode) }
		page,html := request(http.MethodGet,preview,"","",nil,exchanged.Cookies()[0])
		digest := sha256.Sum256(html)
		if page.StatusCode!=200 || hex.EncodeToString(digest[:])!=item.htmlSHA { t.Fatalf("%s built bytes changed: %d",item.label,page.StatusCode) }
		status,body := request(http.MethodGet,api,owner,"",nil,nil)
		if status.StatusCode!=200 || !bytes.Contains(body,[]byte(`"observedState":"preview_ready"`)) { t.Fatalf("%s Preview not observed: %d",item.label,status.StatusCode) }
		t.Logf("oidc_private_preview=%s deployment=%s TLS=verified HTML_SHA256=%s",host,item.dep,item.htmlSHA)
	}
}
