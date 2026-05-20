# Slice 145 — Data-export hardening decisions

Slice 145 (`docs/issues/145-data-export-hardening-payload-redaction-concurrency.md`)
ships two operator-facing knobs on top of slice 135's data-export library:

1. `?include_payload=<bool>` query param on `GET /v1/admin/audit-log/export`
2. Per-(tenant, user) concurrency cap via a new `internal/export/concurrency.go`

Both are non-breaking additive changes. The judgement calls below were
made during the build-time pickup and are recorded here per the
JUDGMENT-slice convention.

## D1 — `?include_payload` default direction

**Decision:** Default to `true`. `?include_payload` is opt-out, not opt-in.

**Why:**

- Slice 135 already shipped with the payload column populated. Flipping
  to default-false is a wire-shape break for every existing caller —
  scripts, the BFF download UI, any downstream forensics tooling. The
  slice doc P0-HARDEN-1 explicitly requires the slice 135 wire shape to
  be preserved.
- Forensics is the load-bearing v1 use case for the audit-log export.
  An accidental redacted forensics export is materially worse than an
  accidental over-permissive external-audit handoff: in the first case
  the operator may not notice payloads are missing until the incident
  response is already underway; in the second case the operator
  positively chooses `?include_payload=false` and signs off on it.
- The opt-out is a single URL parameter. The friction tax on the rare
  external-audit-handoff workflow (operator types six extra
  characters: `&include_payload=false`) is negligible.
- The meta-audit row records the value used. An operator reviewing
  past exports can grep `me_audit_log` for the explicit-false events
  and confirm which exports went to which audience.

**Rejected:**

- **Default false ("redact by default").** Breaks the slice 135 wire
  shape. Even if we accepted the break, it would make the most common
  workflow (forensics) the one that requires extra ceremony, which is
  exactly inverted from the v1 user's actual ratio of forensics to
  audit-handoff exports (~10:1 by retro-STRIDE estimate).
- **No default — make `?include_payload` required.** Maximally
  explicit, but breaks slice 135 callers identically to default-false
  with no upside that the explicit recorded value doesn't already
  give us.

## D2 — Concurrency cap default

**Decision:** Default cap = 2 per (tenant, user). Tunable via
`ATLAS_EXPORT_MAX_CONCURRENT_PER_USER`.

**Why:**

- The export endpoint streams for the duration of the encoder write.
  At the slice 135 default row cap of 100,000 a CSV export holds a
  pgxpool connection for ~5–15 seconds depending on disk I/O and
  network upload speed. Two in-flight exports under one operator is
  already 20% of a default 10-connection pgxpool — meaningful pressure
  on the per-tenant pool.
- Two simultaneous forensic exports is also the realistic upper bound
  for one human running audit-handoff work. A typical workflow is
  "kick off the CSV for SOC 2 evidence, switch tabs and start the
  JSON for the auditor-handoff zip in parallel"; cap=2 admits that
  while a third concurrent request (which would almost always be a
  client bug or a runaway script, not deliberate operator behavior)
  surfaces fast as a 429.
- The env var gives the operator a tuning knob without a code change.
  Operators with large pgxpools (50+ connections) can raise the cap
  to 5 or 10; operators on small VPS deployments can lower it to 1
  if a single export already starves the pool.
- The cap is per-(tenant, user) and **not** queueing. Refused requests
  do NOT block on a future slot to free up — they return 429
  immediately with `Retry-After: 30`. Queueing would convert the DoS
  into a latency tax on every other request in the pool (the queue
  itself becomes the failure mode).

**Rejected:**

- **Cap = 5.** Loosens the DoS mitigation more than necessary. Five
  simultaneous CSV exports against the default 100K row cap is
  ~50% of a 10-connection pgxpool — at that point the operational
  blast radius starts to overlap with the slice 135 row cap. Operators
  who want this can set the env var; making it the default puts the
  thumb on the wrong side of the safety/throughput trade-off.
- **Cap = 1.** Tightest possible mitigation but eliminates the
  realistic "two-exports-in-flight" forensics workflow above. Forces
  every operator to serialize their work, which is a regression
  against slice 135's perceived UX.
- **Global cap, not per-(tenant, user).** A global cap of 2 would
  throttle a super_admin running exports across five tenants. The
  DoS surface lives at the per-tenant pgxpool level — the cap should
  apply at that granularity, not at the user-identity level.
  (Slice 145 P0-HARDEN-2.)
- **Queueing semaphore (with timeout).** Mentioned above — queueing
  converts the DoS into a latency tax. Non-blocking refusal forces
  the misbehaving client to back off out-of-band, which is the
  correct shape for a server-side cap.

## D3 — Encoder option shape (additive `WriteRowsWithOpts`)

**Decision:** Add a new `Exporter.WriteRowsWithOpts(w, header, rows, opts)`
method alongside the existing `WriteRows`. `WriteRows` calls into the
opts variant with a zero-valued `WriteOpts{}` for backwards-compat.

**Why:**

- Per-encoder option threading is the cleanest way to surface the
  JSON `null` rendering for redacted columns without forcing CSV and
  XLSX to grow a "render this cell as null" code path they don't
  semantically need (those formats render redacted columns as empty
  cells, which is the right shape for an external auditor anyway).
- The zero-valued opts case is byte-for-byte equivalent to the slice
  135 WriteRows path — verified by `TestWriteRowsMatchesWriteRowsWithOptsZero`.
  Existing slice 135 callers (and slice 136/137/138/139 spillovers
  not yet shipped) keep their current call sites unchanged.
- A simple field on `WriteOpts` (`NullForEmpty map[string]bool`) is
  easier for future contributors to extend than a typed-per-format
  options struct. New options land as new map keys; CSV + XLSX
  ignore them; JSON honors what's relevant.

**Rejected:**

- **Add a new parameter to `WriteRows`.** Breaks every existing call
  site. Goes against slice 145's "non-breaking + additive" framing.
- **Per-format functional options (`csv.WithRedactedColumns(...)`).**
  Cleaner type signature but doesn't compose with the shared
  `Exporter` interface — the handler resolves the encoder polymorphically
  via `ResolveExporter`, so per-format opts would need a runtime
  type assertion to thread through. More code for no benefit.

## D4 — 429 response body shape

**Decision:** 429 returns a JSON body with three keys:
`error` (human-readable string mentioning the cap),
`retry_after_seconds` (integer 30), `cap` (integer = configured cap).
The header `Retry-After: 30` is set alongside.

**Why:**

- P0-A10 explicitly requires both a header AND a JSON body —
  operators reading `curl https://atlas/... | jq` without `-i` MUST
  still see the limit message. Header-only would be invisible to
  the most common operator probe shape.
- The JSON body MIRRORS the header value (`retry_after_seconds: 30`)
  so scripts can read either source. Scripts that ALREADY honor
  `Retry-After` headers (most HTTP clients) need no change; scripts
  that parse the body get the same number.
- Including the configured `cap` in the body helps operators
  diagnose unexpected 429s — a misconfigured
  `ATLAS_EXPORT_MAX_CONCURRENT_PER_USER=1` would surface as
  `"cap": 1` and the operator can correlate without grepping the
  server's startup log.

**Rejected:**

- **Plain-text body.** Inconsistent with the rest of the platform's
  4xx/5xx wire shape (`{"error": "..."}`). Operators parse JSON
  errors with `jq -r .error`.
- **Empty body (header-only).** Violates P0-A10.

## D5 — Goroutine-leak posture (idempotent release)

**Decision:** The `Limiter.Acquire` return is a `func()` closure
guarded by `sync.Once` over a buffered-channel receive. Calling the
release function twice is a no-op (the second call is a Once-noop).

**Why:**

- Slice 145 P0-A9 requires every acquired slot to release on defer,
  including panic / error paths. The handler's `defer release()` is
  the load-bearing pattern; making the release idempotent means the
  handler can ALSO have an explicit release on a happy-path branch
  without double-freeing the slot to a sibling caller.
- The buffered-channel-of-`struct{}{}` design is the standard Go
  semaphore primitive — simple, zero-allocation per slot, no
  dependency on `golang.org/x/sync/semaphore`. The Once guard is
  three lines of code; the alternative (e.g. an atomic counter)
  would be more complex for the same goroutine-safety contract.

**Rejected:**

- **Non-idempotent release.** A double-defer (defer at acquire, then
  an explicit release on a happy-path return) would steal a slot from
  a concurrent caller. The slice 135 export handler already has
  multiple branches that could each plausibly call release; the
  idempotent shape removes the footgun.

## D6 — Where the limiter lives (singleton vs DI)

**Decision:** Process-wide singleton at `export.DefaultLimiter()`,
overridable per-handler via `Handler.WithLimiter(l)` for tests.

**Why:**

- The cap is a process-wide property (the pgxpool the cap protects
  is also process-wide). A singleton matches the operational reality.
- The env-var read happens once at first use via `sync.Once` — tests
  that inject a custom limiter via `WithLimiter` get deterministic
  caps without touching `os.Setenv` (which is process-global state
  that leaks across tests in the same `go test` invocation).
- Production callers leave `Handler.limiter` nil and resolve the
  singleton lazily on every request. The pointer dereference is the
  only per-request overhead.

**Rejected:**

- **Per-handler limiter, no singleton.** Forces every handler
  constructor to receive the limiter. Surfaces a configuration burden
  at every wiring site for what is conceptually a global cap.
- **`context.Value`-threaded limiter.** Overkill for a tunable that
  doesn't change per-request. The `context.Value` anti-pattern.

## P0 anti-criteria audit

All P0 anti-criteria from `docs/issues/145-data-export-hardening-payload-redaction-concurrency.md`
honored:

- **P0-A1** (no default flip to false): D1 keeps default true.
- **P0-A2** (no column-level redaction beyond payload_json):
  Verified — `tenant_id`, `actor_id`, `target_id`, etc. still render
  on the `?include_payload=false` path. Tested in
  `TestSlice145_IncludePayloadFalseRedactsCSV` (asserts tenant_id is
  non-empty) and `TestSlice145_IncludePayloadFalseRedactsJSON`.
- **P0-A3** (no bandwidth throttling): Concurrency cap is the only
  mitigation; per-stream byte throttling is out of scope.
- **P0-A4** (no `?include_payload` for non-audit-log entities): The
  flag is parsed and threaded exclusively in
  `internal/api/adminauditlog/export.go`. Slices 136-139 (per-entity
  exports, not-ready) will adopt the encoder-side `WriteOpts` hook
  independently when they ship.
- **P0-A5** (no destructive shape change to slice 135's meta-audit):
  The new `include_payload` field is optional (`*bool` /
  `json:"include_payload,omitempty"`). Legacy rows without the key
  represent default-true.
- **P0-A6** (no vendor-prefixed test tokens): No test fixtures use
  vendor token prefixes; the only "key\_..." identifiers
  (`key_test_export_145`) are local string literals naming the
  in-process test credential, never tokenized.
- **P0-A7** (no slice 124 wire shape change): Slice 124's
  `/v1/admin/audit-log/unified` handler is untouched. The slice 145
  changes live exclusively in `ExportUnified`,
  `internal/export/`, and the meta-audit shape — none of which slice
  124 calls into.
- **P0-A8** (no touching sessions table / slice 141): Verified — no
  sessions changes; the concurrency key is `(tenant_id, user_id)`
  computed from the existing credential context, no DB read.
- **P0-A9** (no goroutine leak — every acquired slot releases on
  defer): D5 documents the idempotent release; tested in
  `TestLimiterReleasesOnDeferEvenAfterPanic`.
- **P0-A10** (429 carries Retry-After header AND JSON body):
  D4 documents the wire shape; tested in
  `TestSlice145_ConcurrencyCapReturns429WithRetryAfter`.

## Test footprint

- `internal/export/concurrency.go` — new file, 230 lines including
  doc comments.
- `internal/export/concurrency_test.go` — new file, 7 test functions
  covering acquire/release, cap, isolation, idempotency, panic-safety,
  sentinel error, concurrent acquires, default-limiter fallback.
- `internal/export/include_payload_test.go` — new file, 6 test
  functions covering CSV/JSON/XLSX behavior under `WriteOpts.NullForEmpty`
  - backwards-compat smoke between `WriteRows` and `WriteRowsWithOpts`.
- `internal/api/adminauditlog/export_concurrency_integration_test.go`
  — new file, 7 test functions covering the wire surface end-to-end
  against a live Postgres + RLS context.
- Slice 135 unit + integration tests all still PASS unchanged.

## Spillovers filed

None. The slice is intentionally narrow:

- The `?include_payload` flag is scoped to `/v1/admin/audit-log/export`
  per P0-A4. Slices 136-139 (per-entity exports) will pick up the
  `WriteOpts.NullForEmpty` hook when they implement; the library
  primitive is in place.
- The concurrency cap covers the audit-log export path specifically.
  Slices 136-139 will register their own
  `DefaultLimiter().Acquire(tenantID, userID)` calls when they ship;
  the slice doc names that as an inherited behavior, not a slice 145
  responsibility.

If a downstream slice surfaces an additional surface that needs the
same shape (e.g. a `?include_payload` for evidence-payload exports,
or a separate concurrency cap for a different bulk endpoint), file
as slice 172 spillover per Amendment 2.
