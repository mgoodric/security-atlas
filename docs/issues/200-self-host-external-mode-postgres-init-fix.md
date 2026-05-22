# 200 — Fix self-host bundle external-mode postgres init regression

**Cluster:** Quality / Infra
**Estimate:** 0.5d
**Type:** JUDGMENT (diagnose-heavy; root-cause must be confirmed before fix)
**Status:** `ready` (spillover from slice 196)

## Provenance

Surfaced during slice 196 (PR #465) CI. The `Self-host bundle · end-to-end (external)` matrix job failed at the harness's pre-create step:

```
[test-self-host:external] pre-creating a NON-SUPERUSER atlas_migrate with a password
psql: error: connection to server on socket "/var/run/postgresql/.s.PGSQL.5432" failed:
  FATAL:  database "security_atlas" does not exist
```

Failure URL: https://github.com/mgoodric/security-atlas/actions/runs/26258908869/job/77287818427

Slice 196 did NOT touch `deploy/docker/test-self-host-bundle.sh` or the `postgres` service block in `deploy/docker/docker-compose.yml`. The failure is in the script's external-mode pre-create logic at lines 158-225, which slice 196 left intact.

Recent main runs (e.g. `2b943c1`, `293334c`) show the self-host bundle matrix being SKIPPED via `dorny/paths-filter@v4` — the path-filter has been hiding this failure. Slice 196's path touches (`deploy/docker/*` modifications) caused the path-filter to fire, exposing the latent break.

## Narrative

The external-mode test harness brings up only the `postgres` service first (line 167: `"${COMPOSE[@]}" up -d postgres`), waits for `pg_isready`, then runs:

```
"${COMPOSE[@]}" exec -T postgres psql -U postgres -d security_atlas ...
```

This expects the `security_atlas` database to exist at `pg_isready`-time. The postgres image creates `POSTGRES_DB` at initdb. In external mode the harness writes `POSTGRES_HOST_AUTH_METHOD=` (empty string) to `.env.test`, intending postgres to fall back to `scram-sha-256` per the script's docstring (line 130-131).

Hypothesis: the EMPTY-string value for `POSTGRES_HOST_AUTH_METHOD` causes the postgres entrypoint to skip the initdb step that creates `POSTGRES_DB`, leaving the cluster without the `security_atlas` database. Postgres 16+ may have tightened the behavior vs the version the harness was originally designed against.

Alternative hypothesis: the `PG_INITDB_ROLES=/dev/null` line writes a no-op initdb script that somehow short-circuits POSTGRES_DB creation. (Plausible if the postgres entrypoint's "create DB" step is part of the same flow as "run /docker-entrypoint-initdb.d/\*" scripts.)

Either hypothesis is verifiable by directly running the script locally with debug instrumentation.

## Acceptance criteria

- **AC-1.** Reproduce the failure locally: `cd deploy/docker && ./test-self-host-bundle.sh external`. Capture full failure output to `docs/audit-log/200-external-mode-debug.md`.
- **AC-2.** Identify the actual root cause via debug instrumentation (psql `\l` listing the databases, `docker logs sa-selfhost-external-postgres-1` showing initdb output, etc.). Document in the decisions log.
- **AC-3.** Apply the surgical fix. Two candidate shapes:
  - **(a)** Pre-create `security_atlas` database in the harness before the `psql` call (one extra `createdb` or `CREATE DATABASE` step).
  - **(b)** Stop writing `POSTGRES_HOST_AUTH_METHOD=` to `.env.test`; instead set it to `scram-sha-256` explicitly (which is what the docstring claims happens already).
- **AC-4.** Both modes pass locally: `./test-self-host-bundle.sh bundled` AND `./test-self-host-bundle.sh external`.
- **AC-5.** CI `Self-host bundle · end-to-end (external)` and `Self-host bundle · end-to-end (bundled)` both pass on the slice 200 PR.
- **AC-6.** Decisions log at `docs/audit-log/200-external-mode-postgres-init-decisions.md` captures the diagnosis + fix rationale.

## Anti-criteria (P0 — block merge)

- **P0-200-1.** Does NOT relax the test by skipping the external mode. The external mode codifies the "shared-cluster" deployment shape — relaxing it removes a real verification surface.
- **P0-200-2.** Does NOT promote the self-host bundle job into `.github/branch-protection.json` required contexts. That's slice 116 territory and is independent of this fix.
- **P0-200-3.** Does NOT change `POSTGRES_DB` default in the compose file (the harness writes it to `.env.test` explicitly; that's the right layer for the fix).

## Dependencies

- **#196** (in flight) — surfaced this issue but did not cause it; slice 196 PR can merge UNSTABLE before slice 200 lands.
- **#194** (merged) — path-filter that has been hiding this failure.

## Skill mix (3-4)

- `tdd` (red — reproduce; green — fix; refactor — minimize the script change)
- `grill-with-docs` (the postgres image's exact behavior for empty `POSTGRES_HOST_AUTH_METHOD` is the load-bearing question)
- `ship-gate`

## Notes for the implementing agent

The path-filter has been masking this for unknown weeks. Other recent main runs were "Docs-only change — skipped per dorny/paths-filter@v4." That means the last time external mode actually RAN successfully is somewhere before slice 194's path-filter introduction. Worth a quick `git log -p deploy/docker/test-self-host-bundle.sh` walk to see if anything in the script changed since the last known-good run.

The fix should be MINIMAL — likely 1-3 lines of bash. If the diagnosis points to a postgres-image behavioral change, prefer fixing the harness over pinning the image to an older version.

## Provenance

Filed 2026-05-21 as orchestrator-driven spillover from slice 196 CI. Engineer claimed local pass for both modes but their decisions log honestly notes self-host e2e was "pending verification at PR-open time" — the local run for external mode was never actually performed. Slice 196 itself is unaffected by this issue; it surfaced the latent break by triggering the path-filter for the first time in a while.
