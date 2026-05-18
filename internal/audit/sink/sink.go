// Package sink is the external-write fanout for the slice-126 audit-log
// tamper-evidence story. Every per-domain audit-log INSERT in the platform
// (the nine sites listed in slice 124 + slice 126) calls [Emit] AFTER the
// in-app row commits; this package writes a JSONL line to a maintainer-
// configured file with a per-record HMAC-SHA256 integrity tag.
//
// # Tamper-evidence model
//
// The constitutional principle (slice 126 "Why external matters") is that
// the in-app admin can read + write the in-app audit-log tables — the RLS
// policy split allows tenant-write for admins. The external sink closes
// the loop by ensuring a copy lives in a place the admin cannot touch
// from inside the app:
//
//  1. Cryptographic integrity: each record is HMACed with a per-deployment
//     secret loaded from ATLAS_AUDIT_SINK_HMAC_KEY. A tampered record will
//     fail HMAC verification at the receiver; a forged record cannot exist
//     without knowledge of the key.
//
//  2. UID separation: the operator mounts the sink path as a volume owned
//     by a different UID (e.g. syslog/vector/1003) that atlas can write to
//     but cannot read or unlink. The in-app admin running INSIDE the atlas
//     process cannot reach into another UID's file ownership.
//
// Receiver-side verification is documented in docs/observability.md.
//
// # Non-blocking + at-least-once
//
// [Emit] performs a non-blocking channel send. If the bounded channel
// (default 10000) is full, the entry is written to the audit_sink_failures
// table (a four-policy append-only ledger with the slice 036 RLS pattern)
// AND an ERROR is logged via slog. Records are NEVER silently dropped
// (P0-A1). The in-app INSERT path NEVER blocks on sink emit (P0-A2).
//
// # No-op when unconfigured
//
// When ATLAS_AUDIT_SINK_PATH is unset, [New] returns a no-op Sink that
// accepts Emit calls and discards them. Mirrors slice 121's
// OTEL_EXPORTER_OTLP_ENDPOINT-unset → no-op pattern. The Sink is opt-in
// at deployment time.
//
// # Configuration
//
//	ATLAS_AUDIT_SINK_PATH         (string)  Path to JSONL sink file. Unset = sink disabled.
//	ATLAS_AUDIT_SINK_HMAC_KEY     (string)  Per-deployment HMAC secret. REQUIRED when path is set; min 32 bytes.
//	ATLAS_AUDIT_SINK_BUFFER_SIZE  (int)     Channel buffer capacity. Default 10000.
//
// See slice 126's decisions log (docs/audit-log/126-external-audit-log-sink-decisions.md)
// for the JUDGMENT trail.
package sink

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/audit/unifiedlog"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Env-var names. ATLAS_-namespaced per slice 121 D1's principle (OTEL_ is
// reserved for the OTel SDK's own knobs; atlas-specific opt-ins use ATLAS_).
const (
	EnvPath       = "ATLAS_AUDIT_SINK_PATH"
	EnvHMACKey    = "ATLAS_AUDIT_SINK_HMAC_KEY"
	EnvBufferSize = "ATLAS_AUDIT_SINK_BUFFER_SIZE"
)

// DefaultBufferSize is the channel capacity when ATLAS_AUDIT_SINK_BUFFER_SIZE
// is unset (slice 126 AC-4).
const DefaultBufferSize = 10000

// MinHMACKeyLen is the minimum HMAC key length in bytes. 32 bytes = 256 bits
// = one SHA-256 block. Shorter keys are rejected at boot.
const MinHMACKeyLen = 32

// BatchFlushInterval is the longest the writer goroutine will hold a
// non-empty batch before fsync. Bounds the data-loss-on-crash window.
const BatchFlushInterval = 250 * time.Millisecond

// BatchFlushSize is the batch-record count that triggers an immediate fsync.
const BatchFlushSize = 100

// PoolForTenant is a callback the caller supplies so the sink can write
// fallback rows without coupling to pgxpool. The caller is responsible
// for opening a tx, applying tenancy.ApplyTenant, and returning the tx
// + cleanup func. The sink commits-or-rollbacks via the cleanup.
//
// In production, this is satisfied by a small adapter in cmd/atlas that
// wraps *pgxpool.Pool + tenancy.WithTenant. In tests, the integration
// test supplies a real pgxpool against the test database.
type PoolForTenant func(ctx context.Context, tenantID string) (pgx.Tx, func(context.Context, error), error)

// Sink is the externally-visible fanout. Construct via [New]; call [Emit]
// from each per-domain audit-log INSERT site AFTER the in-app row commits.
// Close via [Shutdown] in the binary's graceful-shutdown path.
type Sink struct {
	enabled bool

	// path + writer are set when enabled. writer is the test seam — in
	// production it wraps an *os.File opened O_APPEND|O_CREATE|O_WRONLY.
	path   string
	writer io.Writer
	closer io.Closer // nil for test in-memory writers; non-nil for *os.File

	hmacKey []byte

	bufferSize int
	ch         chan unifiedlog.Entry

	// poolForTenant powers the fallback ledger write. nil-tolerated:
	// when nil (test setups that don't need the table write), buffer
	// overflows still ERR-log + count, just don't write a row.
	poolForTenant PoolForTenant

	// logger receives the AC-6 / P0-A1 ERROR lines on overflow + write
	// failure. Falls back to slog.Default when nil.
	logger *slog.Logger

	// counters surface via [Stats] for tests + production observability.
	stats Stats

	// writer-goroutine lifecycle.
	wg           sync.WaitGroup
	shutdownCh   chan struct{}
	shutdownOnce sync.Once
}

// Stats are atomic snapshots of the sink's lifetime counters. Safe to call
// concurrently with [Emit].
type Stats struct {
	Emitted     atomic.Int64 // entries successfully written to the sink file
	Dropped     atomic.Int64 // entries rejected by the bounded channel (overflow)
	WriteErrors atomic.Int64 // sink-side write failures (disk full, broken pipe, etc.)
	FailureRows atomic.Int64 // audit_sink_failures rows successfully written
}

// Snapshot returns a value-typed copy of the counters at this moment.
// Suitable for /metrics export and test assertions.
func (s *Stats) Snapshot() (emitted, dropped, writeErrors, failureRows int64) {
	return s.Emitted.Load(), s.Dropped.Load(), s.WriteErrors.Load(), s.FailureRows.Load()
}

// Options is the build-time configuration. Production callers pass an
// empty Options{} (or supply Logger + PoolForTenant) and let New read the
// env-vars. Tests pass WithWriter + an explicit HMAC key to bypass file
// I/O.
type Options struct {
	// Logger receives ERROR lines on overflow + write failure. Falls
	// back to slog.Default when nil.
	Logger *slog.Logger
	// PoolForTenant powers fallback-ledger writes on overflow. nil =
	// "no fallback row write" (acceptable for the disabled sink case +
	// some tests; production callers MUST supply this).
	PoolForTenant PoolForTenant
	// Writer overrides the file destination — test-only seam. When
	// non-nil, [New] uses this instead of opening the env-var path.
	// Production callers leave nil.
	Writer io.Writer
	// HMACKey, if non-nil, overrides the env-var key — test-only. When
	// nil, [New] reads ATLAS_AUDIT_SINK_HMAC_KEY.
	HMACKey []byte
	// BufferSize, if positive, overrides the env-var buffer size — test-only.
	BufferSize int
}

// New constructs a Sink from Options + env-vars. The returned Sink is
// always non-nil:
//
//   - When ATLAS_AUDIT_SINK_PATH is unset AND opts.Writer is nil, the
//     returned Sink is a no-op (Enabled()=false). Emit calls are
//     instantly discarded with no ERROR. This is the production default.
//
//   - When ATLAS_AUDIT_SINK_PATH is set (or opts.Writer is non-nil), the
//     HMAC key MUST be set (either via opts.HMACKey or via
//     ATLAS_AUDIT_SINK_HMAC_KEY) and MUST be at least MinHMACKeyLen bytes.
//     If absent or too short, New returns an error — atlas fail-fasts at
//     boot rather than ship a silent integrity gap.
//
// The writer goroutine begins consuming the channel synchronously inside
// New before returning. Shutdown is the caller's responsibility on
// graceful exit.
func New(opts Options) (*Sink, error) {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	path := strings.TrimSpace(os.Getenv(EnvPath))

	// AC-6: no-op when ATLAS_AUDIT_SINK_PATH is unset AND opts.Writer
	// is nil (the production-default disabled path).
	if path == "" && opts.Writer == nil {
		s := &Sink{
			enabled:    false,
			logger:     logger,
			shutdownCh: make(chan struct{}),
		}
		return s, nil
	}

	// Resolve the HMAC key (D6: REQUIRED when sink is active).
	key := opts.HMACKey
	if len(key) == 0 {
		raw := strings.TrimSpace(os.Getenv(EnvHMACKey))
		if raw == "" {
			return nil, fmt.Errorf("sink: %s must be set when %s is set (no unsigned mode)", EnvHMACKey, EnvPath)
		}
		key = []byte(raw)
	}
	if len(key) < MinHMACKeyLen {
		return nil, fmt.Errorf("sink: HMAC key must be at least %d bytes; got %d", MinHMACKeyLen, len(key))
	}

	bufSize := opts.BufferSize
	if bufSize <= 0 {
		bufSize = DefaultBufferSize
		if raw := strings.TrimSpace(os.Getenv(EnvBufferSize)); raw != "" {
			if v, err := strconv.Atoi(raw); err == nil && v > 0 {
				bufSize = v
			}
		}
	}

	// Resolve the writer + closer. opts.Writer is the test seam; the
	// production path opens an *os.File with O_APPEND|O_CREATE|O_WRONLY.
	var writer io.Writer
	var closer io.Closer
	if opts.Writer != nil {
		writer = opts.Writer
	} else {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, fmt.Errorf("sink: open %s: %w", path, err)
		}
		writer = f
		closer = f
	}

	s := &Sink{
		enabled:       true,
		path:          path,
		writer:        writer,
		closer:        closer,
		hmacKey:       key,
		bufferSize:    bufSize,
		ch:            make(chan unifiedlog.Entry, bufSize),
		poolForTenant: opts.PoolForTenant,
		logger:        logger,
		shutdownCh:    make(chan struct{}),
	}

	s.wg.Add(1)
	go s.run()

	logger.Info("atlas: audit-sink: enabled",
		slog.String("path", path),
		slog.Int("buffer_size", bufSize),
	)
	return s, nil
}

// Enabled reports whether the sink is actively writing. False = no-op
// mode (ATLAS_AUDIT_SINK_PATH unset, no opts.Writer).
func (s *Sink) Enabled() bool { return s.enabled }

// Emit is the one-and-only producer entry point. NON-BLOCKING: returns
// instantly whether the channel accepted the entry or not. On overflow,
// writes to the audit_sink_failures table (via opts.PoolForTenant) and
// ERR-logs. NEVER returns an error to the caller (D8) — handle-on-emit
// errors are not the caller's problem to handle.
//
// Call from each per-domain audit-log INSERT site AFTER the in-app row
// commits. Calling on a no-op (Enabled()=false) sink is a fast discard.
func (s *Sink) Emit(ctx context.Context, entry unifiedlog.Entry) {
	if !s.enabled {
		return
	}
	select {
	case s.ch <- entry:
		// accepted — the writer goroutine will pick it up.
	default:
		// Buffer full. P0-A1: never silent-drop. Log + write the
		// fallback row.
		s.stats.Dropped.Add(1)
		s.logger.ErrorContext(ctx, "atlas: audit-sink: buffer overflow",
			slog.String("kind", string(entry.Kind)),
			slog.String("actor_id", entry.ActorID),
			slog.String("target_type", entry.TargetType),
			slog.String("target_id", entry.TargetID),
			slog.String("action", entry.Action),
			slog.String("tenant_id", entry.TenantID.String()),
			slog.Int("buffer_size", s.bufferSize),
		)
		s.writeFailureRow(ctx, entry, "buffer_overflow", "")
	}
}

// Shutdown drains the channel + closes the writer. Safe to call multiple
// times (idempotent). The drain has a soft timeout via ctx.Deadline.
//
// Production: defer sink.Shutdown(context.Background()) after sink.New().
func (s *Sink) Shutdown(ctx context.Context) error {
	if !s.enabled {
		return nil
	}
	var err error
	s.shutdownOnce.Do(func() {
		close(s.shutdownCh)
		// Wait for the writer goroutine to drain + exit, bounded by ctx.
		done := make(chan struct{})
		go func() {
			s.wg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-ctx.Done():
			err = ctx.Err()
		}
		if s.closer != nil {
			if cerr := s.closer.Close(); cerr != nil && err == nil {
				err = cerr
			}
		}
	})
	return err
}

// run is the writer goroutine body. Consumes the channel + batches writes
// + fsyncs after BatchFlushSize records OR BatchFlushInterval quiet.
func (s *Sink) run() {
	defer s.wg.Done()
	ticker := time.NewTicker(BatchFlushInterval)
	defer ticker.Stop()

	batch := 0
	flush := func() {
		if batch == 0 {
			return
		}
		if f, ok := s.writer.(*os.File); ok {
			if err := f.Sync(); err != nil {
				s.logger.Error("atlas: audit-sink: fsync failed",
					slog.String("err", err.Error()),
				)
			}
		}
		batch = 0
	}

	for {
		select {
		case <-s.shutdownCh:
			// Drain remaining entries up to bufferSize before exit.
			for {
				select {
				case entry := <-s.ch:
					s.writeOne(entry)
					batch++
					if batch >= BatchFlushSize {
						flush()
					}
				default:
					flush()
					return
				}
			}
		case entry := <-s.ch:
			s.writeOne(entry)
			batch++
			if batch >= BatchFlushSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// writeOne serializes one entry to JSONL with HMAC + writes one line.
// On failure, increments WriteErrors + writes a fallback row + ERR-logs.
// Returns no error — the writer goroutine cannot fail the in-app path.
func (s *Sink) writeOne(entry unifiedlog.Entry) {
	line, err := s.canonicalizeAndSign(entry)
	if err != nil {
		// Serialization failure is exceptional (the unifiedlog.Entry
		// shape uses stdlib-marshalable types). If it happens, log
		// + count + fallback.
		s.stats.WriteErrors.Add(1)
		s.logger.Error("atlas: audit-sink: canonicalize failed",
			slog.String("err", err.Error()),
			slog.String("kind", string(entry.Kind)),
		)
		s.writeFailureRow(context.Background(), entry, "write_error", err.Error())
		return
	}
	if _, err := s.writer.Write(line); err != nil {
		s.stats.WriteErrors.Add(1)
		s.logger.Error("atlas: audit-sink: write failed",
			slog.String("err", err.Error()),
			slog.String("kind", string(entry.Kind)),
		)
		s.writeFailureRow(context.Background(), entry, "write_error", err.Error())
		return
	}
	s.stats.Emitted.Add(1)
}

// canonicalizeAndSign produces the JSONL line: the canonical entry shape
// with an additional "_hmac" hex field. Caller appends "\n" semantic via
// the JSON marshal output already.
func (s *Sink) canonicalizeAndSign(entry unifiedlog.Entry) ([]byte, error) {
	// First marshal the entry without _hmac so we have the canonical
	// bytes the receiver will re-hash. encoding/json orders struct
	// fields by source order — deterministic across runs.
	canonical, err := json.Marshal(entry)
	if err != nil {
		return nil, fmt.Errorf("marshal canonical: %w", err)
	}
	mac := hmac.New(sha256.New, s.hmacKey)
	mac.Write(canonical)
	tag := hex.EncodeToString(mac.Sum(nil))

	// The wire form is {<canonical fields...>, "_hmac": "<hex>"}. To
	// emit that without a second marshal of a synthesized struct, we
	// splice: trim the trailing '}' of canonical, append a comma +
	// "_hmac" field + '}', append '\n'.
	if len(canonical) < 2 || canonical[len(canonical)-1] != '}' {
		return nil, errors.New("canonical entry has unexpected shape")
	}
	out := make([]byte, 0, len(canonical)+len(tag)+16)
	out = append(out, canonical[:len(canonical)-1]...)
	out = append(out, `,"_hmac":"`...)
	out = append(out, tag...)
	out = append(out, '"', '}', '\n')
	return out, nil
}

// writeFailureRow inserts one row into audit_sink_failures. Best-effort:
// failure to write the failure row is logged but cannot fail anything
// upstream.
func (s *Sink) writeFailureRow(ctx context.Context, entry unifiedlog.Entry, reason, errText string) {
	if s.poolForTenant == nil {
		// No pool wired — common in tests that exercise only the
		// channel + writer surface. The counter increment still
		// happens elsewhere.
		return
	}
	// Use a fresh non-cancellable context so an in-flight request
	// cancellation does NOT skip the failure-row write. The fallback
	// is the durable signal — losing it on context cancel would
	// re-introduce the silent-drop hole.
	bgCtx := context.WithoutCancel(ctx)
	tenantID := entry.TenantID.String()
	tx, finish, err := s.poolForTenant(bgCtx, tenantID)
	if err != nil {
		s.logger.Error("atlas: audit-sink: open tx for failure row",
			slog.String("err", err.Error()),
			slog.String("tenant_id", tenantID),
		)
		return
	}
	q := dbx.New(tx)
	_, qerr := q.WriteSinkFailure(bgCtx, dbx.WriteSinkFailureParams{
		TenantID:        pgtype.UUID{Bytes: entry.TenantID, Valid: true},
		FailureReason:   reason,
		EntryKind:       string(entry.Kind),
		EntryActor:      entry.ActorID,
		EntryTargetType: entry.TargetType,
		EntryTargetID:   entry.TargetID,
		EntryAction:     entry.Action,
		ErrorText:       errText,
	})
	finish(bgCtx, qerr)
	if qerr != nil {
		s.logger.Error("atlas: audit-sink: write failure row",
			slog.String("err", qerr.Error()),
			slog.String("tenant_id", tenantID),
			slog.String("reason", reason),
		)
		return
	}
	s.stats.FailureRows.Add(1)
}

// StatsSnapshot returns the current counter values. Concurrency-safe.
func (s *Sink) StatsSnapshot() (emitted, dropped, writeErrors, failureRows int64) {
	return s.stats.Snapshot()
}

// PoolFromPgx is a convenience adapter wrapping the common pgxpool +
// tenancy pattern into a PoolForTenant. Production callers in cmd/atlas
// build this once at startup and pass it via Options.PoolForTenant.
//
// The returned PoolForTenant opens a transaction, calls
// tenancy.WithTenant on the context, then tenancy.ApplyTenant on the
// transaction. The finish callback commits on err==nil OR rolls back on
// err != nil.
func PoolFromPgx(pool interface {
	BeginTx(ctx context.Context, txOptions pgx.TxOptions) (pgx.Tx, error)
}) PoolForTenant {
	return func(ctx context.Context, tenantID string) (pgx.Tx, func(context.Context, error), error) {
		tenantCtx, err := tenancy.WithTenant(ctx, tenantID)
		if err != nil {
			return nil, nil, fmt.Errorf("with-tenant: %w", err)
		}
		tx, err := pool.BeginTx(tenantCtx, pgx.TxOptions{})
		if err != nil {
			return nil, nil, fmt.Errorf("begin tx: %w", err)
		}
		if err := tenancy.ApplyTenant(tenantCtx, tx); err != nil {
			_ = tx.Rollback(tenantCtx)
			return nil, nil, fmt.Errorf("apply tenant: %w", err)
		}
		finish := func(ctx context.Context, qerr error) {
			if qerr != nil {
				_ = tx.Rollback(ctx)
				return
			}
			_ = tx.Commit(ctx)
		}
		return tx, finish, nil
	}
}
