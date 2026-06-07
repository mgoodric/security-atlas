# 473 — idempotent migrate-on-upgrade (fail-closed) — decisions log

- detection_tier_actual: production
- detection_tier_target: integration

This slice originates from a CONFIRMED PRODUCTION INCIDENT (2026-06-05):
the maintainer's atlas-edge box silently fell 3 migrations behind after a
Watchtower binary update and the demo-seed button failed with a masked
HTTP 500 (the `me_audit_log_action_check` CHECK rejected the `demo_seed`
action value an un-applied migration would have added). `actual =
production` because no automated tier caught "binary newer than DB" — the
self-host-bundle e2e only ever exercised a single first-boot bring-up,
never an upgrade. `target = integration`: the right tier is a
multi-container compose e2e that re-ups after adding a migration. AC-7's
new `migrate` mode IS that tier — it now reproduces the incident and
fails if the gap regresses. Closing the `target` gap is the load-bearing
deliverable.

## Post-push regression fixes (PR #1042 CI)

The first push passed the NEW `migrate` leg but broke the EXISTING
`bundled` + `external` self-host-bundle legs (deterministic). Two
distinct causes, both fixed without touching the compose gate or the AC-7
scenario:

1. **Harness EXIT-trap exit-status leak (caught: `integration`/CI; the
   bundled+external legs).** The `cleanup()` EXIT trap ended with
   `[ -n "$SENTINEL_SQL" ] && rm -f "$SENTINEL_SQL"`. In every non-migrate
   mode `SENTINEL_SQL` is empty, so `[ -n "" ]` returns 1, the `&&`
   short-circuits, and — because it is the LAST statement of a function
   invoked from the EXIT trap — that 1 became the SCRIPT's exit code. Every
   assertion passed (`ALL ASSERTIONS PASSED` printed) but the harness still
   exited 1. `migrate` mode passed only because its non-empty `SENTINEL_SQL`
   made the test true. Fix: replaced the `&&` one-liner with an `if`-block,
   which evaluates to 0 when the condition is false. Pure harness fix; no
   production/compose change.

2. **Proxy overlay missing `atlas-migrate` network membership (proxy
   leg).** `docker-compose.proxy.yml` (slice 470, not owned here) pins each
   base service to the fixed `atlasnet` subnet via a per-service
   `networks:` list. In compose, a service that declares ANY explicit
   `networks:` joins ONLY those — so the new `atlas-migrate` service, absent
   from the overlay, landed on the implicit default network while postgres
   was on `atlasnet` only. atlas-migrate could not reach Postgres and
   looped forever on "Postgres not reachable" (its `restart:"on-failure"`
   masked it as a hang), so its `service_completed_successfully` gate never
   resolved and the whole proxy bring-up stalled. Fix: add `atlas-migrate`
   to the overlay's `atlasnet` membership, mirroring `atlas-bootstrap`.
   This is a consequence of MY new service, so adding its network row is in
   scope (minimal, mirrors the existing pattern).

**Proof after both fixes (local, offset host ports to avoid the shared
host's busy defaults):** all four legs exit 0 — `bundled`, `external`,
`migrate` (AC-7 ledger 67→68 + serve-after-gate + idempotent no-reseed),
and `proxy` (AC-2 proxy IP honoured `203.0.113.10` / AC-3 forged XFF
rejected, recorded `10.124.0.3`).

## What shipped

- `deploy/docker/bootstrap/migrate.sh` — NEW always-run, idempotent,
  fail-closed migrate script. Split out of `bootstrap.sh`'s old phases
  1-2 (wait-for-Postgres + roles + forward migrations + atlas_app
  password). Names the failing migration on failure and exits non-zero.
- `deploy/docker/bootstrap/bootstrap.sh` — migrations REMOVED; now
  first-boot-only seed + SCF import + control upload. Phases renumbered.
- `deploy/docker/bootstrap.Dockerfile` — `chmod +x` migrate.sh; default
  ENTRYPOINT stays `bootstrap.sh` (the migrate service overrides it).
- `deploy/docker/docker-compose.yml` + `docker-compose.edge.yml` — NEW
  `atlas-migrate[-edge]` service (always-run, watchtower-labelled,
  `entrypoint: migrate.sh`, gated only on Postgres healthy). `atlas[-edge]`
  - `atlas-bootstrap[-edge]` now `depends_on` it with
    `condition: service_completed_successfully`.
- `deploy/docker/test-self-host-bundle.sh` — NEW `migrate` mode (AC-7):
  reproduce-the-incident e2e.
- `.github/workflows/ci.yml` — `migrate` added to the self-host-bundle
  matrix (mirrors slice 470's `proxy` leg).
- `docs/SELF_HOSTING.md` + `docs-site/docs/upgrade.md` — migrate-on-upgrade
  behavior + fail-closed contract documented; slice-464 migrate-command
  drift reconciled (AC-8).

## Decisions made

### D1 — SPLIT a dedicated `atlas-migrate` service (vs. make bootstrap re-runnable + watchtower-managed)

**Options:** (a) make the whole `atlas-bootstrap` one-shot
watchtower-managed + `service_completed_successfully`-gated; (b) split a
dedicated always-run `atlas-migrate` service out of bootstrap, leaving
seed/SCF/upload as first-boot-only.

**Chosen: (b).** Option (a) reintroduces the slice-065 DEADLOCK: bootstrap
phase 4 BLOCKS on atlas `/health` (and phase 5 uploads bundles to the
live server), so gating `atlas` on bootstrap _completing_ wedges both
containers. The migrate-only step, by contrast, never touches atlas — it
hits Postgres and exits — so it can be cleanly
`service_completed_successfully`-gated. Splitting is therefore the only
factoring that gives a fail-closed gate WITHOUT the deadlock. It also
cleanly separates "always-run schema truth" from "first-boot seed,"
matching the spec's recommended shape. **Confidence: high** (the deadlock
is documented in the compose file and was a real slice-065 bug).

**Revisit if:** a future migration becomes long-running enough that the
serial migrate-before-serve gate hurts cold-start time materially. The
current set is fast (the AC-7 run applies + gates in seconds).

### D2 — `atlas` gates on `atlas-migrate: service_completed_successfully`; `atlas-bootstrap` stays `service_started`

**Chosen:** atlas (and bootstrap) gate on `atlas-migrate` completing
(the fail-closed P0-473-1 gate), but atlas keeps `atlas-bootstrap:
service_started` to preserve the slice-065 deadlock avoidance (bootstrap's
upload phase needs atlas live). With migrations now in atlas-migrate, the
schema is guaranteed current before atlas serves, and bootstrap's seed
runs in parallel with atlas booting — exactly the prior choreography minus
the migrate footgun. **Confidence: high** (verified in the rendered
compose config + the AC-7 run).

### D3 — Fail-closed exit: name the failing migration, exit non-zero, let the gate block the backend

**Chosen:** migrate.sh wraps each per-file apply; on failure it logs
`FATAL: migration '<filename>' failed`, prints the SQL error (psql
already emitted it), and `exit 1`. The `service_completed_successfully`
gate then leaves `atlas` unstarted — no serving on a partial schema
(P0-473-1 / P0-473-5). The migrate service uses `restart: "on-failure"`
so a transient Postgres-not-ready blip retries, but a genuine migration
failure still ends non-zero (the for-loop exits before the success log)
and the gate stays unmet. **Confidence: high.**

### D4 — atlas_app password ALTER ROLE moves into the migrate step

**Chosen:** the `ALTER ROLE atlas_app PASSWORD` (a DDL-role concern that
must complete before the backend connects as atlas_app) moves from
bootstrap into migrate.sh, so it lands inside the backend-gating step.
Idempotent (ALTER ROLE PASSWORD). **Confidence: high.**

### D5 — AC-7 reproduces the incident by WRITING a new migration into the bind-mounted checkout

**Options:** (a) bring up at an artificially-truncated migration set then
add the real pending ones; (b) bring up at the full current set, then
write a brand-new sentinel migration + re-run migrate (simulating a newer
image's migration set), assert it applies + the recreated backend serves
a current schema + a second run is an idempotent no-reseed no-op.

**Chosen: (b).** It is hermetic (no fragile "truncate the migration set"
staging), uses a far-future-timestamp sentinel that sorts last so the
existing migrate for-loop picks it up unchanged, drops a trivial marker
table (reversible, no tenant data), and is removed by the harness EXIT
trap so the working tree is never left dirty. It exercises the EXACT gap:
a migrate step the running DB has not seen, applied on re-up, with the
backend gated behind it. **Confidence: high** — the AC-7 run passed
end-to-end locally (ledger 67 → 68 on re-up; backend serves a current
schema after force-recreate; second run logs `schema current` with
unchanged seed counts).

## Constitutional invariants honored

- **Fail-closed integrity** — the backend never serves against a partial
  schema (`service_completed_successfully` gate). Mirrors canvas §4.3.
- **#6 RLS / role separation** — migrate runs as `atlas_migrate`
  (`DATABASE_URL[_MIGRATE]`), serving as `atlas_app`; no privilege
  widening, no superuser (P0-473-3 / AC-5).
- **No destructive rollback** — `.down.sql` files are explicitly skipped
  (P0-473-2).

## Anti-criteria check

- P0-473-1 (no serve on partial schema): gate verified in AC-7 (D2/D3).
- P0-473-2 (no auto down-migrations): `*.down.sql` skipped in the loop.
- P0-473-3 (no privilege widening): runs as atlas_migrate, no superuser.
- P0-473-4 (idempotent, no re-seed): AC-7 second run = `schema current`
  no-op, seed counts unchanged.
- P0-473-5 (no silent swallow): FATAL line names the file, exit non-zero.
- P0-473-6 (neutral test fixtures): the AC-7 sentinel + env use only
  `test-*` / neutral values; no vendor-prefixed tokens.
