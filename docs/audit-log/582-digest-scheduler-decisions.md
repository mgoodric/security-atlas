# Slice 582 — notification-channel digest scheduler (fan-out driver): decisions log

**Slice type:** JUDGMENT (scheduling shape + enumeration mechanism + idempotency
reuse are build-time calls recorded here, not blocked on a human sign-off — the
runtime AI-assist boundary is unrelated and untouched).

**Parent:** slice 543 ("Slack + webhook channels") decisions-log "What this
slice does NOT do" named the per-user fan-out driver as the follow-on; slice 445
(email) shipped the same `DeliverDigest` substrate. Both are merged on `main`;
the slice doc's "blocked on 543/445" line is stale.

This slice adds the recurring DRIVER over the existing per-channel
`DeliverDigest` sinks. It is a delivery driver, not a producer: it reads the
opt-in tables and drives the existing minimum-disclosure builders unchanged. It
never writes a notification and never widens per-channel disclosure (P0).

## Detection-tier classification (slice 353 Q-13)

- `detection_tier_actual`: `none` — no bug escaped the tier it should have been
  caught in. One build-time misfit was caught immediately by its target tier: an
  initial generic projection helper referenced an undeclared `pgUUID` type alias
  in the channel adapters → caught at the `unit`/compile tier (`go build`) and
  fixed by projecting to `pgtype.UUID` directly.
- `detection_tier_target`: `unit` — and it was caught at compile (`unit`).

## D1 — Scheduling shape: in-process daily tick-loop, no external cron

**Decision:** an in-process `time.Ticker` loop wired into `cmd/atlas/main.go`'s
startup, mirroring the slice-076 metrics scheduler and the slice-510 backup
scheduler. Default cadence is `24h` (`DefaultInterval`), overridable via
`ATLAS_DIGEST_INTERVAL` for dev loops. The first sweep fires inline on start so
a fresh deploy delivers without waiting a full interval; `ctx` cancellation
stops it cleanly (the same shape as the metrics scheduler's `Run`).

**Why.** The project's locked-in pattern for recurring work on the single-VM
self-host target is in-process tick-loops, not external cron (CLAUDE.md /
slice 510 D1). Daily is the honest digest-period name: it matches the
per-UTC-day `digest_key` the sinks key idempotency on. A finer tick is harmless
(the claim makes extra passes no-ops) but `24h` is the period we name and do NOT
market as "continuous monitoring" (canvas anti-pattern).

## D2 — Enumeration: migrator-pool cross-tenant read of keys only, then per-user RLS delivery

**Decision:** enumerate the opted-in `(tenant_id, user_id)` pairs per channel
through the migrator (BYPASSRLS) pool — the same pattern the metrics scheduler
uses to walk all tenants in one pass — then deliver each user through the app
pool with `tenancy.WithTenant` applied, so every read inside `DeliverDigest` is
RLS-scoped to the user's own tenant. Three new sqlc queries —
`ListEmailOptInUsers`, `ListSlackOptInUsers`, `ListWebhookOptInUsers` — each
`SELECT tenant_id, user_id ... WHERE enabled = true`.

**Why.** The enumeration is the ONE deliberate cross-tenant read; it returns
ONLY the `(tenant, user)` keys — no notification content, no PII — so the
blast radius of the privileged read is the key set the driver needs and nothing
more. `enabled = false` / no-row are excluded by the `WHERE`, so default
opted-OUT (P0-445-7 / P0-543-3) is honored at the SQL layer. The per-user
delivery re-reads RLS-scoped, so Tenant A's notifications can never reach
Tenant B's user (canvas invariant #6). The slice-029 `recipient_user_id` passed
to `DeliverDigest` is the user's UUID rendered as a string — the exact contract
the sinks + their existing integration tests already use
(`DeliverDigest(ctx, userID, userID.String())`).

## D3 — Idempotency: reuse the sinks' claim-before-send; add NO new surface

**Decision:** the driver adds no idempotency state of its own. Each sink's
existing claim-before-send (the per-UTC-day `digest_key` UNIQUE in
`email_delivery_log` / `channel_delivery_log`) makes a re-run within the same
UTC day a no-op: the second claim collides on the UNIQUE key and the sink
returns `Skipped`, so no user is double-sent. A partial sweep that re-runs is
therefore safe.

**Why.** Re-implementing idempotency in the driver would duplicate the guard and
risk drift. The integration test `TestSweepOnce_IdempotentNoDoubleSend` proves
the driver double-CALLS `DeliverDigest` across two sweeps but the recipient is
sent exactly once — the guarantee is no second SEND, not no second call.

## D4 — Channel binding: thin adapters over a 1-method driver interface

**Decision:** the driver depends on a single `DigestDeliverer` interface
(`DeliverDigest(ctx, userID, recipientUserID) (Delivery, error)`) plus an
`OptInLister` func. The three concrete channels (email/slack/webhook) each
return their own `DeliveryResult{Sent,Skipped,Reason}`; `channels.go` flattens
each into the shared `Delivery{Sent,Skipped}` via a per-channel adapter. The
driver imports none of the channel result types directly through the interface.

**Why.** This honors slice 543 D1 (sibling packages, no heavy registry): the
driver does not force a shared `Digest`/recipient abstraction onto the channels.
The adapter layer is ~30 lines and keeps the driver decoupled from each
channel's `DeliveryResult` shape, so a future channel plugs in by writing one
adapter + one enumeration query.

## D5 — main.go mounting: only configured channels are mounted

**Decision:** in `cmd/atlas/main.go` the digest scheduler mounts only the
channels whose config is present — email when SMTP is configured
(`emailCfg.Enabled()`), Slack when a webhook URL is set, webhook when a URL is
set AND passes the SSRF guard. If no channel is configured the scheduler is not
started (log-only) and its pool is closed.

**Why.** A channel that can never deliver (no SMTP host / no webhook URL) should
not have the driver spin enumerating users for it every tick. This mirrors the
backup scheduler's "only mount when the target builds" guard. The webhook SSRF
guard runs at construction so a rejected internal target fails fast and visibly
(log-only) rather than at send time (P0-543-2).

## Verification

- `go build ./...` clean.
- `go test ./internal/notify/...` green (unit, incl. new scheduler suite).
- `go test -tags=integration -p 1 ./internal/notify/...` green against local
  Postgres with bootstrap roles + all forward migrations applied.
- `bash scripts/audit-integration-enrolment.sh` → OK (scheduler enrolled in
  `scripts/integration-shards.txt` leg B2).
- `bash scripts/check-integration-shard-coverage.sh` → OK.
- `just sqlc-generate` → no drift (the committed dbx matches the queries).
- `golangci-lint run` (incl. `--build-tags=integration`) on touched packages →
  0 issues.
- Coverage: merged unit+integration 71.43% for `internal/notify/scheduler`;
  floor set to 69 in `cmd/scripts/coverage-thresholds.json`
  (`floor(measured - ~2pp)`, the slice-426 convention).
