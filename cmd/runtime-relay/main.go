// runtime-relay runs in the isolated Builder network namespace and exposes a
// root-only Unix HTTP socket to the Gateway. It never receives Docker access.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func admitted(socket, upstream string) error {
	allowed := (socket == "/run/ewp-r2-20261007/runtime.sock" && upstream == "172.29.77.2:3027") ||
		(socket == "/run/ewp-r2-20261007/v3.sock" && upstream == "172.29.77.3:3047") ||
		(socket == "/run/ewp-r2-20261007/xianlixiu.sock" && upstream == "172.29.77.4:3067")
	if !allowed {
		return errors.New("runtime relay requires the fixed isolated endpoint")
	}
	return nil
}

func isolatedNetwork() bool {
	host, err := os.Stat("/proc/1/ns/net")
	if err != nil {
		return false
	}
	self, err := os.Stat("/proc/self/ns/net")
	if err != nil {
		return false
	}
	hostStat, hostOK := host.Sys().(*syscall.Stat_t)
	selfStat, selfOK := self.Sys().(*syscall.Stat_t)
	return hostOK && selfOK && hostStat.Ino != selfStat.Ino
}

func relayHandler(upstream string) (http.Handler, error) {
	parsed, err := url.Parse("http://" + upstream)
	if err != nil || parsed.Host == "" || parsed.Path != "" {
		return nil, errors.New("invalid runtime endpoint")
	}
	proxy := httputil.NewSingleHostReverseProxy(parsed)
	proxy.FlushInterval = -1
	proxy.Transport = &http.Transport{Proxy: nil, DisableKeepAlives: true,
		DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "runtime unavailable", http.StatusServiceUnavailable)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/__ewp/") || r.Method == http.MethodConnect || r.Method == http.MethodTrace {
			http.NotFound(w, r)
			return
		}
		if r.ContentLength > 1<<20 {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			return
		}
		proxy.ServeHTTP(w, r)
	}), nil
}

func run(socket, upstream string) error {
	if err := admitted(socket, upstream); err != nil {
		return err
	}
	if !isolatedNetwork() {
		return errors.New("relay refused host network namespace")
	}
	info, err := os.Lstat(filepath.Dir(socket))
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return errors.New("protected relay directory unavailable")
	}
	if _, err := os.Lstat(socket); !errors.Is(err, os.ErrNotExist) {
		return errors.New("relay socket exists or cannot be checked")
	}
	handler, err := relayHandler(upstream)
	if err != nil {
		return err
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	defer listener.Close()
	if err := os.Chmod(socket, 0600); err != nil {
		return err
	}
	defer os.Remove(socket)
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 3 * time.Second, MaxHeaderBytes: 16 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	ended := make(chan error, 1)
	go func() { ended <- server.Serve(listener) }()
	select {
	case err := <-ended:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
	return nil
}

func main() {
	socket := flag.String("socket", "", "root-only Unix socket")
	upstream := flag.String("upstream", "", "fixed internal sandbox IP:port")
	flag.Parse()
	if err := run(*socket, *upstream); err != nil {
		log.Fatal(fmt.Errorf("runtime relay: %w", err))
	}
}
