package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRelayForwardsSyntheticStreamingAndRefusesControlPath(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("start\n"))
		w.(http.Flusher).Flush()
		time.Sleep(100 * time.Millisecond)
		w.Write([]byte("done\n"))
	}))
	defer upstream.Close()
	handler, err := relayHandler(strings.TrimPrefix(upstream.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(handler)
	defer front.Close()
	response, err := front.Client().Get(front.URL + "/api/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	first := make([]byte, len("start\n"))
	if _, err := io.ReadFull(response.Body, first); err != nil || string(first) != "start\n" {
		t.Fatalf("first chunk: %s %v", first, err)
	}
	start := time.Now()
	second, err := io.ReadAll(response.Body)
	if err != nil || string(second) != "done\n" || time.Since(start) < 40*time.Millisecond {
		t.Fatalf("buffered stream: %s %v", second, err)
	}
	if denied, err := front.Client().Get(front.URL + "/__ewp/control/stop"); err != nil || denied.StatusCode != 404 {
		t.Fatalf("control path reachable: %v %v", denied, err)
	} else {
		denied.Body.Close()
	}
}

func TestRelayRejectsUnexpectedNetworkAndSocket(t *testing.T) {
	if admitted("/run/ewp-r2-20261007/v3.sock", "172.29.77.3:3047") != nil {
		t.Fatal("second synthetic runtime pair rejected")
	}
	if admitted("/run/ewp-r2-20261007/xianlixiu.sock", "172.29.77.4:3067") != nil {
		t.Fatal("synthetic original application pair rejected")
	}
	for _, item := range [][2]string{{"/tmp/runtime.sock", "172.29.77.2:3027"}, {"/run/ewp-r2-20261007/runtime.sock", "169.254.169.254:80"}} {
		if admitted(item[0], item[1]) == nil {
			t.Fatalf("untrusted relay accepted: %q", item)
		}
	}
}
