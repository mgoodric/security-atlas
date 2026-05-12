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
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/schemaregistry"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
	"github.com/mgoodric/security-atlas/internal/evidence/streambuf"
	"github.com/mgoodric/security-atlas/internal/exception"
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

	// Slice 015: if NATS_URL is set, open a JetStream connection and wire
	// the substrate. The push handler then publishes to the stream and
	// the consumer drains it into the ledger via ingestSvc. Without
	// NATS_URL, the platform runs in dev mode where push goes
	// straight to Service.Process (slice 013's path).
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	var streamConn *streambuf.Conn
	var streamPub *streambuf.JetStreamPublisher
	var streamConsumer *streambuf.Consumer
	if natsURL := os.Getenv("NATS_URL"); natsURL != "" && ingestSvc != nil {
		bootCtx, bootCancel := context.WithTimeout(context.Background(), 10*time.Second)
		conn, err := streambuf.Open(bootCtx, streambuf.Config{
			URL:    natsURL,
			Logger: logger,
		})
		bootCancel()
		if err != nil {
			// Streaming substrate must not silently fail. AC-2 / AC-3
			// require this path when operators have configured NATS.
			fmt.Fprintf(os.Stderr, "atlas: streambuf.Open: %v\n", err)
			os.Exit(1)
		}
		streamConn = conn
		streamPub = streambuf.NewJetStreamPublisher(conn)
		streamConsumer = streambuf.NewConsumer(conn, ingestSvc)
		fmt.Fprintf(os.Stderr, "atlas: NATS JetStream substrate ready (slice 015)\n")
	}

	cfg := api.Config{
		SchemaRegistry:   schemaSvc,
		IngestService:    ingestSvc,
		EvidencePushRate: 100, // 100 records/sec default per EVIDENCE_SDK §4.6
	}
	if streamPub != nil {
		cfg.EvidencePublisher = streamPub
	}
	srv := api.New(cfg)

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

	// Slice 015: drive the consumer alongside the gRPC + HTTP servers.
	// It shares the same stop signal so SIGTERM tears all three down
	// together.
	if streamConsumer != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fmt.Fprintf(os.Stderr, "atlas: NATS consumer draining %s\n", streambufSubject())
			if err := streamConsumer.Start(ctx); err != nil {
				errCh <- fmt.Errorf("streambuf consumer: %w", err)
			}
		}()
	}

	// Slice 021: exception auto-expiry tick loop. Runs as the migrator
	// role (BYPASSRLS) so the sweep can cross tenants -- the per-tenant
	// transaction inside applies the GUC for RLS-honest writes. Default
	// cadence is 24h; ATLAS_EXCEPTION_EXPIRY_INTERVAL overrides for dev
	// loops. Only mounts when the migrator URL is available (the same
	// guard the schema importer uses); otherwise the platform runs
	// without auto-expiry and the operator must sweep manually.
	if migratorURL := os.Getenv("DATABASE_URL"); migratorURL != "" {
		expiryCtx, expiryCancel := context.WithTimeout(context.Background(), 10*time.Second)
		expiryPool, err := pgxpool.New(expiryCtx, migratorURL)
		expiryCancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "atlas: exception expiry pool: %v\n", err)
		} else {
			interval := exception.DefaultExpiryInterval
			if raw := os.Getenv("ATLAS_EXCEPTION_EXPIRY_INTERVAL"); raw != "" {
				if d, perr := time.ParseDuration(raw); perr == nil && d > 0 {
					interval = d
				} else {
					fmt.Fprintf(os.Stderr, "atlas: ATLAS_EXCEPTION_EXPIRY_INTERVAL=%q invalid: %v\n", raw, perr)
				}
			}
			expirer := exception.NewExpirer(expiryPool, logger)
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer expiryPool.Close()
				fmt.Fprintf(os.Stderr, "atlas: exception expirer ticking every %s\n", interval.String())
				if err := expirer.Run(ctx, interval); err != nil {
					errCh <- fmt.Errorf("exception expirer: %w", err)
				}
			}()
		}
	}

	wg.Wait()
	close(errCh)
	if streamConn != nil {
		streamConn.Close()
	}
	for err := range errCh {
		fmt.Fprintf(os.Stderr, "atlas: %v\n", err)
		os.Exit(1)
	}
}

// streambufSubject surfaces the default subject for the boot log line.
func streambufSubject() string { return streambuf.DefaultSubject }

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
