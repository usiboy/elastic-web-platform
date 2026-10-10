package routing

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRealTLSHostsPreviewProtectionAndProductionRollback(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{
		SerialNumber: big.NewInt(99), Subject: pkix.Name{CommonName: "site.example.test"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		DNSNames: []string{"site.example.test", "d-one.preview.test"},
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(parsed)
	r := NewRegistry()
	a, err := r.Register(static(t, "t_a", "p_a", "one"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.Register(static(t, "t_a", "p_a", "two"))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.BindFixed("d-one.preview.test", a); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SwapAlias("site.example.test", "t_a", "p_a", 0, a, false); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(NewGateway(r, func(req *http.Request, _ Route) bool { return req.Header.Get("X-Test-Preview") == "yes" }))
	srv.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	srv.StartTLS()
	defer srv.Close()
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
		},
	}}
	fetch := func(host, path, overrideHost string, preview bool) (int, string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if overrideHost != "" {
			req.Host = overrideHost
		}
		if preview {
			req.Header.Set("X-Test-Preview", "yes")
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		buf := make([]byte, 128)
		n, _ := resp.Body.Read(buf)
		return resp.StatusCode, string(buf[:n])
	}
	if code, _ := fetch("d-one.preview.test", "/app.js", "", false); code != 401 {
		t.Fatalf("anonymous JS preview: %d", code)
	}
	if code, body := fetch("d-one.preview.test", "/index.html", "", true); code != 200 || body != "one" {
		t.Fatalf("preview body: %d %q", code, body)
	}
	if code, body := fetch("site.example.test", "/", "", false); code != 200 || body != "one" {
		t.Fatalf("production v1: %d %q", code, body)
	}
	if _, err := r.SwapAlias("site.example.test", "t_a", "p_a", 1, b, false); err != nil {
		t.Fatal(err)
	}
	if code, body := fetch("site.example.test", "/", "", false); code != 200 || body != "two" {
		t.Fatalf("production v2: %d %q", code, body)
	}
	if _, err := r.SwapAlias("site.example.test", "t_a", "p_a", 2, a, false); err != nil {
		t.Fatal(err)
	}
	if code, body := fetch("site.example.test", "/", "", false); code != 200 || body != "one" {
		t.Fatalf("rollback: %d %q", code, body)
	}
	if code, _ := fetch("site.example.test", "/", "d-one.preview.test", true); code != 421 {
		t.Fatalf("SNI/Host confusion: %d", code)
	}
}
