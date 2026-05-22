# 202 — Self-host bundled mode: atlas service starts before migrations finish

**Cluster:** Infra (self-host)
**Estimate:** 0.5d
**Type:** AFK
**Status:** `ready`
**Parent:** spillover from slice 131 (surfaced on PR #484 — pure test-code diff, no production change; failure pre-existed and was masked by path-filter)

## Narrative

The `Self-host bundle · end-to-end (bundled)` CI job fails on PR #484 with `atlas-bootstrap exited 1`. The diff in PR #484 is two-file (`internal/audit/notes/integration_slice029_test.go` + `CHANGELOG.md`) — pure test harness fix; no production code, no migrations, no docker-compose touch. The failure cannot have been caused by slice 131. It is a latent self-host bundled bootstrap race surfaced because the changed-paths filter triggered the self-host job to actually run (path-filter-as-gap-multiplier — same pattern that surfaced slice 196 / 200).

Failure log (PR #484, run 26293268087, job 77399044936):

```
[test-self-host:bundled] FAIL: atlas-bootstrap exited 1, want 0
atlas-bootstrap-1  | [bootstrap]   applying 20260518000000_audit_sink_failures.sql
atlas-bootstrap-1  | error: upload failed: HTTP 400: {"error":"control bundle: evidence_kind \"osquery.host_posture.v1\" is not registered in the schema registry"}
atlas-1  | atlas: schema import: insert 1password.org_policy.v1/1.0.0: ERROR: relation "evidence_kind_schemas" does not exist (SQLSTATE 42P01)
atlas-1  | atlas: schema cache reload attempt 1 failed (retrying): list global schemas: ERROR: relation "evidence_kind_schemas" does not exist (SQLSTATE 42P01)
atlas-1  | atlas: metrics seeder: catalog/metrics: upsert audit_period_currency: ERROR: relation "metrics_catalog" does not exist (SQLSTATE 42P01)
atlas-1  | time=2026-05-22T14:24:12.837Z level=WARN msg="atlas: oauth_auth_codes sweep failed" err="oauthcode: sweep: ERROR: relation \"oauth_auth_codes\" does not exist (SQLSTATE 42P01)"
atlas-1  | time=2026-05-22T14:24:12.838Z level=WARN msg="atlas: oauth_revoked_tokens sweep failed" err="revocation: sweep: ERROR: relation \"oauth_revoked_tokens\" does not exist (SQLSTATE 42P01)"
```

The shape: atlas-bootstrap container is still applying migrations when the atlas service container starts and immediately queries tables (`evidence_kind_schemas`, `metrics_catalog`, `oauth_auth_codes`, `oauth_revoked_tokens`). Bootstrap then tries to upload a control bundle to atlas, but the schema registry has not yet been seeded because the seeder ran into the same missing-table window.

This is distinct from slice 200's race (`pg_isready` returned ready before `docker_setup_db` ran CREATE DATABASE). Slice 200 fixed Postgres init timing. This is a different race: **atlas service depends_on completion-of-migrations, not just postgres-readiness**.

External mode (separate Postgres container) PASSED on the same run — implying the migration-apply timing is acceptable when Postgres is external (probably because the external image's `entrypoint` finishes initdb + accept-connections more deterministically than the bundled image's embedded Postgres path).

## Acceptance criteria

- [ ] AC-1: `deploy/docker/docker-compose.bundled.yml` (or whichever bundled compose file shapes the dep chain): atlas service waits for `atlas-bootstrap` to complete migrations, not just for Postgres healthcheck.
- [ ] AC-2: Wait condition uses one of: (a) `depends_on: atlas-bootstrap: condition: service_completed_successfully`, (b) a healthcheck on atlas that checks for the existence of `evidence_kind_schemas` (sentinel of bootstrap completion), or (c) a CI-time `until` loop in the smoke-test harness that polls for the sentinel before the script proceeds. Pick whichever fits the existing compose+harness shape; record choice in decisions log.
- [ ] AC-3: `Self-host bundle · end-to-end (bundled)` PASSES on this slice's PR.
- [ ] AC-4: `Self-host bundle · end-to-end (external)` STAYS PASSING on this slice's PR (no regression).
- [ ] AC-5: No production Go code touched (compose / CI shape only).
- [ ] AC-6: CHANGELOG entry under unreleased "Fixed".
- [ ] AC-7: Decisions log at `docs/audit-log/202-self-host-bundled-atlas-bootstrap-race-decisions.md` records which of the three approaches was chosen and why.

## Dependencies

None (pure infra timing fix).

## Anti-criteria (P0)

- **P0-A1**: DOES NOT touch any production Go code (`internal/*`, `cmd/*`) — pure compose/CI shape change.
- **P0-A2**: DOES NOT alter migration ordering or migration content.
- **P0-A3**: DOES NOT add a sleep-based wait — pick a deterministic completion signal (service_completed_successfully OR sentinel table existence). Sleeps mask races; they don't fix them.

## Notes

- Pattern continuity: slice 196 → 200 → 197 → 201 → 198 → 142 → 131 → 202. Self-host bundle has now had two distinct race-condition failures (slice 200 fixed first; this slice fixes second). After this slice merges, propose a follow-on observation: are there more self-host bundle race conditions latent? Worth running the bundled smoke locally 5+ consecutive times post-merge to soak.
- File `cmd/scripts/test-self-host.sh` (or equivalent harness location) is likely where the polling option (c) would land.
- Provenance: filed 2026-05-22 as a spillover from slice 131 PR #484 (CI run 26293268087, job 77399044936). PR #484 itself merges UNSTABLE on self-host bundled per the established slice 196 + 197 precedent (Self-host bundle is not currently a required-checks gate per branch-protection).
