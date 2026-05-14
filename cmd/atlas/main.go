// Package main is the security-atlas platform server entrypoint.
//
// Hosts the gRPC server (Evidence + Admin + Connectors) and an HTTP server
// (anchors + frameworks API). Both share the in-memory credentials store;
// the HTTP server additionally needs a Postgres pool to back the catalog
// queries. Both listeners stop on SIGINT/SIGTERM via a common context.
package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	authapi "github.com/mgoodric/security-atlas/internal/api/auth"
	"github.com/mgoodric/security-atlas/internal/api/schemaregistry"
	"github.com/mgoodric/security-atlas/internal/audit/auditor"
	"github.com/mgoodric/security-atlas/internal/auth/apikeystore"
	"github.com/mgoodric/security-atlas/internal/auth/bearer"
	"github.com/mgoodric/security-atlas/internal/auth/oidc"
	"github.com/mgoodric/security-atlas/internal/auth/sessions"
	"github.com/mgoodric/security-atlas/internal/auth/users"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/decision"
	"github.com/mgoodric/security-atlas/internal/eval"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
	"github.com/mgoodric/security-atlas/internal/evidence/streambuf"
	"github.com/mgoodric/security-atlas/internal/exception"
	"github.com/mgoodric/security-atlas/internal/freshnessdrift"
	"github.com/mgoodric/security-atlas/internal/oscal"
	"github.com/mgoodric/security-atlas/internal/risk"
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

	// Slice 034: BEARER_HASH_KEY is mandatory. Without it the server cannot
	// authenticate DB-backed API keys (api_keys.token_hash is HMAC-SHA256
	// keyed with this secret). Refuse-to-boot per docs/adr/0002.
	bearerHashKey, err := bearer.LoadHashKey()
	if err != nil {
		fmt.Fprintf(os.Stderr, "atlas: %v\n", err)
		os.Exit(1)
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

	// Slice 012: the evaluation-stage background job (AC-2). The
	// IngestSubscriber binds a SECOND durable JetStream consumer to the
	// same evidence-ingest stream slice 015 created -- it reacts to each
	// ingested record by re-evaluating the affected control. It is
	// independent of slice 015's ledger-writer consumer (two durable
	// consumers on a Limits-retention stream each get every message). Only
	// wired when NATS + the DB pool are both available.
	var evalSubscriber *eval.IngestSubscriber
	if streamConn != nil && pool != nil {
		evalSubscriber = eval.NewIngestSubscriber(
			streamConn.Stream(),
			streamConn.Cfg().Subject,
			eval.NewEngineFactory(pool),
			logger,
		)
		fmt.Fprintf(os.Stderr, "atlas: evaluation ingest subscriber ready (slice 012)\n")
	}

	// Slice 016: the freshness + drift read-model refresh background job
	// (AC-4). RefreshSubscriber binds a THIRD durable JetStream consumer to
	// the evidence-ingest stream -- it reacts to each ingested record by
	// refreshing the affected tenant's freshness + drift read models. It is
	// independent of slice 015's ledger-writer consumer and slice 012's
	// evaluation consumer (three durable consumers on a Limits-retention
	// stream each get every message). Only wired when NATS + the DB pool are
	// both available.
	var freshnessDriftSubscriber *freshnessdrift.RefreshSubscriber
	if streamConn != nil && pool != nil {
		freshnessDriftSubscriber = freshnessdrift.NewRefreshSubscriber(
			streamConn.Stream(),
			streamConn.Cfg().Subject,
			freshnessdrift.NewRefresherFactory(pool),
			logger,
		)
		fmt.Fprintf(os.Stderr, "atlas: freshness/drift refresh subscriber ready (slice 016)\n")
	}

	// Slice 020: the residual subscriber binds a FOURTH durable JetStream
	// consumer to the same evidence-ingest stream. On every ingested record
	// it recomputes residual risk for every risk linked to the affected
	// control. It re-evaluates each control from the ledger first (the
	// EvaluateControl-first race fix), so a just-ingested record is reflected
	// even before slice 012's own subscriber writes the new evaluation row.
	// Only wired when NATS + the DB pool are both available.
	var residualSubscriber *risk.ResidualSubscriber
	if streamConn != nil && pool != nil {
		residualSubscriber = risk.NewResidualSubscriber(
			streamConn.Stream(),
			streamConn.Cfg().Subject,
			pool,
			logger,
		)
		fmt.Fprintf(os.Stderr, "atlas: risk residual subscriber ready (slice 020)\n")
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

	// Slice 037: when ATLAS_BOOTSTRAP_TOKEN is set, mint a fixed-token
	// admin credential for ATLAS_BOOTSTRAP_TENANT. The docker-compose
	// self-host bundle's one-shot atlas-bootstrap container uses this
	// deterministic token to authenticate control-bundle uploads — it
	// cannot consume the random token the block above prints to stderr.
	// This is a self-host bootstrap convenience; .env.example flags the
	// token as a must-rotate value.
	if bootstrapToken := os.Getenv("ATLAS_BOOTSTRAP_TOKEN"); bootstrapToken != "" {
		bootstrapTenant := os.Getenv("ATLAS_BOOTSTRAP_TENANT")
		if bootstrapTenant == "" {
			fmt.Fprintln(os.Stderr, "atlas: ATLAS_BOOTSTRAP_TOKEN set but ATLAS_BOOTSTRAP_TENANT empty — skipping fixed-token credential")
		} else {
			cred, err := srv.IssueBootstrapFixedAdminCredential(bootstrapTenant, bootstrapToken)
			if err != nil {
				fmt.Fprintf(os.Stderr, "atlas: bootstrap fixed-token issue: %v\n", err)
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "atlas: fixed-token admin credential issued: id=%s tenant=%s last4=%s\n",
				cred.ID, cred.TenantID, cred.Last4)
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if pool != nil {
		defer pool.Close()
		srv.AttachDB(pool)
		fmt.Fprintf(os.Stderr, "atlas: pgx pool ready\n")

		// Slice 034: wire the DB-backed apikey store. The auth pool
		// (DATABASE_URL = BYPASSRLS migrate role) is used for the
		// lookup-by-hash on the auth hot path; the app pool (RLS-enforced)
		// is used for issue/list/rotate/revoke under a tenant context.
		hasher, hErr := bearer.NewHasher(bearerHashKey)
		if hErr != nil {
			fmt.Fprintf(os.Stderr, "atlas: bearer.NewHasher: %v\n", hErr)
			os.Exit(1)
		}
		var authPool *pgxpool.Pool
		if migURL := os.Getenv("DATABASE_URL"); migURL != "" {
			apCtx, apCancel := context.WithTimeout(ctx, 10*time.Second)
			ap, err := pgxpool.New(apCtx, migURL)
			apCancel()
			if err != nil {
				fmt.Fprintf(os.Stderr, "atlas: api-key auth pool: %v\n", err)
			} else {
				authPool = ap
				defer ap.Close()
			}
		}
		apikeySvc := apikeystore.NewStore(pool, authPool, hasher, 0)
		srv.AttachAPIKeyStore(apikeySvc)
		fmt.Fprintf(os.Stderr, "atlas: api_keys store wired (BEARER_HASH_KEY ok)\n")

		// Slice 037: wire the user-facing auth routes so /auth/local/login
		// mounts. The docker-compose self-host bundle is a local-mode
		// deployment — a default local user signs in with email+password,
		// no external IdP. The OIDC authenticator is still constructed
		// (the handler requires a non-nil *oidc.Authenticator) but its
		// resolver always reports "unknown IdP": OIDC is an opt-in
		// post-install configuration step, not part of first sign-in.
		// secureCookies follows ATLAS_SECURE_COOKIES (default false for
		// the local-HTTP self-host default; operators set it true behind
		// TLS).
		userStore := users.NewStore(pool)
		sessionStore := sessions.NewStore(pool, 0)
		oidcAuth := oidc.New(localModeIdpResolver{})
		secureCookies := os.Getenv("ATLAS_SECURE_COOKIES") == "true"
		srv.AttachAuthHandler(authapi.New(oidcAuth, userStore, sessionStore, secureCookies))
		fmt.Fprintf(os.Stderr, "atlas: auth handler wired (/auth/local/login mounted, secure_cookies=%t)\n", secureCookies)

		// Slice 035: construct the OPA engine + decision audit writer
		// once at startup. The engine loads the embedded rego bundle;
		// failure here is fatal because every API path needs the engine
		// to run. The resolver reads user_roles via the app pool (RLS
		// enforced through tenancy context).
		azEngineCtx, azCancel := context.WithTimeout(ctx, 10*time.Second)
		azEngine, err := authz.NewEngine(azEngineCtx, authz.NewDBRolesResolver(pool))
		azCancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "atlas: authz engine: %v\n", err)
			os.Exit(1)
		}
		// Slice 025: hydrate auditor ABAC attrs (audit_period_ids)
		// from auditor_assignments on every auditor-role request.
		azEngine = azEngine.WithAttrsResolver(auditor.NewDBAttrsResolver(pool))
		azAudit := authz.NewAuditWriter(pool)
		srv.AttachAuthz(azEngine, azAudit)
		fmt.Fprintf(os.Stderr, "atlas: authz OPA engine wired (5 roles + decision audit + auditor attrs)\n")

		// Slice 030: wire the OSCAL export pipeline when the Python
		// oscal-bridge sidecar is configured. OSCAL_BRIDGE_ADDR is the
		// gRPC address of the bridge (e.g. 127.0.0.1:50070). When unset,
		// the export route is simply absent — the bridge is an optional
		// sidecar, not a hard dependency of the platform binary. The
		// signing key comes from OSCAL_SIGNING_KEY (hex-encoded ed25519
		// private key); absent that, a per-process ephemeral key is used
		// (the public key still travels in every bundle manifest, so the
		// signature stays verifiable — see the slice-030 decisions log).
		if bridgeAddr := os.Getenv("OSCAL_BRIDGE_ADDR"); bridgeAddr != "" {
			bridge, bErr := oscal.DialBridge(bridgeAddr)
			if bErr != nil {
				fmt.Fprintf(os.Stderr, "atlas: oscal-bridge dial (%s): %v — OSCAL export disabled\n", bridgeAddr, bErr)
			} else {
				signer, sErr := oscalSignerFromEnv()
				if sErr != nil {
					fmt.Fprintf(os.Stderr, "atlas: oscal signer: %v — OSCAL export disabled\n", sErr)
					_ = bridge.Close()
				} else {
					srv.AttachOscalExporter(oscal.NewExporter(pool, bridge, signer))
					fmt.Fprintf(os.Stderr, "atlas: OSCAL export pipeline wired (bridge=%s)\n", bridgeAddr)
				}
			}
		}

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

	// Slice 012: drive the evaluation ingest subscriber alongside the
	// other consumers. Shares the same stop signal so SIGTERM tears it
	// down with everything else (AC-2).
	if evalSubscriber != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fmt.Fprintf(os.Stderr, "atlas: evaluation ingest subscriber starting\n")
			if err := evalSubscriber.Start(ctx); err != nil {
				errCh <- fmt.Errorf("eval ingest subscriber: %w", err)
			}
		}()
	}

	// Slice 016: drive the freshness/drift refresh subscriber alongside the
	// other consumers. Shares the same stop signal so SIGTERM tears it down
	// with everything else (AC-4: refresh on every ledger write).
	if freshnessDriftSubscriber != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fmt.Fprintf(os.Stderr, "atlas: freshness/drift refresh subscriber starting\n")
			if err := freshnessDriftSubscriber.Start(ctx); err != nil {
				errCh <- fmt.Errorf("freshness/drift refresh subscriber: %w", err)
			}
		}()
	}

	// Slice 020: drive the risk residual subscriber alongside the other
	// consumers. Shares the same stop signal so SIGTERM tears it down with
	// everything else (AC-5).
	if residualSubscriber != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fmt.Fprintf(os.Stderr, "atlas: risk residual subscriber starting\n")
			if err := residualSubscriber.Start(ctx); err != nil {
				errCh <- fmt.Errorf("risk residual subscriber: %w", err)
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

	// Slice 055: Decision Log overdue-notification tick loop (AC-6). Runs as
	// the migrator role (BYPASSRLS) so the sweep can cross tenants -- the
	// per-tenant transaction inside applies the GUC for RLS-honest writes.
	// Each tick emits one in-app notification per not-yet-notified overdue
	// decision to its decision_maker, paired with one `overdue_notified`
	// row in decisions_audit (the authoritative dedup marker -- P0
	// anti-criterion: never repeated). Default cadence is 24h;
	// ATLAS_DECISION_OVERDUE_INTERVAL overrides for dev loops. Only mounts
	// when the migrator URL is available (the same guard the exception
	// expirer uses).
	if migratorURL := os.Getenv("DATABASE_URL"); migratorURL != "" {
		overdueCtx, overdueCancel := context.WithTimeout(context.Background(), 10*time.Second)
		overduePool, err := pgxpool.New(overdueCtx, migratorURL)
		overdueCancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "atlas: decision overdue pool: %v\n", err)
		} else {
			interval := decision.DefaultOverdueInterval
			if raw := os.Getenv("ATLAS_DECISION_OVERDUE_INTERVAL"); raw != "" {
				if d, perr := time.ParseDuration(raw); perr == nil && d > 0 {
					interval = d
				} else {
					fmt.Fprintf(os.Stderr, "atlas: ATLAS_DECISION_OVERDUE_INTERVAL=%q invalid: %v\n", raw, perr)
				}
			}
			notifier := decision.NewNotifier(overduePool, logger)
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer overduePool.Close()
				fmt.Fprintf(os.Stderr, "atlas: decision overdue notifier ticking every %s\n", interval.String())
				if err := notifier.Run(ctx, interval); err != nil {
					errCh <- fmt.Errorf("decision overdue notifier: %w", err)
				}
			}()
		}
	}

	// Slice 012: time-based control-state recompute (AC-2, second half).
	// Freshness decays with wall-clock -- a control `fresh` yesterday is
	// `stale` today even with no new evidence -- so the engine must
	// re-evaluate on a schedule independent of ingest. Runs as the migrator
	// role (BYPASSRLS) to enumerate tenants; each tenant's evaluation runs
	// through an app-role Engine for RLS-honest writes. Only mounts when
	// the migrator URL + the app pool are both available. Cadence defaults
	// to hourly; ATLAS_EVAL_RECOMPUTE_INTERVAL overrides for dev loops.
	if migratorURL := os.Getenv("DATABASE_URL"); migratorURL != "" && pool != nil {
		schedCtx, schedCancel := context.WithTimeout(context.Background(), 10*time.Second)
		schedPool, err := pgxpool.New(schedCtx, migratorURL)
		schedCancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "atlas: eval scheduler pool: %v\n", err)
		} else {
			interval := eval.DefaultRecomputeInterval
			if raw := os.Getenv("ATLAS_EVAL_RECOMPUTE_INTERVAL"); raw != "" {
				if d, perr := time.ParseDuration(raw); perr == nil && d > 0 {
					interval = d
				} else {
					fmt.Fprintf(os.Stderr, "atlas: ATLAS_EVAL_RECOMPUTE_INTERVAL=%q invalid: %v\n", raw, perr)
				}
			}
			scheduler := eval.NewScheduler(schedPool, eval.NewEngineFactory(pool), logger)
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer schedPool.Close()
				fmt.Fprintf(os.Stderr, "atlas: eval scheduler ticking every %s\n", interval.String())
				if err := scheduler.Run(ctx, interval); err != nil {
					errCh <- fmt.Errorf("eval scheduler: %w", err)
				}
			}()
		}
	}

	// Slice 016: daily 00:00 UTC freshness + drift read-model recompute
	// (AC-4, first half). Freshness decays with wall-clock and drift is a
	// day-over-day delta -- both need a guaranteed daily recompute even when
	// no new evidence arrives. Runs as the migrator role (BYPASSRLS) to
	// enumerate tenants; each tenant's refresh runs through app-role Stores
	// for RLS-honest writes. The scheduler wakes hourly and fires the sweep
	// the first time it observes a new UTC calendar day. Only mounts when
	// the migrator URL + the app pool are both available.
	if migratorURL := os.Getenv("DATABASE_URL"); migratorURL != "" && pool != nil {
		fdCtx, fdCancel := context.WithTimeout(context.Background(), 10*time.Second)
		fdPool, err := pgxpool.New(fdCtx, migratorURL)
		fdCancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "atlas: freshness/drift scheduler pool: %v\n", err)
		} else {
			tickCheck := freshnessdrift.DefaultDailyTickCheck
			if raw := os.Getenv("ATLAS_FRESHNESS_DRIFT_TICK_CHECK"); raw != "" {
				if d, perr := time.ParseDuration(raw); perr == nil && d > 0 {
					tickCheck = d
				} else {
					fmt.Fprintf(os.Stderr, "atlas: ATLAS_FRESHNESS_DRIFT_TICK_CHECK=%q invalid: %v\n", raw, perr)
				}
			}
			fdScheduler := freshnessdrift.NewScheduler(fdPool, freshnessdrift.NewRefresherFactory(pool), logger)
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer fdPool.Close()
				fmt.Fprintf(os.Stderr, "atlas: freshness/drift scheduler checking every %s\n", tickCheck.String())
				if err := fdScheduler.Run(ctx, tickCheck); err != nil {
					errCh <- fmt.Errorf("freshness/drift scheduler: %w", err)
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

// oscalSignerFromEnv builds the slice-030 export-bundle signer. When
// OSCAL_SIGNING_KEY is set (hex-encoded 64-byte ed25519 private key) it
// is loaded; otherwise a fresh per-process ephemeral keypair is
// generated. The ephemeral path is acceptable because the public key
// travels in every bundle manifest — the signature stays verifiable;
// it just is not anchored to a long-lived identity (the cosign-keyless
// upgrade is the v3 revisit item in the slice-030 decisions log).
func oscalSignerFromEnv() (*oscal.Signer, error) {
	raw := os.Getenv("OSCAL_SIGNING_KEY")
	if raw == "" {
		return oscal.NewEphemeralSigner()
	}
	key, err := hex.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("OSCAL_SIGNING_KEY is not valid hex: %w", err)
	}
	return oscal.NewSigner(ed25519.PrivateKey(key))
}

// localModeIdpResolver is the slice-037 no-op IdP resolver for local-mode
// self-host deployments. The docker-compose bundle ships local-mode
// authentication (email + password against a seeded default user); OIDC
// is an opt-in post-install configuration step. Until an operator
// configures an IdP, every OIDC resolution reports "unknown IdP" and the
// /auth/oidc/* routes 400 cleanly — /auth/local/login is the working
// sign-in path.
type localModeIdpResolver struct{}

func (localModeIdpResolver) ResolveIdp(_ context.Context, _ uuid.UUID, _ string) (oidc.IdpConfig, error) {
	return oidc.IdpConfig{}, oidc.ErrUnknownIdp
}
