# Slice 200 — external-mode postgres init regression — decisions log

**Slice:** 200 — fix self-host bundle external-mode postgres init regression
**Date:** 2026-05-21
**Author:** Engineer agent (PAI ALGORITHM mode)
**Type:** JUDGMENT (diagnose-heavy)

## Decision summary

The harness's external-mode readiness check polls `pg_isready -U postgres`
(no `-d <dbname>`), which returns ready against the postgres image's
**temporary internal postgres server** before the docker-entrypoint script
has run `CREATE DATABASE "$POSTGRES_DB"`. The next harness step then opens a
psql connection against the (not-yet-existing) `security_atlas` database and
fails with `FATAL: database "security_atlas" does not exist`.

The fix is a one-line bash change: point `pg_isready` at the target database
so it only succeeds once the database actually exists.

## Diagnosis verdict

**Neither H1 (empty `POSTGRES_HOST_AUTH_METHOD`) nor H2 (`PG_INITDB_ROLES=/dev/null`)
is the root cause.** See `200-external-mode-debug.md` for the probe evidence.

The root cause is a **race condition** between two phases of `postgres:16-alpine`'s
docker-entrypoint.sh:

1. `docker_temp_server_start` — starts a postgres listening on
   `/var/run/postgresql/.s.PGSQL.5432` (the in-container Unix socket).
   `pg_isready -U postgres` succeeds against this server immediately.
2. `docker_setup_db` — runs `CREATE DATABASE "$POSTGRES_DB"` against the
   temp server. Happens **AFTER** step 1.

Between (1) and (2), the harness's `pg_isready` polling loop sees the
"postgres is ready" answer and exits. The very next harness step
(`psql -d security_atlas -v ON_ERROR_STOP=1 <<SQL`) then fails because
`security_atlas` has not yet been created by step 2.

On macOS local Docker (Apple Silicon), `initdb` is slow enough relative to
the harness's polling cadence that the race is always "won in the harness's
favor" — `pg_isready` returns ready only after step 2 has finished, and the
script proceeds happily. On the GitHub Actions hosted runner (x86_64 Linux,
faster CPU + warm filesystem cache), `initdb` is fast enough that the
polling loop hits the "step-1-done, step-2-not-yet-done" window.

This race has been latent since slice 065 (the harness was first introduced
in August 2025 in commit `08404d5`). It was masked by `dorny/paths-filter@v4`
(slice 194), which has been skipping the self-host bundle matrix on every PR
that does not touch `deploy/docker/*`. Slice 196's container env changes
re-triggered the matrix for the first time in weeks, and that single CI run
lost the race.

## Fix shape — chosen

Neither of the candidate shapes documented in the slice doc is the right
fix:

- **(a)** "Pre-create `security_atlas` in the harness before the psql call" —
  doesn't address the race; the new `createdb` call would face the same
  race window because `pg_isready` would still be lying about readiness.
- **(b)** "Set `POSTGRES_HOST_AUTH_METHOD=scram-sha-256` explicitly instead
  of empty" — fixes nothing because `pg_isready` does not authenticate.

The chosen fix is the **correct readiness predicate**:

```diff
- if "${COMPOSE[@]}" exec -T postgres pg_isready -U postgres >/dev/null 2>&1; then
+ if "${COMPOSE[@]}" exec -T postgres pg_isready -U postgres -d security_atlas >/dev/null 2>&1; then
```

`pg_isready -d <dbname>` includes `dbname=<dbname>` in the conn-string it
sends to postgres. The temp server validates the database name in its
startup-message handling and answers "not_ready" (specifically, the same
"database does not exist" condition) until `docker_setup_db` finishes. The
race window collapses to zero because the readiness predicate now waits for
exactly the precondition the next step needs.

The fail message is also updated to name the specific failure mode so the
NEXT contributor diagnosing this knows what to look for: `postgres did not
become ready (security_atlas DB never created)`.

## Why this fix and not alternative (a)

Alternative (a) — `createdb security_atlas` from the harness before the
psql call — has two problems:

1. The race still applies. `createdb` is a thin wrapper around
   `CREATE DATABASE` that connects via the same Unix socket. It hits the
   same window as `pg_isready -d security_atlas` does; it would just push
   the failure into a different line of the script.
2. The compose file already specifies `POSTGRES_DB=security_atlas` as the
   canonical source. Pre-creating it in the harness duplicates that source
   of truth — `docker_setup_db` would then try to create a DB that already
   exists, which is harmless but odd. Better to let the compose env do its
   job and just wait for it correctly.

## Why this fix and not alternative (b)

Alternative (b) — `POSTGRES_HOST_AUTH_METHOD=scram-sha-256` instead of empty
— fixes a real-but-different problem (config implicitness) but does not
address the race. `pg_isready` does not auth, so the auth method is
irrelevant to the readiness predicate.

We may want to make `POSTGRES_HOST_AUTH_METHOD` explicit in a separate
slice for clarity, but doing it here would conflate two changes and dilute
the diagnosis.

## P0 anti-criteria compliance

- **P0-200-1 (does not skip external mode)** — PASS. The external mode
  still runs; only the readiness check was tightened.
- **P0-200-2 (does not promote into branch-protection required contexts)**
  — PASS. `.github/branch-protection.json` is not touched in this slice.
- **P0-200-3 (does not change `POSTGRES_DB` default in compose)** — PASS.
  `deploy/docker/docker-compose.yml` is not touched in this slice.

## Local verification (before PR open)

Both modes ran end-to-end on the slice 200 branch locally:

### external mode (with the fix)

```
$ POSTGRES_PORT=55432 NATS_PORT=54222 NATS_MONITOR_PORT=58222 \
  MINIO_PORT=59000 MINIO_CONSOLE_PORT=59001 \
  ATLAS_HTTP_PORT=58080 ATLAS_GRPC_PORT=58085 WEB_PORT=53000 \
  ./test-self-host-bundle.sh external

[test-self-host:external] writing .../deploy/docker/.env.test
[test-self-host:external] external mode: starting postgres alone, scram-sha-256, no trust
[test-self-host:external] pre-creating a NON-SUPERUSER atlas_migrate with a password
[test-self-host:external] docker compose up -d (full bundle)
[test-self-host:external] waiting for atlas-bootstrap to exit
[test-self-host:external] atlas-bootstrap exited 0
[test-self-host:external] checking atlas /health
[test-self-host:external] atlas /health host port is 58080
[test-self-host:external] atlas /health is 200
[test-self-host:external] controls table has 50 rows
[test-self-host:external] decision_audit_log table has 50 row(s)
[test-self-host:external] re-running atlas-bootstrap (idempotency check)
[test-self-host:external] atlas-bootstrap re-run exited 0 with identical row counts (idempotent)
[test-self-host:external] ALL ASSERTIONS PASSED (external)
[test-self-host:external] tearing down
```

Exit code 0. Full log captured at `/tmp/slice200-debug/external-fixed-v2.log`.

### bundled mode (regression check)

```
$ POSTGRES_PORT=55432 NATS_PORT=54222 NATS_MONITOR_PORT=58222 \
  MINIO_PORT=59000 MINIO_CONSOLE_PORT=59001 \
  ATLAS_HTTP_PORT=58080 ATLAS_GRPC_PORT=58085 WEB_PORT=53000 \
  ./test-self-host-bundle.sh bundled

[test-self-host:bundled] writing .../deploy/docker/.env.test
[test-self-host:bundled] docker compose up -d (full bundle)
[test-self-host:bundled] waiting for atlas-bootstrap to exit
[test-self-host:bundled] atlas-bootstrap exited 0
[test-self-host:bundled] checking atlas /health
[test-self-host:bundled] atlas /health host port is 58080
[test-self-host:bundled] atlas /health is 200
[test-self-host:bundled] controls table has 50 rows
[test-self-host:bundled] decision_audit_log table has 50 row(s)
[test-self-host:bundled] re-running atlas-bootstrap (idempotency check)
[test-self-host:bundled] atlas-bootstrap re-run exited 0 with identical row counts (idempotent)
[test-self-host:bundled] ALL ASSERTIONS PASSED (bundled)
[test-self-host:bundled] tearing down
```

Exit code 0. Full log captured at `/tmp/slice200-debug/bundled-fixed.log`.

(The port overrides are local-only because this development host has long-running
ssh tunnels on 5432, 8080 and 9000. CI runners have those ports free and the
script's default compose-derived ports work without overrides.)

## Spillovers

None filed. The change is the minimum-viable fix for the named regression.

A separate, lower-priority cleanup could make `POSTGRES_HOST_AUTH_METHOD`
explicit in external mode (alternative (b) above) but that is style and not
correctness; not worth its own slice unless a future contributor finds the
implicit empty-to-scram-sha-256 fallback confusing.
