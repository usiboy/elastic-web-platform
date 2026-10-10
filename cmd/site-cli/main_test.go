package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeployCLIUploadsRealBytesAndReturnsCandidateState(t *testing.T) {
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	content := "<h1>From Agent</h1>"
	if err := tw.WriteHeader(&tar.Header{Name: "index.html", Size: int64(len(content)), Mode: 0644}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(tw, content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "site.tar.gz")
	if err := os.WriteFile(file, archive.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	called := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ewp_agent_test" {
			t.Errorf("missing scoped Agent token")
		}
		if r.Header.Get("Idempotency-Key") == "" {
			t.Errorf("missing idempotency key")
		}
		switch r.URL.Path {
		case "/v1/teams/t_alpha/projects/p_beta/sources/uploads":
			called++
			if r.Header.Get("Content-Type") != "application/gzip" {
				t.Errorf("wrong upload type")
			}
			got, _ := io.ReadAll(r.Body)
			if !bytes.Equal(got, archive.Bytes()) {
				t.Errorf("archive bytes changed")
			}
			w.WriteHeader(201)
			_, _ = io.WriteString(w, `{"sourceId":"src_001","status":"stored_unverified"}`)
		case "/v1/teams/t_alpha/projects/p_beta/deployments":
			called++
			var input map[string]string
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input["sourceId"] != "src_001" {
				t.Errorf("incorrect deployment source: %v %v", input, err)
			}
			w.WriteHeader(202)
			_, _ = io.WriteString(w, `{"deploymentId":"dep_001","desiredState":"candidate","observedState":"not_deployed"}`)
		default:
			t.Errorf("unexpected API path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	var output bytes.Buffer
	err := run(context.Background(), []string{"deploy", "--team", "t_alpha", "--project", "p_beta", "--archive", file, "--api", server.URL, "--request-id", "safe-retry"}, "ewp_agent_test", server.Client(), &output)
	if err != nil || called != 2 || !strings.Contains(output.String(), `"deploymentId": "dep_001"`) || strings.Contains(output.String(), "ewp_agent_test") {
		t.Fatalf("CLI deploy result: %v calls=%d output=%q", err, called, output.String())
	}
}

func TestCLIDisallowsNonLoopbackPlaintextAndMissingToken(t *testing.T) {
	for _, tc := range []struct{ url, token string }{{"http://example.com", "ewp_test"}, {"http://127.0.0.1:8097", ""}} {
		if err := run(context.Background(), []string{"status", "--team", "t_a", "--project", "p_b", "--deployment", "dep_x", "--api", tc.url}, tc.token, nil, io.Discard); err == nil {
			t.Fatalf("unsafe CLI config accepted: %+v", tc)
		}
	}
}

func TestDeployRejectsUnsafeRequestIDBeforeReadingArchive(t *testing.T) {
	err := run(context.Background(), []string{"deploy", "--team", "t_alpha", "--project", "p_beta", "--request-id", "bad\r\nX-Other: injected", "--archive", "/nonexistent"}, "ewp_test", nil, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "request ID") {
		t.Fatalf("header-like request ID was not rejected first: %v", err)
	}
}

func TestPreviewCommandExchangesOneTimeCodeWithoutPrintingOrForwardingTokens(t *testing.T) {
	var siteCalls int
	code := "ewp_" + strings.Repeat("x", 43)
	site := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("Agent Bearer token leaked to site Host")
		}
		switch r.URL.Path {
		case "/session/exchange":
			siteCalls++
			if r.Header.Get("Origin") != "https://console.preview.test" || r.FormValue("code") != code {
				t.Error("incorrect Preview exchange")
			}
			http.SetCookie(w, &http.Cookie{Name: "__Host-ewp_preview", Value: "v1_cookie", Secure: true, HttpOnly: true, Path: "/"})
			w.Header().Set("Location", "/")
			w.WriteHeader(303)
		case "/":
			siteCalls++
			if _, err := r.Cookie("__Host-ewp_preview"); err != nil {
				w.WriteHeader(401)
				return
			}
			_, _ = io.WriteString(w, "<h1>Private site</h1>")
		default:
			w.WriteHeader(404)
		}
	}))
	defer site.Close()
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ewp_agent_test" {
			t.Error("control request missing scoped Agent token")
		}
		if r.URL.Path != "/v1/teams/t_alpha/projects/p_beta/deployments/dep_001/preview-exchanges" {
			t.Errorf("unexpected control path %s", r.URL.Path)
		}
		w.WriteHeader(201)
		_, _ = io.WriteString(w, `{"code":"`+code+`","desiredHost":"`+strings.TrimPrefix(site.URL, "https://")+`"}`)
	}))
	defer control.Close()
	var output bytes.Buffer
	err := run(context.Background(), []string{"preview", "--team", "t_alpha", "--project", "p_beta", "--deployment", "dep_001", "--api", control.URL, "--control-origin", "https://console.preview.test"}, "ewp_agent_test", site.Client(), &output)
	if err != nil || siteCalls != 2 || !strings.Contains(output.String(), `"httpStatus": 200`) || strings.Contains(output.String(), code) || strings.Contains(output.String(), "ewp_agent_test") {
		t.Fatalf("preview command: %v calls=%d output=%q", err, siteCalls, output.String())
	}
}
