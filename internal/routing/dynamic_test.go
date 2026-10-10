package routing

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDynamicPreviewStreamsAfterAuthAndRollbackRetainsHealthyStatic(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/hang" {
			select {
			case <-r.Context().Done():
			case <-time.After(500 * time.Millisecond):
			}
			return
		}
		if r.Header.Get("Authorization") != "" || strings.Contains(r.Header.Get("Cookie"), previewCookie) {
			t.Error("platform credential leaked into tenant runtime")
		}
		if r.URL.Path == "/stream" {
			w.Write([]byte("first\n"))
			w.(http.Flusher).Flush()
			time.Sleep(80 * time.Millisecond)
			w.Write([]byte("second\n"))
			return
		}
		w.Header().Add("Set-Cookie", previewCookie+"=stolen; Path=/")
		w.Write([]byte("dynamic:" + r.URL.Path))
	}))
	defer upstream.Close()
	r := NewRegistry()
	old, err := r.Register(static(t, "t_a", "p_a", "healthy-old"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.SwapAlias("site.example.test", "t_a", "p_a", 0, old, false); err != nil {
		t.Fatal(err)
	}
	const dynamicID = "dep_11111111111111111111111111111111"
	if err = r.RegisterRuntime("t_a", "p_a", dynamicID, upstream.URL, 1, 150*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err = r.BindFixed("d-runtime.preview.test", dynamicID); err != nil {
		t.Fatal(err)
	}
	if _, err = r.SwapAlias("site.example.test", "t_a", "p_a", 1, dynamicID, false); err != nil {
		t.Fatal(err)
	}
	allowed := false
	h := NewGateway(r, func(_ *http.Request, route Route) bool { return allowed && route.DeploymentID == dynamicID })
	request := func(method, host, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "https://"+host+path, strings.NewReader("hello"))
		req.Header.Set("Authorization", "Bearer platform-secret")
		req.Header.Set("Cookie", previewCookie+"=private-token; app=tenant")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	if w := request(http.MethodGet, "d-runtime.preview.test", "/api"); w.Code != 401 {
		t.Fatalf("anonymous preview reached Node: %d", w.Code)
	}
	allowed = true
	if w := request(http.MethodPost, "d-runtime.preview.test", "/api"); w.Code != 200 || w.Body.String() != "dynamic:/api" || strings.Contains(strings.Join(w.Header().Values("Set-Cookie"), ";"), previewCookie) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("private dynamic response leaked auth or lost POST: %d %q %+v", w.Code, w.Body.String(), w.Header())
	}
	if w := request(http.MethodGet, "d-runtime.preview.test", "/stream"); w.Code != 200 || w.Body.String() != "first\nsecond\n" {
		t.Fatalf("streaming response: %d %q", w.Code, w.Body.String())
	}
	if w := request(http.MethodGet, "site.example.test", "/hang"); w.Code != 504 {
		t.Fatalf("runtime timeout: %d", w.Code)
	}
	if w := request(http.MethodGet, "site.example.test", "/"); w.Code != 200 || !strings.Contains(w.Body.String(), "healthy-old") {
		t.Fatalf("runtime failure did not restore previous healthy deployment: %d %q", w.Code, w.Body.String())
	}
	if route, ok := r.Resolve("site.example.test"); !ok || route.DeploymentID != old || route.Generation != 3 {
		t.Fatalf("runtime failure did not persist CAS rollback: %+v", route)
	}
	if w := request(http.MethodGet, "d-runtime.preview.test", "/api"); w.Code != 404 {
		t.Fatalf("failed runtime remained reachable at fixed Preview: %d", w.Code)
	}
}

func TestDynamicRuntimeConcurrencyAndBodyLimits(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/hold" {
			close(started)
			<-release
		}
		w.Write([]byte("ok"))
	}))
	defer upstream.Close()
	r := NewRegistry()
	const id = "dep_22222222222222222222222222222222"
	if err := r.RegisterRuntime("t_a", "p_a", id, upstream.URL, 1, time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SwapAlias("site.example.test", "t_a", "p_a", 0, id, false); err != nil {
		t.Fatal(err)
	}
	h := NewGateway(r, nil)
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "https://site.example.test/hold", nil))
		firstDone <- w
	}()
	<-started
	busy := httptest.NewRecorder()
	h.ServeHTTP(busy, httptest.NewRequest(http.MethodGet, "https://site.example.test/", nil))
	if busy.Code != 503 {
		t.Fatalf("second concurrent request not bounded: %d", busy.Code)
	}
	close(release)
	if done := <-firstDone; done.Code != 200 || done.Body.String() != "ok" {
		t.Fatalf("first request failed: %d", done.Code)
	}
	large := httptest.NewRecorder()
	h.ServeHTTP(large, httptest.NewRequest(http.MethodPost, "https://site.example.test/upload", strings.NewReader(strings.Repeat("x", maxRuntimeRequest+1))))
	if large.Code != 413 {
		t.Fatalf("oversized request reached runtime: %d", large.Code)
	}
	for _, endpoint := range []string{"http://169.254.169.254:8080", "http://localhost:8080", "http://127.0.0.1:bad", "http://127.0.0.1:9000/path"} {
		if err := r.RegisterRuntime("t_a", "p_a", "dep_33333333333333333333333333333333", endpoint, 1, time.Second); err == nil {
			t.Fatalf("unsafe runtime upstream accepted: %s", endpoint)
		}
	}
}

func TestDynamicGatewayFlushesBeforeSecondChunk(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("start\n"))
		w.(http.Flusher).Flush()
		time.Sleep(180 * time.Millisecond)
		w.Write([]byte("done\n"))
	}))
	defer upstream.Close()
	r := NewRegistry()
	const id = "dep_44444444444444444444444444444444"
	if err := r.RegisterRuntime("t_a", "p_a", id, upstream.URL, 1, time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SwapAlias("site.example.test", "t_a", "p_a", 0, id, false); err != nil {
		t.Fatal(err)
	}
	front := httptest.NewTLSServer(NewGateway(r, nil))
	defer front.Close()
	req, err := http.NewRequest(http.MethodGet, front.URL+"/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "site.example.test"
	response, err := front.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	first := make([]byte, len("start\n"))
	if _, err := io.ReadFull(response.Body, first); err != nil || string(first) != "start\n" {
		t.Fatalf("first chunk not streamed: %q %v", first, err)
	}
	started := time.Now()
	second, err := io.ReadAll(response.Body)
	if err != nil || string(second) != "done\n" || time.Since(started) < 80*time.Millisecond {
		t.Fatalf("response was buffered instead of streamed: %q %v", second, err)
	}
}
