# Slice 126 — Decisions log

Slice type: JUDGMENT. Per the slice-development workflow (CLAUDE.md "JUDGMENT slices"), this file records the design calls Claude made at build time. The maintainer iterates post-deployment.

## D1 — Sink mechanism: (a) JSONL-to-disk with HMAC-SHA256

**Decision.** Pick option (a) JSONL-to-disk. Each audit-log write fans out to one JSONL line appended to a maintainer-configured file path (default `/var/log/security-atlas/audit-log.jsonl`). Per-record integrity proof is a stdlib HMAC-SHA256 over the canonical Entry payload, keyed by a per-deployment secret loaded from `ATLAS_AUDIT_SINK_HMAC_KEY`. The HMAC tag is appended to each line as a `_hmac` field. Receiver-side verification re-computes the HMAC over the same canonical bytes and compares constant-time.

**Why (override of the maintainer's lean).** Maintainer's filed lean was option (c) OTel logs via Collector. I picked (a) instead. The deciding factors:

1. **OTel logs SDK is pre-1.0 alpha.** `go.opentelemetry.io/otel/log` is at `v0.19.0` and `go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc` is at `v0.19.0` as of this build (2026-05-18). Slice 121's D1 already recorded the pain of version-wrangling the stable OTel `v1.x` line against `otelpgx@v0.10.0`. Adding two more pre-1.0 packages doubles the version-bump-driven churn risk for the lifetime of this code path.
2. **Tamper-evidence is independent of transport.** The "evidence" is the cryptographic chain — HMAC over each record bound to a per-deployment key, signed externally. JSONL with HMAC gives the same cryptographic guarantee as OTel logs with HMAC. The OTel transport adds no integrity property the HMAC doesn't already provide.
3. **"External" property is achieved via filesystem UID ownership.** The constitutional principle is "out-of-app." With option (a), the operator mounts a volume into the atlas container owned by a different UID (`syslog` / `vector` / `1003`) that atlas can write but cannot read or unlink. logrotate / vector / fluentbit / promtail running as that UID drains the file to wherever the operator wants (Loki, S3, Splunk, file storage). The in-app admin cannot reach into another UID's file system from inside the atlas process.
4. **Zero new Go dependencies.** Stdlib `crypto/hmac` + `crypto/sha256` + `encoding/json` + `os` is all that's needed. The dependency graph stays small, the supply-chain attack surface doesn't grow, and slice 121's already-painful go.mod resolution is unaffected.
5. **Composes with every operator stack.** A JSONL file is the lingua franca of log shipping. Operators who DO want OTel routing can run vector or fluentbit with an OTel sink — but they're not forced into it. Operators who want Splunk's HEC, Datadog's agent, or just S3 archival get there with a one-line vector config.
6. **Predictable backpressure.** A blocking `os.File.Write` to a local disk has a known latency floor (microseconds to milliseconds) and a known failure mode (disk full → write error). gRPC to an OTel Collector adds an unbounded latency tail (network, Collector queue depth, downstream backpressure) that compounds the buffered-channel overflow risk AC-4 guards against.

**Why NOT each alternative (per AC-1, all four options documented even for the unchosen ones):**

| Option                            | Pros                                                                                                        | Cons                                                                                                                                                              | Verdict                                                                                                                |
| --------------------------------- | ----------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------- |
| **(a) JSONL to disk**             | Simplest; zero new deps; readable; HMAC integrity; composes with all log shippers; predictable backpressure | Operator must rotate (logrotate is standard ops); UID-based separation is the "external" property (relies on container/host hygiene, not crypto-only)             | **CHOSEN.** Cryptographic integrity via HMAC + operator-controlled UID separation gives equivalent tamper-evidence.    |
| (b) Syslog UDP/TCP                | Common ops choice; zero new infra in atlas                                                                  | UDP can silently drop (P0-A1 hostile); TCP introduces a synchronous network blocker on the write path; rsyslog config burden on operator                          | Rejected. UDP-drop conflicts with P0-A1.                                                                               |
| (c) OTel logs → Collector         | Composes with slice 121; uniform with traces+metrics; tenant_id as resource attribute                       | Requires two pre-1.0 dependencies (`otel/log@v0.19.0` + `otlploggrpc@v0.19.0`); adds Collector as an operational dependency; doubles slice 121's version-bump tax | Rejected on dependency-maturity grounds. Operators who want this can still get it by running vector with an OTel sink. |
| (d) S3-compatible cosigned object | Most paranoid cryptographic chain; reuses slice-030 cosign infra                                            | Per-write cosign latency is incompatible with non-blocking AC-3; cost-per-write scaling problem; complex setup                                                    | Rejected on latency + cost grounds. Could be a future v3 option for high-stakes deployments.                           |

**Spillover risk.** If a future operator demands native OTel logs routing without running vector as a shim, file a follow-on slice that adds an opt-in OTel exporter alongside the JSONL writer (the `sink.Emit` API is mechanism-agnostic by design — Entry → sink — so a future mechanism plugs in behind the same surface). The existing mechanism stays the default.

**Receiver-side verification.** Documented in `docs/observability.md` (AC-10). The procedure:

1. Read the JSONL file line-by-line.
2. For each line, parse the JSON object; extract `_hmac` and the remaining fields.
3. Re-serialize the remaining fields with `json.Marshal` (RFC 8259 canonicalization — `encoding/json` sorts struct fields by source order, which is deterministic across runs).
4. Compute `hmac.New(sha256.New, key).Write(canonicalized).Sum(nil)`.
5. Compare with `hmac.Equal` (constant-time).

The HMAC is bound to the deployment key. A tampered line whose `_hmac` doesn't verify is flagged. A line with a forged `_hmac` cannot exist without knowledge of the key — which is sealed in the operator's secret store, never written to the JSONL file, never logged on startup.

## D2 — Canonical Entry shape: re-export from `unifiedlog`

**Decision.** The sink package does NOT define its own Entry struct. It imports and re-uses `internal/audit/unifiedlog.Entry` (slice 124's canonical shape). The package boundary is type-safe: `sink.Emit(ctx context.Context, entry unifiedlog.Entry) error`.

**Why.** The slice text mandates "the same canonical shape as slice 124's `unifiedlog.Entry`" (AC-2). Duplicating the struct would be a maintenance hazard — a future field addition would have to be applied in two places, and any drift between the two shapes would silently produce two divergent audit-log surfaces. Re-using the unifiedlog Entry guarantees the sink's record format and the in-app aggregator's record format stay byte-for-byte identical forever (or at least, both broken together if they ever drift).

The dependency direction (sink → unifiedlog) is correct: unifiedlog is the OLDER, READ-only package and has no awareness of the writer; sink is the NEW, WRITE-side fanout and depends on unifiedlog's shape. unifiedlog imports nothing from sink. No cycle.

## D3 — Buffered channel size 10000 + backpressure semantics

**Decision.** The sink runs one writer goroutine consuming from a `chan unifiedlog.Entry` of capacity 10000 (the slice's AC-4 default). Producers (the 9 INSERT call sites) call `sink.Emit(ctx, entry)` which performs a non-blocking `select { case ch <- entry: ... default: ... }`. The default branch ERRORs to slog AND writes one row to the new `audit_sink_failures` table via `WriteSinkFailure`. No record is ever silently dropped (P0-A1).

**Why 10000.** The slice doc's AC-4 specifies `10000` as the default. The math: 10000 records × ~512 bytes average JSON serialization = ~5 MiB of in-flight buffer. That's a safe burst absorber. The atlas process budget allows for this; the buffer is the only meaningful memory cost.

**Why a writer goroutine, not direct sync write.** AC-3 / P0-A2 demand the in-app INSERT path NOT block on sink emit. A goroutine consuming from a buffered channel is the canonical Go idiom for this. The producer side stays bounded-O(1) (channel send) regardless of disk-write latency.

**Why ERR + table-row on overflow, not drop or panic.** P0-A1 forbids silent-drop. Panic is hostile to the in-app path (the slice cannot break the in-app write per P0-A2). ERR + table-row gives ops a visible signal (log) AND a queryable durable record (the new `audit_sink_failures` table) for postmortem investigation. The table is itself an audit-log — append-only with the slice 036 four-policy RLS pattern — so an operator can't simply mask the gap by deleting the row.

## D4 — Fallback table: `audit_sink_failures` with four-policy append-only RLS

**Decision.** New migration `20260518000000_audit_sink_failures.sql` creates one table:

```sql
CREATE TABLE audit_sink_failures (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID NOT NULL,
    occurred_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    failure_reason  TEXT NOT NULL CHECK (failure_reason IN ('buffer_overflow', 'write_error')),
    entry_kind   TEXT NOT NULL,
    entry_actor  TEXT NOT NULL,
    entry_target_type TEXT NOT NULL,
    entry_target_id   TEXT NOT NULL,
    entry_action TEXT NOT NULL,
    error_text   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_audit_sink_failures_tenant_occurred
    ON audit_sink_failures (tenant_id, occurred_at DESC);
ALTER TABLE audit_sink_failures ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_sink_failures FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON audit_sink_failures
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON audit_sink_failures
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
GRANT SELECT, INSERT ON audit_sink_failures TO atlas_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON audit_sink_failures TO atlas_migrate;
```

**Why this column set.** We deliberately do NOT mirror the full `Entry.PayloadJSON` here. The PayloadJSON could itself be very large (e.g., evidence-audit payloads). The fallback's job is to FLAG the gap and let ops investigate; it isn't a parallel ledger. The minimum identifying tuple (kind / actor / target_type / target_id / action / occurred_at) is enough to correlate against the in-app row, and `failure_reason` + `error_text` capture the OPS context.

**Why four-policy append-only.** Matches slice 036 / slice 108 / slice 062 precedent: `tenant_read FOR SELECT` + `tenant_write FOR INSERT WITH CHECK` under `FORCE ROW LEVEL SECURITY`, with NO update/delete policy. The grant list mirrors `me_audit_log` exactly — `atlas_app` has SELECT + INSERT only (defense-in-depth even if a future RLS policy is accidentally added).

## D5 — Config env-var naming: `ATLAS_AUDIT_SINK_*` (NOT `OTEL_AUDIT_SINK_*`)

**Decision.** Env vars are `ATLAS_AUDIT_SINK_PATH`, `ATLAS_AUDIT_SINK_HMAC_KEY`, `ATLAS_AUDIT_SINK_BUFFER_SIZE` (optional, default 10000). The slice text suggested `OTEL_AUDIT_SINK_*` but that was tied to option (c). Since we picked option (a), the OTel namespace would be misleading — there's no OTel pathway involved.

**Why `ATLAS_` and not `OTEL_`.** Slice 121's D1 documented the principle: OTel-namespaced env vars are reserved for the OTel SDK's own knobs. atlas-specific opt-in toggles use `ATLAS_*` (slice 121's `ATLAS_METRICS_FALLBACK_ENABLE` precedent). Reusing `OTEL_*` for a non-OTel sink would burn that namespace and confuse operators.

**Opt-in default.** When `ATLAS_AUDIT_SINK_PATH` is unset, `sink.Emit` is a no-op (the writer goroutine never starts). Matches slice 121's `OTEL_EXPORTER_OTLP_ENDPOINT`-unset → no-op pattern.

## D6 — HMAC key requirement: REQUIRED when path is set (fail-fast)

**Decision.** If `ATLAS_AUDIT_SINK_PATH` is set but `ATLAS_AUDIT_SINK_HMAC_KEY` is unset (or empty), `sink.New()` returns an error. The atlas binary fail-fasts at boot. There is no "unsigned mode."

**Why fail-fast.** An unsigned audit-log sink is hostile to the purpose of the slice — the operator believes they have tamper-evidence and they don't. Better to refuse boot than to ship a silent integrity gap. The cost is one extra env-var in the operator setup, which is documented in `docs/observability.md`.

The key must be at least 32 bytes (256 bits — the SHA-256 block size). Shorter keys are rejected at boot.

## D7 — File write: append-only with O_APPEND + Sync after each batch

**Decision.** The writer goroutine opens the path with `os.O_APPEND|os.O_CREATE|os.O_WRONLY`. Each line is written as one `WriteString` call (atomic at the kernel level for writes < PIPE_BUF — well above one JSONL line's ~1 KB typical size). After a batch of up to 100 records OR a 250 ms quiet period (whichever comes first), the writer calls `file.Sync()` to fsync the page cache to disk.

**Why O_APPEND.** Two writers (atlas + e.g., logrotate's `copytruncate`) appending to the same file is the standard log-rotation hazard. With `O_APPEND`, every write is positioned at end-of-file at write time, so concurrent appenders don't collide. logrotate's default `copy + truncate` mode breaks this; the operator must use `create` mode (atomic rename + reopen on SIGHUP). This is documented in `docs/observability.md`.

**Why batched fsync.** Per-record fsync would dominate latency (millisecond per write). Batching to 100-records OR 250ms gives ~10ms p99 effective latency at high throughput AND bounds the data-loss-on-crash window to 250ms. Within the constitutional commitment of an append-only ledger, this is the right trade-off.

## D8 — sink.Emit error semantics: NEVER returns an error to the caller

**Decision.** `sink.Emit(ctx, entry)` returns no error. The signature is `func Emit(ctx context.Context, entry unifiedlog.Entry)` — void. Caller cannot ignore-an-error wrongly.

**Why.** P0-A2 forbids the sink from breaking the in-app write. If `Emit` returned an error, every one of the 9 call sites would have to decide what to do with it — and the safe answer is always "ignore." Better to encode that in the type system: there IS no error to handle. The sink does its best (channel send, fallback table on overflow); failures are visible via slog ERROR + the fallback table, not via a return value the caller has to handle.

The trade-off: a unit-test asserting "Emit failed" needs a different mechanism. The sink package exposes `func (s *Sink) Stats() Stats` returning `{Emitted, Buffered, Dropped, FailureRows}` counters for tests + production observability.

## D9 — Integration test mock: in-memory sink for the 100-record + 10001-record cases

**Decision.** The integration tests do NOT write real files. They construct a `*Sink` with `WithWriter(io.Writer)` returning the data into an in-memory buffer (or `discard.Writer` + counter for the backpressure test). The 100-record test asserts the buffer's line count + each HMAC's validity. The 10001-record test asserts buffer == 10000 lines AND the `audit_sink_failures` table contains 1 row.

**Why.** Real-file tests would be flaky in CI (disk space, permissions, parallel-test race on tmpfile cleanup). The writer goroutine itself is the SUT; the file is an implementation detail. The exported `WithWriter` option (the only test-seam in an otherwise file-bound package) is the test-only seam.

## D10 — Migration timestamp: `20260518000000_audit_sink_failures.sql`

**Decision.** Use the next monotonic timestamp after `20260517000000_unified_audit_log.sql`. Pick `20260518000000` to reflect today's date and the natural ordering.

**Why.** The migration runner globs `migrations/sql/*.sql` in lexical order; timestamp prefixes give natural ordering. No collision with existing files.

## D11 — Cap canonical entry size at 1 MiB (CodeQL allocation-overflow fix)

**Decision.** Add `const MaxCanonicalSize = 1 << 20` and reject any entry whose marshaled canonical JSON exceeds the cap. Rejection path: `canonicalizeAndSign` returns an error → `writeOne` increments `WriteErrors` + writes a fallback row + ERR-logs. Cap is checked AFTER `json.Marshal` and BEFORE the splice allocation.

**Why.** CodeQL alert #29 (`go/allocation-size-overflow`) flagged `make([]byte, 0, len(canonical)+len(tag)+16)` on line 448 because `canonical` derives from `entry.PayloadJSON` — a caller-supplied value that the type system does not bound. On 64-bit Go (our deployment target) the sum cannot in practice overflow `int`, but CodeQL's data-flow analysis cannot prove that without a visible bound, and defense in depth says a malicious or buggy upstream that hands the sink a multi-GB payload would OOM the writer goroutine regardless of overflow. Bounding it explicitly:

1. Silences the CodeQL alert (the bound is a constant, visible to the analyzer).
2. Defends against pathological inputs.
3. Composes with P0-A1 (no silent drop): rejected entries land in `audit_sink_failures` with `reason="write_error"` + error message `"canonical entry exceeds 1048576 byte cap (got N)"`.

The cap of 1 MiB is generous — typical audit-log entries are < 4 KiB; the payload_json fields in slice-124's 9-table union are small structured records (before/after diffs, decision metadata, evidence pointers). 1 MiB leaves > 250x headroom for unusual cases while keeping the per-write allocation bounded.

Test: `TestCanonicalize_RejectsOversizedEntry` constructs an entry with a `payload_json` larger than the cap, calls `Emit`, asserts `WriteErrors == 1`, `Emitted == 0`, and 0 bytes written to the sink.
