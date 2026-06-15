# Slice 541 — decisions log

Wire the staleness digest (slice 439) to email delivery.

- detection_tier_actual: none
- detection_tier_target: integration

No bug surfaced during the build. The slice is a regression-guard + wiring
verification: the producer (439), the substrate (445), and the cross-channel
digest scheduler (582) were all already on `main` and already correctly wired.
The work was proving the 439 → 445 path end-to-end and recording the JUDGMENT
calls. The new integration test is the durable guard against a future silent
regression (its target tier is integration; the unit tests add a fast-tier
complement).

## Decisions made

### D1 — No new scheduler; reuse the slice-582 digest sweep. (Confidence: high)

The slice-541 spec was written when slice 445 was the newest delivery work and
assumed a tick still needed building ("445 left the tick to its consumers").
Between the spec being filed and this build, **slice 582 shipped the exact tick**
at `internal/notify/scheduler` — an in-process daily fan-out that enumerates the
opted-in (tenant, user) pairs per channel via a migrator/BYPASSRLS pool and
drives each channel's `DeliverDigest` per user under that user's own tenant
context (`tenancy.WithTenant`). The email channel is already registered
(`scheduler.EmailChannel`) and already started in `cmd/atlas/main.go`
(lines ~1104-1163) when SMTP is configured. Building a parallel email-only tick
would duplicate that machinery and violate the slice's own scope discipline
("strongly prefer wiring through the existing scheduler"). So: no new code on the
delivery path. This slice contributes the missing **AC-2 proof** that the
staleness producer flows through that sweep, plus this record.

### D2 — Cadence: daily, env var `ATLAS_DIGEST_INTERVAL`, default 24h. (Confidence: high)

Already chosen + shipped by slice 582 and inherited unchanged:
`scheduler.DefaultInterval = 24 * time.Hour`, overridable via
`ATLAS_DIGEST_INTERVAL` (a `time.ParseDuration` string; invalid values log and
fall back to the default). Daily aligns with the slice-445 per-UTC-day
`digest_key` idempotency period, so a finer tick is harmless (the
claim-before-send makes extra passes no-ops) and daily is the honest period name
(canvas honest-interval discipline — not marketed as "continuous monitoring").
This is the cadence slice 541 was asked to choose; it was already the right one,
so it is adopted as-is rather than re-litigated.

### D3 — Reuse the generic slice-445 digest template; no staleness-specific template. (Confidence: high)

The slice doc names this the default and asks for a staleness-specific template
only "if clearly better". It is not better here:

- The generic digest builder (`internal/notify/email/message.go`) already maps
  `evidence.staleness` to its own human label, "Stale-evidence digests", in the
  closed `typeLabels` map. A staleness reminder already reads as such in the
  inbox.
- The digest is intentionally minimum-disclosure (counts + a single deep-link,
  no payload — P0-445-4). A staleness-specific template would either stay at the
  same disclosure level (no operator gain) or widen disclosure (adds the exact
  surface 445's threat model closed).
- A second template is a second header/CRLF-guard surface to maintain, against
  P0-541-1 ("do not change the 445 minimum-disclosure body / guards").

So the generic template is reused unchanged. A richer staleness-specific email
("N controls have stale evidence, top offenders…") is captured as a future
revisit, not built here.

### D4 — Per-user, per-tenant isolation inherited, not re-implemented; proven by a real-RLS test. (Confidence: high)

The scheduler already delivers per user under that user's own tenant GUC and
never batches across tenants in one tx (the enumeration reads only (tenant,
user) keys via BYPASSRLS; every content read happens inside `DeliverDigest`
under `tenancy.WithTenant`). Slice 582's suite already proves no cross-tenant
delivery (`TestSweepOnce_TwoTenantsNoCross`, `TestSweepOnce_TenantIsolation`).
This slice adds AC-2 coverage (the staleness type specifically) and relies on
the existing tenant-isolation tests for P0-541-2; both run against live Postgres
with RLS enforced in this build.

## Tests added (this slice)

- `internal/notify/scheduler/integration_test.go`:
  `TestSweepOnce_StalenessNotificationFlowsToDigest` — seeds an opted-in user
  whose only unread notification is a real `evidence.staleness` row, runs
  `SweepOnce` against live Postgres + RLS through the real email channel, and
  asserts the delivered digest body contains "Stale-evidence digests" (AC-2).
  Added `seedUserWithType` (explicit notification-type seam) and a `bodyFor`
  provider accessor; the three existing tests are unchanged behaviorally
  (`seedUser` now delegates to `seedUserWithType`).
- `internal/notify/email/message_test.go`:
  `TestTypeLabel_StalenessHasSpecificLabel` (pins the `evidence.staleness` label
  is specific, not the generic fallback) and `TestBuildDigest_StalenessSurfacesInBody`
  (a built digest renders the staleness label) — pure-Go, fast tier (slice-353
  Q-2).

A mutation check confirmed the guards have teeth: temporarily dropping
`evidence.staleness` from the `typeLabels` map fails both unit tests and the
integration test; reverting makes them green.

## Verification

- `go build ./...`, `go vet ./internal/notify/...`,
  `go vet -tags=integration ./internal/notify/...` — clean.
- `go test ./internal/notify/email/...` — green (unit).
- `go test -tags=integration -p 1 ./internal/notify/scheduler/... ./internal/notify/email/...`
  against a live postgres:16 container (bootstrap `01-roles.sql` + all forward
  migrations, `atlas_app` role) — all green, including the new AC-2 test and the
  inherited cross-tenant isolation tests. Live-tested (not deferred).
- No production (`!_test.go`) code changed → no coverage-floor movement; the
  `internal/notify/email` (65) and `internal/notify/scheduler` (69) floors are
  unaffected (test-only additions cannot lower coverage).
- No new HTTP route → no openapi-drift surface.
- No migration (the substrate + producer + tick all pre-exist; cadence is an env
  var).

## Revisit once in use

- **Staleness-specific digest email.** If the solo operator finds the generic
  "Stale-evidence digests: N" line too terse to act on without opening the app,
  consider a staleness-specific section ("top stale controls, oldest first")
  drawn from the slice-439 `DigestPayload` — at the SAME minimum-disclosure level
  (control IDs + counts, no evidence payload). Filed only as a revisit; not a
  committed slice.
- **Per-user cadence preferences.** The cadence is platform-wide
  (`ATLAS_DIGEST_INTERVAL`). If operators want per-user "daily vs weekly" digest
  frequency, that is a new opt-in dimension on top of the slice-445 master
  opt-in + slice-542 per-kind prefs — a separate slice, not a tweak here.
- **Send pacing under many tenants.** The per-user idempotency key already bounds
  to one send per user per UTC day, but a single-VM self-host with thousands of
  opted-in users would fan out many SMTP sends in one tick. Revisit pacing/jitter
  (the threat-model "D — Denial of service" note) only if a real deployment hits
  it; today the bound is the day-key, which is sufficient for the v1 solo-operator
  target.
