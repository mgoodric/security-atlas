# 656 — Cross-connector shared webhook-receiver abstraction (refactor)

**Cluster:** Connectors
**Estimate:** M (1-2d)
**Type:** JUDGMENT (abstraction shape — which idioms factor cleanly vs which stay per-vendor)
**Status:** `ready` (no external dependency; three concrete receivers now exist to factor against)

## Narrative

Surfaced during slice 540 (PagerDuty event-driven webhook profile,
decisions-log D1). There are now **three** independently-built source-side
webhook receivers that mirror the same ~8 idioms without sharing code:

- `connectors/github/internal/githubwebhook` (slice 044) — the original.
- `connectors/hris/webhook` (slices 573 / 655) — a shared HRIS receiver, but
  its `Verifier`/`WorkerFetcher`/`PayloadParser` interfaces are typed against
  HRIS domain types (`worker.HRIS`, `worker.RawWorker`).
- `connectors/pagerduty/internal/webhook` (slice 540) — PagerDuty-specific.

Each independently re-implements: the receive→**verify-first**→build→push
pipeline; the gosec-G112 bounded `http.Server` (ReadHeader/Read/Write/Idle
timeouts); `Serve(ctx)` graceful SIGINT/SIGTERM shutdown; `MaxBytesReader→413`
body cap; constant-time `hmac.Equal` HMAC-SHA256-over-raw-body verification; the
`%q`-at-the-log-sink CWE-117 discipline; and the reuse of a per-connector
idempotency key for cross-profile (webhook+pull) dedup.

Slice 540 deliberately did NOT force a shared abstraction (it would have either
contorted the HRIS package or leaked HRIS types into PagerDuty — a premature
abstraction with only two prior shapes). With a **third** concrete shape now in
hand, the cost/benefit flips: a `connectors/shared/webhookrecv` package can own
the **vendor-agnostic** idioms (bounded server + lifecycle + body-cap + the
generic constant-time HMAC core + the verify-first handler skeleton) while each
connector keeps a thin vendor adapter (its signature-header scheme, its payload
parser, its record builder). The HMAC verifiers differ only in header name,
encoding (hex/upper/base64), and prefix/multi-signature handling — all
parameterizable.

## Acceptance criteria (sketch — refine at pickup)

- [ ] A `connectors/shared/webhookrecv` package owns the bounded `http.Server`
      constructor (gosec-G112 timeouts), `Serve(ctx)` graceful shutdown, the
      `MaxBytesReader→413` body cap, and the verify-first handler skeleton.
- [ ] A parameterizable constant-time HMAC-SHA256 verifier core covering the
      header-name / encoding (hex · hex-upper · base64) / prefix / multi-signature
      (comma-separated `v1=`) variants the three connectors need.
- [ ] github + hris + pagerduty receivers refactored onto the shared package,
      each keeping only its vendor adapter (header scheme · payload parser ·
      record builder).
- [ ] No behavior change: every existing receiver test stays green; the
      verify-first-before-record P0 invariant holds for all three; the
      `%q`-at-log-sink discipline preserved.
- [ ] No platform-side wire change (push only — invariant #3); ZERO
      `internal/api/` / `migrations/` / `proto/` / `schemaregistry/` diff.
- [ ] Coverage floors for the new shared package added; touched packages stay ≥
      floor.

## Anti-criteria (P0)

- **P0.** No platform-side wire change (the receivers stay source-side).
- **P0.** No weakening of the verify-first-before-record invariant for any
  connector during the refactor.
- **P0.** No change to any emitted evidence kind or idempotency key (pure
  refactor; dedup behavior must be byte-identical).

## Dependencies

- **#044, #573, #655, #540** — the three concrete receivers to factor against;
  all merged / in-review.

## Notes

This is a refactor for navigability + future-connector velocity (the next
webhook connector — e.g. an MDM #557 receiver — drops onto the shared package
instead of re-deriving the idioms a fourth time). It is explicitly NOT urgent:
the three receivers work correctly today. Pick up when a fourth webhook
connector is imminent, or as a standalone tech-debt drain.
