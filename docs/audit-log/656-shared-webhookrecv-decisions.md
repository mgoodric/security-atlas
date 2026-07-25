# Slice 656 — shared webhook-receiver abstraction · decisions log

**Type:** JUDGMENT (abstraction shape — which idioms factor cleanly vs which stay per-vendor)
**Anti-criterion (P0):** byte-identical behavior — every existing test across all
three receivers stays green with NO assertion change.

- detection_tier_actual: unit
- detection_tier_target: unit

(The one behavior-preservation hazard surfaced during the slice — the pagerduty
package dipping below its 90 coverage floor after server-lifecycle code moved to
the shared package — was caught by the local coverage gate, the unit tier. Fixed
in-slice by adding the pure-Go `helpers_test.go` for the summary normalizers,
NOT by lowering the floor. No production-tier bug surfaced.)

## Decisions made

### D1 — Shared API surface: a config STRUCT for HMAC, free FUNCTIONS for the server, a function + small interface for the skeleton

The shared `connectors/shared/webhookrecv` package exposes:

- `HMACConfig` (struct: `Header`, `Prefix`, `Encoding`, `Multi`) with
  `Verify(secret, body, header) error` and `Sign(secret, body) string`. The
  parameterizable constant-time HMAC-SHA256 core. A **struct** (not an
  interface) because the variation is pure data (header name / encoding / prefix
  / single-vs-multi), not behavior — each connector declares its scheme
  declaratively rather than implementing a method.
- `Encoding` enum: `EncodingHex` · `EncodingHexUpper` · `EncodingBase64`.
- `NewServer(addr, path, handler) *http.Server` and `Serve(ctx, srv) error` —
  free functions (the gosec-G112 bounded server ctor + graceful-shutdown
  lifecycle). No state to carry, so no constructor type.
- `Handle(w, req, maxBodyBytes, Verifier, BuildAndPush)` — the verify-first
  skeleton, as a free function taking a `Verifier` interface (one method:
  `Verify(body, header) error`) and a `BuildAndPush` callback
  (`func(req, body) (status int)`).
- `Verifier` interface (1 method) + `BuildAndPush` func type — the vendor seam.
- `ErrUnsigned` / `ErrBadSignature` — the two shared sentinels.

**Rationale:** config-struct-for-data + free-functions-for-stateless-lifecycle is
the lowest-ceremony shape that still keeps the verify-first invariant in ONE
place. Pattern-matched to the pre-existing HRIS `Config`/`NewServer`/`Serve`
shape (slice 573) — the shared package is essentially that shape promoted up one
level and made generic.

Confidence: **high**.

### D2 — The seam boundary: skeleton ends at "verified raw body"; the vendor `BuildAndPush` owns everything downstream

`webhookrecv.Handle` owns the vendor-AGNOSTIC preamble verbatim: method check →
`MaxBytesReader`→413 → `io.ReadAll` → **verify FIRST** → 401-on-fail. The moment
a body is verified it is handed to the vendor's `BuildAndPush(req, body) int`,
which returns the HTTP status the skeleton writes. The skeleton owns the
response writer end-to-end (the callback never writes to `w`) so the
verify-first ordering and the 405/413/401/4xx status discipline live in exactly
one file.

**Why a single callback returning a status, not a richer interface:** the three
connectors' downstream steps are genuinely different shapes (github: extra
delivery/event header checks → transform → push; hris: parse → dedup/cap →
N-way fan-out; pagerduty: decode-summary → skip-non-incident → single
build+push). A status-returning callback is the narrowest seam that lets each
keep its exact downstream logic unchanged. github did NOT adopt the skeleton
(see D4).

Confidence: **high**.

### D3 — HRIS fan-out maps onto the skeleton WITHOUT leaking HRIS types into the shared package

The shared package carries ZERO connector domain type (no `worker.RawWorker`, no
`incidents.Incident`). HRIS's richer machinery — `WorkerFetcher`,
`PayloadParser.ParseWorkerIDs` (the slice-655 fan-out), `dedupCap`, `processAll`,
`process` — ALL stay in the HRIS package, unchanged. The fan-out is the body of
HRIS's `buildAndPush(req, body) int`: it parses worker ids, dedups/caps them, and
fans out one re-read+build+push per worker, returning 200 (all ok / no
actionable worker) or 502 (≥1 worker failed). The skeleton sees only a body and
a status. github/pagerduty implement `buildAndPush` with their single-record
path. The simpler adapters carry NO HRIS concept — the asymmetry lives entirely
in each adapter's callback body, which is exactly where it belongs.

Confidence: **high**.

### D4 — github keeps its own `VerifySignature` error taxonomy; adopts only the shared HMAC compute/compare

github's `VerifySignature` distinguishes `ErrMissingSignature` /
`ErrMalformedSignature` / `ErrBadSignature` — a finer-grained taxonomy than the
shared `ErrUnsigned`/`ErrBadSignature` pair, and its unit tests assert each via
`errors.Is`. Collapsing it would change behavior. So github keeps its syntactic
pre-checks (missing → `ErrMissingSignature`; bad prefix / bad hex →
`ErrMalformedSignature`) and delegates only the **constant-time digest match** to
the shared `HMACConfig` (lowercase-hex, byte-identical to its prior
hex-decode-then-`hmac.Equal`). `Sign` now routes through `HMACConfig.Sign`.

github also did NOT adopt the skeleton handler: its handler reads with
`io.LimitReader(1<<20)` returning **400** on read error (not `MaxBytesReader`→413),
and runs extra delivery/event-header checks before transform. Forcing it onto the
generic skeleton would change the body-cap mechanism and the 400-vs-413 status —
a behavior change. github's handler is left as-is (it already shares the
verify-first ordering conceptually). github's shared adoption is therefore the
HMAC core only.

**Rationale:** a pure refactor preserves behavior; where unifying would change a
test-asserted status or error, the right move is to NOT unify and keep the thin
divergence in the adapter. This is the JUDGMENT call the slice exists to make.

Confidence: **high**.

### D5 — Shared `ErrUnsigned`/`ErrBadSignature` are re-exported as the connectors' package-local sentinels (var alias)

HRIS and PagerDuty tests assert `errors.Is(err, ErrUnsigned)` / `errors.Is(err,
ErrBadSignature)` against their package-local names. To keep those green with no
test change, each package's `ErrUnsigned`/`ErrBadSignature` are now `var
ErrUnsigned = webhookrecv.ErrUnsigned` aliases — the SAME error value the shared
core returns, so `errors.Is` resolves identically. HRIS's `SigEncoding` and
`EncodingHex`/`EncodingHexUpper` are likewise re-exported aliases of the shared
`Encoding` type/constants, preserving every existing call site (slices 573/655).

Confidence: **high**.

### D6 — Coverage: new shared floor 96; pagerduty floor HELD at 90 by adding pure-Go tests, never lowered

The new `connectors/shared/webhookrecv` floor is **96** (measured 98.8%,
`floor(98.8−2)`). Moving the server-lifecycle code out of the pagerduty package
dropped its measured coverage to 89.7% (< its 90 floor). Per the monotonic-ratchet
contract the floor is NOT lowered — instead a pure-Go `helpers_test.go`
(slice-353 Q-2 convention) was added exercising `normalizeStatus` /
`normalizeUrgency` / `parseTime`, lifting pagerduty back to 96.6%. github (84.2 ≥ 83) and hris (97.0 ≥ 90) stayed above their floors with no new tests.

Confidence: **high**.

## Revisit once in use

- **MDM #557 (the next webhook connector)** is the real test of D1/D2: when it
  drops onto `webhookrecv` instead of re-deriving the idioms a fourth time,
  re-evaluate whether the `BuildAndPush(req, body) int` seam is expressive enough
  (e.g. if #557 needs a non-2xx ack body the skeleton's `statusMessage` map does
  not cover) or whether the skeleton should hand back a richer result.
- **github skeleton adoption (D4):** if a future change makes github's handler
  want `MaxBytesReader`→413 semantics (aligning with hris/pagerduty), the
  skeleton becomes adoptable for github too — but that is a deliberate behavior
  change (400→413) and must be its own slice with the github tests updated, not
  folded into a refactor.
- **base64 encoding** is implemented and unit-tested in the shared core but NOT
  yet exercised by any production connector (all three are hex / hex-upper /
  v1=hex). The first base64-signing vendor validates it end-to-end.
- **`statusMessage` response bodies** ("bad request" / "upstream error") were
  reproduced from the pre-refactor receivers to keep the response byte-identical.
  If a connector ever needs a different 4xx body, it should return that via a
  richer seam rather than expanding the shared map.

## Confidence summary

| Decision                                                           | Confidence |
| ------------------------------------------------------------------ | ---------- |
| D1 shared API surface (config struct + free funcs + callback seam) | high       |
| D2 seam boundary at "verified raw body"                            | high       |
| D3 HRIS fan-out maps on without type leak                          | high       |
| D4 github keeps its error taxonomy + handler                       | high       |
| D5 sentinel/encoding re-export aliases                             | high       |
| D6 coverage floors (new 96; pagerduty held at 90)                  | high       |
