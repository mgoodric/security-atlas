// Package main is the security-atlas platform server entrypoint.
//
// Hosts the gRPC server (Evidence + Admin + Connectors) and an HTTP server
// (anchors + frameworks API). Both share the in-memory credentials store;
// the HTTP server additionally needs a Postgres pool to back the catalog
// queries. Both listeners stop on SIGINT/SIGTERM via a common context.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
)

const (
	defaultGRPCAddr = ":50051"
	defaultHTTPAddr = ":8080"
)

func main() {
	// Honor --version / -v before any other startup work. Keeps the server
	// quiet when invoked by a package manager probing for the version (e.g.
	// `apt-get --version` style smoke tests, install-script self-tests).
	for _, a := range os.Args[1:] {
		if a == "--version" || a == "-v" || a == "version" {
			fmt.Println(versionInfo())
			return
		}
	}

	grpcAddr := envOr("ATLAS_GRPC_ADDR", defaultGRPCAddr)
	httpAddr := envOr("ATLAS_HTTP_ADDR", defaultHTTPAddr)
	dbURL := os.Getenv("DATABASE_URL_APP")
	if dbURL == "" {
		dbURL = os.Getenv("DATABASE_URL")
	}

	srv := api.New(api.Config{})

	if bootstrapTenant := os.Getenv("ATLAS_BOOTSTRAP_TENANT"); bootstrapTenant != "" {
		cred, bearer, err := srv.IssueBootstrapCredential(bootstrapTenant)
		if err != nil {
			fmt.Fprintf(os.Stderr, "atlas: bootstrap issue: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "atlas: bootstrap credential issued: id=%s tenant=%s bearer=%s\n",
			cred.ID, cred.TenantID, bearer)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	var pool *pgxpool.Pool
	if dbURL != "" {
		dialCtx, dialCancel := context.WithTimeout(ctx, 10*time.Second)
		defer dialCancel()
		p, err := pgxpool.New(dialCtx, dbURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "atlas: pgxpool.New: %v\n", err)
			os.Exit(1)
		}
		defer p.Close()
		pool = p
		srv.AttachDB(pool)
		fmt.Fprintf(os.Stderr, "atlas: pgx pool ready\n")
	} else {
		fmt.Fprintln(os.Stderr, "atlas: DATABASE_URL_APP not set — HTTP server will refuse to start")
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 2)

	wg.Add(1)
	go func() {
		defer wg.Done()
		fmt.Fprintf(os.Stderr, "atlas: gRPC listening on %s\n", grpcAddr)
		if err := srv.Run(ctx, grpcAddr); err != nil {
			errCh <- fmt.Errorf("grpc: %w", err)
		}
	}()

	if pool != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fmt.Fprintf(os.Stderr, "atlas: HTTP listening on %s\n", httpAddr)
			if err := srv.RunHTTP(ctx, httpAddr); err != nil {
				errCh <- fmt.Errorf("http: %w", err)
			}
		}()
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		fmt.Fprintf(os.Stderr, "atlas: %v\n", err)
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
