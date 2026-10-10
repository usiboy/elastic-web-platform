package httpapi

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"elastic-web-platform/internal/bundle"
	"elastic-web-platform/internal/routing"
	"elastic-web-platform/internal/source/uploads"
)

func TestAgentUploadToRealTLSPrivatePreviewAcrossControlAndSiteHandlers(t *testing.T) {
	db := testDatabase(t)
	sourceStore := uploads.NewDiskStore(t.TempDir())
	artifactStore := bundle.NewDiskStore(t.TempDir())
	control := NewServer(db, "bootstrap-local-test", WithSourceStore(sourceStore), WithArtifactStore(artifactStore), WithPreviewSuffix("preview.test"), WithProductionSuffix("sites.test"))
	a, owner := createTeam(t, control, "tls-preview-team")
	path := "/v1/teams/" + a + "/projects"
	_, p := request(t, control, http.MethodPost, path, owner, "site", map[string]string{"slug": "site", "name": "Site"})
	projectID := p["id"].(string)
	_, agent := request(t, control, http.MethodPost, "/v1/teams/"+a+"/tokens", owner, "agent", map[string]any{"role": "agent", "projectId": projectID, "actions": []string{"sources:upload", "deployments:create"}})
	source := deploymentSource(t, map[string]string{"index.html": "<h1>Agent built this</h1>", "app.js": "console.log(1)"})
	_, uploaded := uploadSource(t, control, path+"/"+projectID+"/sources/uploads", agent["token"].(string), "upload", source)
	code, candidate := request(t, control, http.MethodPost, path+"/"+projectID+"/deployments", agent["token"].(string), "candidate", map[string]string{"sourceId": uploaded["sourceId"].(string)})
	if code != 202 {
		t.Fatalf("candidate: %d %v", code, candidate)
	}
	host := candidate["desiredHost"].(string)
	durable := routing.NewDurableRegistry(db, artifactStore)
	preview := routing.NewPreviewSessions(db, "https://console.preview.test")
	live := routing.NewLiveGateway(durable, preview)
	if err := live.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	site := httptest.NewUnstartedServer(live)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(17), Subject: pkix.Name{CommonName: "*.preview.test"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{"*.preview.test", "*.sites.test"}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(parsed)
	site.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	site.StartTLS()
	defer site.Close()
	httpClient := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, site.Listener.Addr().String())
	}}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	visit := func(requestHost, path string, cookie *http.Cookie) (int, string) {
		req, err := http.NewRequest(http.MethodGet, "https://"+requestHost+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, string(body)
	}
	if code, _ := visit(host, "/app.js", nil); code != 401 {
		t.Fatalf("anonymous JS: %d", code)
	}
	exchangePath := path + "/" + projectID + "/deployments/" + candidate["deploymentId"].(string) + "/preview-exchanges"
	code, issued := request(t, control, http.MethodPost, exchangePath, owner, "", nil)
	if code != 201 {
		t.Fatalf("control exchange: %d", code)
	}
	form := url.Values{"code": {issued["code"].(string)}}.Encode()
	req, err := http.NewRequest(http.MethodPost, "https://"+host+"/session/exchange", strings.NewReader(form))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "https://console.preview.test")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 303 || len(resp.Cookies()) != 1 || !resp.Cookies()[0].Secure {
		t.Fatalf("real TLS session exchange: %d %v", resp.StatusCode, resp.Cookies())
	}
	if code, body := visit(host, "/", resp.Cookies()[0]); code != 200 || body != "<h1>Agent built this</h1>" {
		t.Fatalf("HTML: %d %q", code, body)
	}
	if code, body := visit(host, "/app.js", resp.Cookies()[0]); code != 200 || body != "console.log(1)" {
		t.Fatalf("JS: %d %q", code, body)
	}
	if code, ready := request(t, control, http.MethodGet, path+"/"+projectID+"/deployments/"+candidate["deploymentId"].(string), owner, "", nil); code != 200 || ready["observedState"] != "preview_ready" {
		t.Fatalf("real Preview not observed: %d %v", code, ready)
	}
	promotePath := path + "/" + projectID + "/aliases/production:promote"
	code, promotion := request(t, control, http.MethodPost, promotePath, owner, "", map[string]any{"deploymentId": candidate["deploymentId"], "expectedGeneration": 0})
	if code != 202 || promotion["observedGeneration"] != float64(0) {
		t.Fatalf("promotion intent: %d %v", code, promotion)
	}
	prodHost := promotion["desiredHost"].(string)
	if code, _ := visit(prodHost, "/", nil); code != 404 {
		t.Fatalf("unreloaded gateway served desired production: %d", code)
	}
	if err := live.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if code, body := visit(prodHost, "/", nil); code != 200 || body != "<h1>Agent built this</h1>" {
		t.Fatalf("observed production: %d %q", code, body)
	}
	aliasPath := path + "/" + projectID + "/aliases/production"
	if code, alias := request(t, control, http.MethodGet, aliasPath, owner, "", nil); code != 200 || alias["state"] != "active" || alias["observedGeneration"] != float64(1) {
		t.Fatalf("production not active after real TLS readback: %d %v", code, alias)
	}
	// A second Agent revision switches only after the live gateway reloads it.
	_, secondSource := uploadSource(t, control, path+"/"+projectID+"/sources/uploads", agent["token"].(string), "upload-2", deploymentSource(t, map[string]string{"index.html": "<h1>Second website</h1>"}))
	_, secondCandidate := request(t, control, http.MethodPost, path+"/"+projectID+"/deployments", agent["token"].(string), "candidate-2", map[string]string{"sourceId": secondSource["sourceId"].(string)})
	if err := live.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	host2 := secondCandidate["desiredHost"].(string)
	code, code2 := request(t, control, http.MethodPost, path+"/"+projectID+"/deployments/"+secondCandidate["deploymentId"].(string)+"/preview-exchanges", owner, "", nil)
	if code != 201 {
		t.Fatalf("second preview exchange: %d", code)
	}
	req2, err := http.NewRequest(http.MethodPost, "https://"+host2+"/session/exchange", strings.NewReader(url.Values{"code": {code2["code"].(string)}}.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req2.Header.Set("Origin", "https://console.preview.test")
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp2, err := httpClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 303 {
		t.Fatalf("second preview auth: %d", resp2.StatusCode)
	}
	if code, body := visit(host2, "/", resp2.Cookies()[0]); code != 200 || body != "<h1>Second website</h1>" {
		t.Fatalf("second preview: %d %q", code, body)
	}
	code, version2 := request(t, control, http.MethodPost, promotePath, owner, "", map[string]any{"deploymentId": secondCandidate["deploymentId"], "expectedGeneration": 1})
	if code != 202 || version2["observedGeneration"] != float64(1) {
		t.Fatalf("second desired promotion: %d %v", code, version2)
	}
	if code, body := visit(prodHost, "/", nil); code != 200 || body != "<h1>Agent built this</h1>" {
		t.Fatalf("old production lost before reload: %d %q", code, body)
	}
	if err := live.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if code, body := visit(prodHost, "/", nil); code != 200 || body != "<h1>Second website</h1>" {
		t.Fatalf("production v2: %d %q", code, body)
	}
	rollbackPath := path + "/" + projectID + "/aliases/production:rollback"
	if code, _ := request(t, control, http.MethodPost, rollbackPath, owner, "", map[string]any{"deploymentId": candidate["deploymentId"], "expectedGeneration": 2}); code != 202 {
		t.Fatalf("rollback request: %d", code)
	}
	if err := live.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if code, body := visit(prodHost, "/", nil); code != 200 || body != "<h1>Agent built this</h1>" {
		t.Fatalf("production rollback: %d %q", code, body)
	}
	if code, alias := request(t, control, http.MethodGet, aliasPath, owner, "", nil); code != 200 || alias["observedGeneration"] != float64(3) {
		t.Fatalf("rollback not observed: %d %v", code, alias)
	}
	ownerHash := sha256.Sum256([]byte(owner))
	if _, err := db.Exec(context.Background(), `UPDATE access_tokens SET revoked_at=now() WHERE token_hash=$1`, ownerHash[:]); err != nil {
		t.Fatal(err)
	}
	if code, _ := visit(host, "/", resp.Cookies()[0]); code != 401 {
		t.Fatalf("revoked owner retained preview: %d", code)
	}
}
