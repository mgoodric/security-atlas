# Slice 200 — external-mode reproduction & debug log

**Slice:** 200 — fix self-host bundle external-mode postgres init regression
**Author:** Engineer agent (PAI ALGORITHM mode)
**Date:** 2026-05-21

## CI failure signature (the regression we are fixing)

From `https://github.com/mgoodric/security-atlas/actions/runs/26258908869/job/77287818427`
(`Self-host bundle · end-to-end (external)`, on slice 196 PR #465):

```
2026-05-21T23:22:50.7180428Z  Container sa-selfhost-external-postgres-1  Started
2026-05-21T23:22:52.9401492Z [test-self-host:external] pre-creating a NON-SUPERUSER atlas_migrate with a password
2026-05-21T23:22:53.0183057Z psql: error: connection to server on socket "/var/run/postgresql/.s.PGSQL.5432" failed:
                              FATAL:  database "security_atlas" does not exist
2026-05-21T23:22:53.0227866Z [test-self-host:external] tearing down
```

Notice the timing: only **2.23 seconds** between "Container Started" and "pre-creating ..."
appearing — i.e. between the `docker compose up -d postgres` returning and the
`pg_isready` polling loop succeeding on its first iteration.

## Local reproduction attempts

### Run 1 — straight invocation (port collision)

```
$ cd deploy/docker && ./test-self-host-bundle.sh external
[test-self-host:external] external mode: starting postgres alone, scram-sha-256, no trust
 Container sa-selfhost-external-postgres-1 Creating
 Container sa-selfhost-external-postgres-1 Created
 Container sa-selfhost-external-postgres-1 Starting
Error response from daemon: failed to set up container networking: driver failed
programming external connectivity on endpoint sa-selfhost-external-postgres-1: Bind
for 0.0.0.0:5432 failed: port is already allocated
[test-self-host:external] tearing down
```

Local host already had a long-running ssh tunnel on 5432 — irrelevant to the CI
regression. Re-ran with port overrides.

### Run 2 — alternate ports, full external mode

```
$ POSTGRES_PORT=55432 NATS_PORT=54222 ... ./test-self-host-bundle.sh external
[test-self-host:external] writing /Users/gmoney/.../deploy/docker/.env.test
[test-self-host:external] external mode: starting postgres alone, scram-sha-256, no trust
 Container sa-selfhost-external-postgres-1 Started
[test-self-host:external] pre-creating a NON-SUPERUSER atlas_migrate with a password
DO
ALTER ROLE
ALTER SCHEMA
GRANT
[test-self-host:external] docker compose up -d (full bundle)
... (full bundle came up, atlas-bootstrap exited 0) ...
[test-self-host:external] atlas-bootstrap exited 0
[test-self-host:external] checking atlas /health
[test-self-host:external] atlas /health host port is 8080
[test-self-host:external] FAIL: atlas /health never returned 200
```

The `pre-create` step **passed** on local macOS Docker (29.4.3) on Apple Silicon.
The eventual `/health` failure here is a SEPARATE local-only issue (atlas
container has a stale `atlas_app` password from a prior local run's data
volume — not the CI regression). The relevant evidence is that the pre-create
step succeeded locally where it failed on CI — meaning the failure is timing /
race-dependent, not a categorical bug in the script's SQL or env handling.

## Image-behavior probes

### Probe A — `postgres:16-alpine` with empty `POSTGRES_HOST_AUTH_METHOD`

```
$ docker run --rm -d --name pg-probe-empty \
      -e POSTGRES_PASSWORD=test \
      -e POSTGRES_DB=security_atlas \
      -e POSTGRES_HOST_AUTH_METHOD= \
      postgres:16-alpine
$ sleep 8 && docker exec pg-probe-empty psql -U postgres -c '\l'
                                                         List of databases
      Name      |  Owner   | Encoding ...
----------------+----------+----------+ ...
 postgres       | postgres | UTF8     ...
 security_atlas | postgres | UTF8     ...    <-- present
 template0      | postgres | UTF8     ...
 template1      | postgres | UTF8     ...
```

**Verdict:** empty `POSTGRES_HOST_AUTH_METHOD` does NOT short-circuit
`POSTGRES_DB` creation. H1 from the slice doc is **REJECTED**.

### Probe B — same, with `/dev/null` mounted as the only initdb script (exact harness shape)

```
$ docker run --rm -d --name pg-probe-devnull \
      -e POSTGRES_PASSWORD=test \
      -e POSTGRES_DB=security_atlas \
      -e POSTGRES_HOST_AUTH_METHOD= \
      -v /dev/null:/docker-entrypoint-initdb.d/01-roles.sql:ro \
      postgres:16-alpine
... entrypoint logs:
    CREATE DATABASE
    /usr/local/bin/docker-entrypoint.sh: running /docker-entrypoint-initdb.d/01-roles.sql
... `\l` again shows security_atlas present.
```

**Verdict:** `/dev/null` mount does NOT interfere with `POSTGRES_DB` creation.
H2 from the slice doc is **REJECTED**.

## Diagnosis verdict — the real root cause

It is a **race condition** between two things that both happen during the
`postgres:16-alpine` docker-entrypoint:

1. The temp postgres server starts on `/var/run/postgresql/.s.PGSQL.5432` —
   `pg_isready -U postgres` (no `-d`) returns 0 against this socket.
2. `docker_setup_db` runs `CREATE DATABASE "$POSTGRES_DB"` against that temp
   server. This happens AFTER (1).

Window: between (1) and (2), `pg_isready` returns 0 but `security_atlas` does
NOT yet exist.

On the GitHub Actions hosted runner that ran slice 196 PR #465, the script's
first `pg_isready` iteration happened inside this window. The next line in the
script — `psql -U postgres -d security_atlas ... <<SQL` — therefore opened a
connection request asking for DB `security_atlas`, and Postgres rejected it
with `FATAL: database "security_atlas" does not exist`.

On macOS local Docker the same window exists but `initdb` is slow enough that
by the time the script's first `pg_isready` runs (after `up -d postgres`
returns + a `sleep 2` floor), `docker_setup_db` has already finished — the
race is "won" in our favor every time, hiding the bug.

Why this latent bug surfaced only now:

- The `pg_isready` loop has been wrong since slice 065 (the harness was first
  introduced in commit `08404d5`, August 2025).
- Slice 194 introduced `dorny/paths-filter@v4` which skips the self-host
  bundle job on every PR that does not touch `deploy/docker/*`.
- For weeks, every main run was skipped. Slice 196 (the OAuth bootstrap
  migration) touched `deploy/docker/*` indirectly via container env changes,
  the matrix actually RAN, and the race lost.

H1, H2 — both **REJECTED**.

The diagnosis verdict is **"other": a race between `pg_isready` and `docker_setup_db`**.

## Fix shape

The candidate fix shapes in the slice doc (a — pre-create `security_atlas`
explicitly; b — set `POSTGRES_HOST_AUTH_METHOD=scram-sha-256` explicitly) are
both wrong fixes because neither solves the actual race.

The **right** fix is to make the readiness check actually wait for the DB to
exist, not just for the postgres socket to be up. The minimal change:

```diff
-        if "${COMPOSE[@]}" exec -T postgres pg_isready -U postgres >/dev/null 2>&1; then
+        if "${COMPOSE[@]}" exec -T postgres pg_isready -U postgres -d security_atlas >/dev/null 2>&1; then
```

`pg_isready -d <dbname>` connects with `dbname=<dbname>` in its conn-string,
which means postgres has to actually open that database to answer — if
`security_atlas` does not exist yet, the temp server returns the same
`database "security_atlas" does not exist` and `pg_isready` exits non-zero,
so the loop keeps polling until `docker_setup_db` finishes. This collapses
the race window to zero.

(`pg_isready` does NOT authenticate, so the empty
`POSTGRES_HOST_AUTH_METHOD` / scram-sha-256 mode is irrelevant — the readiness
check works regardless of auth method.)

## Verification

Both modes are re-run locally against the fixed script in
`docs/audit-log/200-external-mode-postgres-init-decisions.md`.
