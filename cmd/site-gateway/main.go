package main

import (
	"context"
	"crypto/tls"
	"errors"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"elastic-web-platform/db/migrations"
	"elastic-web-platform/internal/bundle"
	"elastic-web-platform/internal/routing"

	"github.com/jackc/pgx/v5/pgxpool"
)

type config struct {
	address, databaseURL, artifactDir, controlOrigin, tlsCert, tlsKey string
}

func validateConfig(c config) error {
	if c.databaseURL == "" || c.artifactDir == "" || c.tlsCert == "" || c.tlsKey == "" {
		return errors.New("DATABASE_URL, ARTIFACT_STORE_DIR and TLS certificate pair are required")
	}
	if _, _, err := net.SplitHostPort(c.address); err != nil {
		return err
	}
	origin, err := url.Parse(c.controlOrigin)
	if err != nil || origin.Scheme != "https" || origin.Host == "" || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" || origin.User != nil {
		return errors.New("CONTROL_ORIGIN must be an exact HTTPS origin")
	}
	return nil
}

func main() {
	address := os.Getenv("SITE_LISTEN_ADDR")
	if address == "" {
		address = "127.0.0.1:8098"
	}
	c := config{address: address, databaseURL: os.Getenv("DATABASE_URL"), artifactDir: os.Getenv("ARTIFACT_STORE_DIR"), controlOrigin: os.Getenv("CONTROL_ORIGIN"), tlsCert: os.Getenv("TLS_CERT_FILE"), tlsKey: os.Getenv("TLS_KEY_FILE")}
	if err := validateConfig(c); err != nil {
		log.Fatal("invalid site gateway configuration: ", err)
	}
	startup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := pgxpool.New(startup, c.databaseURL)
	if err != nil {
		log.Fatal("database connection: ", err)
	}
	defer db.Close()
	if err := migrations.Apply(startup, db); err != nil {
		log.Fatal("database migration: ", err)
	}
	control := routing.NewDurableRegistry(db, bundle.NewDiskStore(c.artifactDir))
	live := routing.NewLiveGateway(control, routing.NewPreviewSessions(db, c.controlOrigin))
	if err := live.Reload(startup); err != nil {
		log.Fatal("initial route snapshot: ", err)
	}
	server := &http.Server{Addr: c.address, Handler: live, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				check, cancel := context.WithTimeout(ctx, 3*time.Second)
				err := live.Reload(check)
				cancel()
				if err != nil {
					log.Printf("site route snapshot degraded: %v", err)
				}
			}
		}
	}()
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.ListenAndServeTLS(c.tlsCert, c.tlsKey) }()
	select {
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("site gateway: ", err)
		}
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			log.Printf("gateway shutdown: %v", err)
		}
	}
}
