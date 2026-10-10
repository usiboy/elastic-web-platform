package routing

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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"elastic-web-platform/internal/bundle"
)

func TestRealTrustedABAssetsAcrossLocalHostsAndTLS(t *testing.T) {
	root := os.Getenv("EWP_LIVE_GITHUB_OUTPUT_DIR")
	if root == "" {
		t.Skip("requires explicit trusted A/B build artifacts")
	}
	db := routeDatabase(t)
	ctx := context.Background()
	durable := NewDurableRegistry(db, bundle.NewDiskStore(t.TempDir()))
	fixtures := []struct{ team, project, sourceSHA, artifact, hostname, builder string }{
		{"t_alpha", "p_alpha", "06eb9696ea468930721f5b4f3eff2d90819ef3553385ca2a0a342b4585c0eaa7", "testA-built-b7dc40fd3655.tar.gz", "a.preview.ecp.lingtong.com", "operator-fixture:node22-vite7"},
		{"t_other", "p_other", "044e7ed94cdcb02c1b1ff6e6654a12ba8e89d46d9eda7e55ce7eaf5cd30d906a", "testB-built-no-treeshake-9ea1fc0c7037.tar.gz", "b.preview.ecp.lingtong.com", "operator-fixture:node22-vite7-no-treeshake"},
	}
	var deployments [2]string
	var bundles [2]*bundle.Static
	for index, item := range fixtures {
		archive, err := os.ReadFile(filepath.Join(root, item.artifact))
		if err != nil {
			t.Fatal(err)
		}
		b, err := bundle.CaptureBuiltArchive(item.team, item.project, item.sourceSHA, item.builder, archive)
		if err != nil {
			t.Fatal(err)
		}
		id, err := durable.RegisterOperatorFixture(ctx, b)
		if err != nil {
			t.Fatal(err)
		}
		if err := durable.BindFixed(ctx, item.hostname, id); err != nil {
			t.Fatal(err)
		}
		deployments[index] = id
		bundles[index] = b
		secret := sha256.Sum256([]byte("ewp_owner_" + item.team))
		if _, err := db.Exec(ctx, `INSERT INTO access_tokens(id,team_id,token_hash,role,expires_at) VALUES($1,$2,$3,'owner',$4)`, "key_"+item.team, item.team, secret[:], time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		code := "ewp_" + strings.Repeat(string('a'+rune(index)), 43)
		codeHash := sha256.Sum256([]byte(code))
		if _, err := db.Exec(ctx, `INSERT INTO preview_exchanges(code_hash,team_id,project_id,deployment_id,actor_token_id,expires_at) VALUES($1,$2,$3,$4,$5,$6)`, codeHash[:], item.team, item.project, id, "key_"+item.team, time.Now().Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := durable.LoadSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sessions := NewPreviewSessions(db, "https://console.ecp.lingtong.com")
	server := httptest.NewUnstartedServer(NewGatewayWithSessions(snapshot, sessions, durable))
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(12), Subject: pkix.Name{CommonName: fixtures[0].hostname}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{fixtures[0].hostname, fixtures[1].hostname}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	trust := x509.NewCertPool()
	trust.AddCert(parsed)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	server.StartTLS()
	defer server.Close()
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: trust, MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var cookies [2]*http.Cookie
	for index, item := range fixtures {
		request, err := http.NewRequest(http.MethodGet, "https://"+item.hostname+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 401 {
			t.Fatalf("anonymous %s: %d", item.hostname, resp.StatusCode)
		}
		code := "ewp_" + strings.Repeat(string('a'+rune(index)), 43)
		form := url.Values{"code": {code}}.Encode()
		request, err = http.NewRequest(http.MethodPost, "https://"+item.hostname+"/session/exchange", strings.NewReader(form))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Origin", "https://console.ecp.lingtong.com")
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err = client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 303 || len(resp.Cookies()) != 1 {
			t.Fatalf("exchange %s: %d", item.hostname, resp.StatusCode)
		}
		cookies[index] = resp.Cookies()[0]
		request, err = http.NewRequest(http.MethodGet, "https://"+item.hostname+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.AddCookie(cookies[index])
		resp, err = client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		expected, _ := bundles[index].Read("/index.html")
		if resp.StatusCode != 200 || string(content) != string(expected) {
			t.Fatalf("wrong static HTML for %s: %d", item.hostname, resp.StatusCode)
		}
		var js string
		for _, asset := range bundles[index].Manifest.Files {
			if strings.HasSuffix(asset.Path, ".js") {
				js = asset.Path
				break
			}
		}
		if js == "" {
			t.Fatal("missing JS")
		}
		request, err = http.NewRequest(http.MethodGet, "https://"+item.hostname+js, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.AddCookie(cookies[index])
		resp, err = client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		actual, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		expected, _ = bundles[index].Read(js)
		if resp.StatusCode != 200 || string(actual) != string(expected) {
			t.Fatalf("wrong static JS for %s: %d", item.hostname, resp.StatusCode)
		}
		t.Logf("hostname=%s repositorySourceSha256=%s deploymentID=%s bundleDigest=%s", item.hostname, item.sourceSHA, deployments[index], bundles[index].Manifest.Digest)
	}
	request, err := http.NewRequest(http.MethodGet, "https://"+fixtures[1].hostname+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(cookies[0])
	resp, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("tenant A preview session crossed to B: %d", resp.StatusCode)
	}
}
