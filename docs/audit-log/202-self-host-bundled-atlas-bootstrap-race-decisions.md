# Slice 202 — self-host bundled atlas/bootstrap race — decisions log

**Slice:** 202 — self-host bundled mode: atlas service starts before migrations finish
**Date:** 2026-05-22
**Author:** Engineer agent (PAI ALGORITHM mode)
**Type:** AFK (deterministic CI timing fix; pure compose/harness, no Go)

## Decision summary

The atlas server's boot-time schema importer (`cmd/atlas/main.go`
~L640, `ImportPlatformSchemas`) runs ONCE without retry. If atlas starts
before `atlas-bootstrap` phase-2 forward migrations have created the
`evidence_kind_schemas` relation, the importer fails with
`relation "evidence_kind_schemas" does not exist (SQLSTATE 42P01)`,
rows are NEVER inserted, the cache-reload retry loop right after the
importer can keep retrying but only re-READS — it does NOT re-IMPORT —
so the schema registry stays empty. Phase 6 of `atlas-bootstrap` then
400s on every `controls upload` call with
`evidence_kind ... is not registered in the schema registry`,
and the bootstrap container exits non-zero.

The fix: stage the smoke-test harness bring-up so the bundled compose
`up -d` happens in two passes. Pass 1 brings up postgres +
atlas-bootstrap and waits for the sentinel relation
`evidence_kind_schemas` to exist (proof phase-2 migrations have created
it). Pass 2 brings up the rest (atlas + web). Atlas's importer then
runs against a fully-migrated DB and succeeds; bootstrap phase 5-6 (in
its existing /health wait-loop) wakes when atlas comes up and uploads
control bundles cleanly.

## D1 — Approach picked

**Picked: option (c) — CI-time harness polling on the sentinel
relation before bringing up atlas.**

Per the spec (`docs/issues/202-self-host-bundled-atlas-bootstrap-race.md`
AC-2) the three candidate shapes were:

- **(a)** `depends_on: atlas-bootstrap: condition: service_completed_successfully`
  on the atlas service.
- **(b)** A healthcheck on atlas (or atlas-bootstrap) that checks for
  the sentinel relation.
- **(c)** A CI-time `until` loop in the smoke-test harness that polls
  for the sentinel before proceeding.

Reasoning:

1. **(a) is a documented deadlock.** The bundled compose file's atlas
   service already comments this out explicitly
   (`deploy/docker/docker-compose.yml` L243-271):

   > This was `condition: service_completed_successfully`, which is a
   > deadlock: atlas would not start until atlas-bootstrap EXITED, but
   > atlas-bootstrap phase 5 BLOCKS waiting for atlas's /health to
   > return 200 (and phase 6 then uploads control bundles to the live
   > server). Neither container could make progress; atlas-bootstrap
   > timed out at 90 attempts, exited 1, and atlas never started.

   The current shape uses `service_started` and a 120s `start_period`
   on atlas's healthcheck so atlas boots in parallel with bootstrap's
   phase 1-4. Reverting to `service_completed_successfully` would
   re-introduce the deadlock that prior decision explicitly rejected.

2. **(b) is invasive and risks an existing-architecture rewrite.** A
   sentinel healthcheck on atlas would require either (i) adding a
   new `/ready` endpoint to atlas (Go change — forbidden by P0-A1) or
   (ii) wiring `pg_isready`/`psql` into the distroless atlas image
   (compose-only but a meaningful image change). A sentinel
   healthcheck on atlas-bootstrap would mean turning the one-shot
   container into a longer-lived process — a much bigger architectural
   change than the bug warrants.

3. **(c) is the minimal-change shape that matches the existing
   slice-200 precedent.** Slice 200 fixed a different race in the same
   harness (`pg_isready -d security_atlas` to gate on
   `docker_setup_db` completion). The same shape — poll the OUTPUT of
   the racing phase from the harness, then proceed — applies cleanly
   here. The production compose file is unchanged; the change is pure
   CI test-infrastructure.

The fix lands in `deploy/docker/test-self-host-bundle.sh` between the
"Bring up the full bundle" comment block and the existing
`atlas-bootstrap exit` assertion. Two new comment blocks document
intent (slice 202 background + deterministic-signal rationale).

## D2 — CI-delta scan

**Path filter (`.github/workflows/ci.yml` `changes.code`):** the filter
includes `'deploy/**'` (line 106). This slice touches
`deploy/docker/test-self-host-bundle.sh` and (below) `CHANGELOG.md` +
`docs/audit-log/202-...md`. The `deploy/**` pattern triggers
`code: true`, which is the gate for the `test-self-host-bundle` job
itself running. The new self-host run on this slice's PR is therefore
guaranteed to execute (not stub-skip) — the change can be validated by
the very job it modifies.

**`changes.code` is `false`-gated for stub job:** when `code` is
`false`, `test-self-host-bundle-stub` posts pass under the same
check name; this is unchanged.

**No new env-var requirements:** the harness fix uses only `psql` /
`docker compose exec -T postgres` / `docker inspect`, all of which are
already used elsewhere in the same script and need no new GitHub
Actions inputs, secrets, or job-level env wiring.

**No matrix changes:** the `bundled` / `external` matrix continues to
run both modes. Both modes execute the staged bring-up — the bundled
path because the bug is there, the external path because the same
race exists latently and the uniform shape is safer than two
divergent code paths in one harness.

**Local-verify status:** Docker on the engineer's workstation is not
spun up for this run; the smoke test is not invoked locally. Mitigation
— relying on the CI matrix run on this slice's PR as the verification.
This matches the slice-200 verification pattern (slice 200's decisions
log also notes "engineer claimed local pass for both modes but their
decisions log honestly notes self-host e2e was 'pending verification
at PR-open time'"). Shellcheck passes clean on the modified harness.

**Honest CI-delta nuance:** the staged-bring-up adds non-trivial
sequencing that the existing CI never exercised. The bundled-mode CI
run on this slice's PR is the load-bearing verification. If it fails
to converge for an unrelated reason (e.g. cold-start migration apply
exceeds the 4-minute poll ceiling on the GitHub Actions runner shape),
we tune the ceiling and re-run rather than reverting the approach —
the underlying race is real and worth the harness complexity.

## D3 — Why not also probe phase-6 readiness (rows in evidence_kind_schemas)?

The cleanest additional sentinel would be `SELECT count(*) FROM
evidence_kind_schemas > 0`, proving atlas's importer ACTUALLY
INSERTED rows. But that is downstream of the fix: with atlas not yet
brought up in stage 1, the importer hasn't run yet — there are no rows
to find. The relation-existence sentinel proves the prerequisite for
the importer's success (target table exists); atlas's importer then
runs in stage 2 against the migrated schema and succeeds. The existing
`atlas-bootstrap exited 0` assertion (line ~260 of the harness)
implicitly verifies the importer succeeded — if rows were missing,
phase 6 would still 400 and bootstrap would still exit non-zero.

We could add an explicit `evidence_kind_schemas` row-count assertion
in a follow-on (would catch any FUTURE regression where the importer
silently inserts 0 rows), but it is outside this slice's scope.
Captured as a possible spillover.

## D4 — Why bail-early on bootstrap exit non-zero in the poll loop?

The new poll loop watches for the sentinel relation. If atlas-bootstrap
fails for an unrelated reason during stage 1 (e.g. a malformed
migration file unrelated to the race), the relation will never
appear and the harness would spin for the full 4-minute ceiling
before failing with an unhelpful "evidence_kind_schemas not created"
message. The bail-early check on `atlas-bootstrap` container
ExitCode != 0 surfaces the real failure immediately with the proper
exit-code-and-logs path. Same defensive shape the rest of the harness
already uses.

## Files touched

- `deploy/docker/test-self-host-bundle.sh` — staged bring-up + sentinel
  poll between stage 1 and stage 2; bail-early on bootstrap non-zero
  exit during stage 1. (~80 lines added; 4 lines removed.)
- `docs/audit-log/202-self-host-bundled-atlas-bootstrap-race-decisions.md` —
  this file.
- `CHANGELOG.md` — Fixed entry under unreleased.

**Not touched (and intentionally so per P0):**

- No `internal/**` or `cmd/**` Go changes (P0-A1).
- No `migrations/**` changes (P0-A2).
- No `deploy/docker/docker-compose.yml` changes — production compose
  is unchanged.
- No `deploy/docker/bootstrap/bootstrap.sh` changes — the bootstrap
  flow itself works correctly; the race is in the smoke-test
  bring-up ordering, not in bootstrap.

## Spillover

None filed during this slice. A possible future slice — explicit
`evidence_kind_schemas` row-count assertion in the harness — is noted
in D3 above but not filed; it is defensive scaffolding for a hazard
the current fix already eliminates by ordering, not by assertion.
