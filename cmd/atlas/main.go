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
	"github.com/mgoodric/security-atlas/internal/api/schemaregistry"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
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

	// The schema registry needs a pool to back its DB store. Construct
	// the pool first; if it succeeds, build the DB-backed registry and
	// hand it to api.New. If no DATABASE_URL is set, fall back to the
	// in-memory registry (slice-003 surface only — no HTTP endpoints).
	ctxBoot, cancelBoot := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelBoot()

	var schemaSvc *schemaregistry.Service
	var pool *pgxpool.Pool
	if dbURL != "" {
		p, err := pgxpool.New(ctxBoot, dbURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "atlas: pgxpool.New: %v\n", err)
			os.Exit(1)
		}
		pool = p
		schemaSvc = schemaregistry.NewService(pool)
	}

	// Slice 013: wire the DB-backed ingestion stage when both the
	// schema registry and the pool are available. Without them the
	// server runs in the slice-003 in-memory fallback mode (gRPC only,
	// no ledger writes).
	var ingestSvc *ingest.Service
	if pool != nil && schemaSvc != nil {
		ingestSvc = ingest.New(pool, schemaSvc)
	}

	srv := api.New(api.Config{
		SchemaRegistry:   schemaSvc,
		IngestService:    ingestSvc,
		EvidencePushRate: 100, // 100 records/sec default per EVIDENCE_SDK §4.6
	})

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

	if pool != nil {
		defer pool.Close()
		srv.AttachDB(pool)
		fmt.Fprintf(os.Stderr, "atlas: pgx pool ready\n")

		// Import bundled platform schemas at boot. Idempotent — no-op when
		// every kind is already present in the DB. Requires the connection
		// to have BYPASSRLS (atlas_migrate) because global rows have
		// tenant_id NULL.
		if importerURL := os.Getenv("DATABASE_URL"); importerURL != "" && schemaSvc != nil {
			impCtx, impCancel := context.WithTimeout(ctx, 30*time.Second)
			impPool, err := pgxpool.New(impCtx, importerURL)
			if err != nil {
				fmt.Fprintf(os.Stderr, "atlas: schema import pool: %v\n", err)
			} else {
				impSvc := schemaregistry.NewService(impPool)
				ins, tot, err := impSvc.ImportPlatformSchemas(impCtx, schemaregistry.PlatformSchemasFS())
				if err != nil {
					fmt.Fprintf(os.Stderr, "atlas: schema import: %v\n", err)
				} else {
					fmt.Fprintf(os.Stderr, "atlas: schema import inserted=%d total=%d\n", ins, tot)
				}
				impPool.Close()
			}
			impCancel()
			// Refresh the app-pool-backed cache so HTTP/gRPC see the new rows.
			loadCtx, loadCancel := context.WithTimeout(ctx, 10*time.Second)
			if err := schemaSvc.LoadFromDB(loadCtx); err != nil {
				fmt.Fprintf(os.Stderr, "atlas: schema cache reload: %v\n", err)
			}
			loadCancel()
		}
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
