package main

import (
	"context"
	"crypto/tls"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/s1ckdark/betterdock-widgets/server/internal/market"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	tlsConfig, err := loadTLSConfig(os.Getenv("TLS_CERT_FILE"), os.Getenv("TLS_KEY_FILE"))
	if err != nil {
		log.Fatal("TLS certificate configuration is invalid")
	}
	catalog, err := market.LoadCatalog(env("CATALOG_PATH", "/catalog/index.json"), env("CATALOG_SIGNATURE_PATH", "/catalog/index.json.sig"))
	if err != nil {
		log.Fatal("signed catalog could not be loaded")
	}
	proxies, err := market.ParseTrustedProxies(os.Getenv("TRUSTED_PROXY_CIDRS"))
	if err != nil {
		log.Fatal("TRUSTED_PROXY_CIDRS is invalid")
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is required")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		log.Fatal("DATABASE_URL is invalid")
	}
	config.MaxConns = 10
	config.MinConns = 0
	config.MaxConnLifetime = 30 * time.Minute
	config.ConnConfig.ConnectTimeout = 5 * time.Second
	config.ConnConfig.RuntimeParams["statement_timeout"] = "5000"
	config.ConnConfig.RuntimeParams["application_name"] = "betterdock-market"
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		log.Fatal("database pool could not be created")
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = pool.Ping(ctx)
	cancel()
	if err != nil {
		log.Fatal("database is unavailable")
	}
	store := market.PostgresStore{Pool: pool}
	catalogCtx, catalogCancel := context.WithTimeout(context.Background(), 30*time.Second)
	err = store.RecordCatalog(catalogCtx, catalog)
	catalogCancel()
	if err != nil {
		log.Fatal("verified catalog history could not be recorded")
	}
	api := market.NewAPI(store, catalog, proxies)
	server := &http.Server{TLSConfig: tlsConfig, Addr: env("LISTEN_ADDR", ":8080"), Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10, ErrorLog: log.New(io.Discard, "", 0)}
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-shutdown
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()
	log.Printf("market API ready; catalog sequence=%d widgets=%d", catalog.Sequence, len(catalog.Versions))
	if tlsConfig != nil {
		// The key pair was loaded before database access. Empty paths reuse the
		// in-memory certificate and never silently fall back to plaintext HTTP.
		err = server.ListenAndServeTLS("", "")
	} else {
		err = server.ListenAndServe()
	}
	if err != nil && err != http.ErrServerClosed {
		log.Fatal("HTTP server failed")
	}
}
func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// Native HTTPS is optional for loopback test deployments, but partial or
// unreadable TLS configuration is an error rather than a plaintext fallback.
func loadTLSConfig(certFile, keyFile string) (*tls.Config, error) {
	if certFile == "" && keyFile == "" {
		return nil, nil
	}
	if certFile == "" || keyFile == "" {
		return nil, errors.New("both TLS files are required")
	}
	certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, errors.New("TLS key pair could not be loaded")
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}}, nil
}
