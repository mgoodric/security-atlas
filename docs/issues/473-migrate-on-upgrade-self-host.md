# 473 — Idempotent migrate-on-upgrade for the self-host deploy stack (fail-closed)

**Cluster:** Infra / Deploy
**Estimate:** M (1–2d)
**Type:** JUDGMENT (compose-ordering + fail-closed semantics + watchtower interaction)

**Status:** `ready`

> Filed 2026-06-05 from a CONFIRMED PRODUCTION INCIDENT on the maintainer's
> atlas-edge deployment. Diagnosed live (SSH + log archaeology). Relates to
> slice 464 (the SELF_HOSTING migrate-command drift) and slice 432 (the
> upgrade runbook). Parent context: the demo-seed failure investigation.

## Narrative

**Why (the confirmed incident).** The self-host deploy stack applies SQL
migrations only via the `atlas-bootstrap[-edge]` **one-shot first-boot**
container. Two compose facts make that a silent footgun on upgrade:

1. **Watchtower auto-updates the backend, not the migrator.** `atlas-edge`
   (the platform binary) carries the `com.centurylinklabs.watchtower.enable`
   label, so Watchtower pulls newer `:edge` images and restarts it.
   `atlas-bootstrap-edge` (the service that runs migrations) has **no**
   watchtower label and `restart: "no"`. So when the binary moves forward, the
   **migrate step never re-runs** — the new binary starts against the migration
   set from first boot.
2. **The backend doesn't even wait for migrations.** `atlas-edge`'s
   `depends_on` on `atlas-bootstrap-edge` uses `condition: service_started`, not
   `service_completed_successfully` — so nothing gates serving on a completed
   migrate.

**The observed failure (2026-06-05).** The maintainer's atlas-edge box silently
fell **3 migrations behind** after an image update (DB at `20260522020000`,
binary expecting through `20260528`). The demo-seed button then failed with a
masked HTTP 500: the handler's fail-closed `me_audit_log` audit write was
rejected by the `me_audit_log_action_check` CHECK constraint, because the
migration that adds the `demo_seed` action value
(`20260525000000_demo_seed_button_meta_audit`) had never applied. Slice 367's
error-detail hardening (correctly) hid the cause from the browser, so the
operator saw only "action failed." Diagnosis required SSH into the host and
reading the container log. The drift also produced recurring scheduler errors
(distinct, lower-severity — see Non-goals).

**What (the deliverable).** Add an **idempotent migrate step that runs on every
stack bring-up / image update**, applies only un-applied migrations, **gates the
backend from serving until it completes**, and **fails closed** (clear operator
message naming the failing migration; the backend does NOT start against a
partially-migrated schema). The operator updating an image should never again
end up with a newer binary on an older schema.

**Scope discipline.** The docker-compose self-host stack — BOTH the bundled
`atlas-bootstrap` (built) and `atlas-bootstrap-edge` (pulled). The migrate step
reuses the existing filename-tracked `schema_migrations` runner (no change to
the tracking mechanism). This slice does **not** automate down-migrations /
rollback, does **not** change the migration runner, does **not** address the
Helm-chart path (a noted follow-on), and does **not** fix the forward-looking
metrics-evaluator log noise (separate; see Non-goals).

## Threat model

STRIDE pass (design-time). Verdict: **has-mitigations** — the whole slice is a
fail-closed integrity gate.

**S — Spoofing.** N/A — no new authenticated surface; the migrate step runs
inside the deploy network as the existing `atlas_migrate` role.

**T — Tampering (integrity — the core).** A half-applied migration set is a
data-integrity hazard: a binary serving against a partial schema can write rows
that violate the intended (un-applied) constraints, or read/compute against
missing columns. _Mitigation/AC:_ migrations apply atomically-per-file
(existing runner) AND the backend is gated on the migrate step completing
successfully — never serve on a partial schema.

**R — Repudiation.** _Mitigation/AC:_ the migrate step logs which migrations it
applied (and "already current; nothing to apply" when idempotent), so the
upgrade has an audit trail in the container log.

**I — Information disclosure.** N/A — migrate output is schema DDL, no
tenant data; runs server-side.

**D — Denial of service / availability (the second core).** A migrate step that
_blocks_ startup could wedge the deployment — but a SILENT drift that surfaces
as a masked runtime 500 hours later is strictly worse (the actual incident).
_Mitigation/AC:_ the migrate step fails **loudly and early** — a non-zero exit +
a clear log line naming the failing migration + the SQL error — surfacing the
problem at **deploy time**, not as a downstream "action failed." Idempotency
(AC-2) ensures the common up-to-date path is a fast no-op, not a re-run cost.

**E — Elevation of privilege.** _Mitigation/AC:_ the migrate step runs as
`atlas_migrate` (already the case via `DATABASE_URL_MIGRATE`); it does NOT widen
the role's privileges and does NOT run as a superuser.

## Acceptance criteria

- [ ] **AC-1.** The deploy stack applies pending migrations on every bring-up /
      image update — not only on first boot. Concretely: either the
      `atlas-bootstrap[-edge]` migrate step is made watchtower-managed +
      re-runnable, OR a dedicated always-run idempotent `atlas-migrate` service
      is split out from the one-shot seed/SCF/bundle steps.
- [ ] **AC-2.** Idempotent — a bring-up against an already-current DB applies
      nothing and exits success with a clear "schema current" log line (no error,
      no re-seed).
- [ ] **AC-3.** The `atlas-edge` / `atlas` backend `depends_on` the migrate step
      with `condition: service_completed_successfully` (not `service_started`) —
      it does not begin serving until migrations have completed.
- [ ] **AC-4.** Fail-closed: a migration failure makes the migrate step exit
      non-zero with a log line naming the failing migration filename + the SQL
      error, and the backend does NOT start (the `service_completed_successfully`
      gate blocks it). No serving against a partial schema.
- [ ] **AC-5.** The migrate step runs as `atlas_migrate` (existing
      `DATABASE_URL_MIGRATE[_EDGE]`); no privilege widening, no superuser.
- [ ] **AC-6.** Applies to BOTH channels: bundled `atlas-bootstrap` (compose +
      `build:`) and `atlas-bootstrap-edge` (pulled `image:`), keeping their
      first-boot-only steps (seed / SCF import / control-bundle upload) one-shot
      while the migrate step becomes always-run.
- [ ] **AC-7.** CI proof reproducing the incident: bring the stack up at
      migration set N, add a migration, re-up (simulating an image update),
      assert the new migration applied AND the backend served only after. (Mirror
      the slice-202 / self-host-bundle e2e harness.)
- [ ] **AC-8.** `docs/SELF_HOSTING.md` + the slice-432 upgrade runbook document
      the migrate-on-upgrade behavior and the fail-closed contract; reconcile the
      slice-464 migrate-command drift (`atlas migrate up` / `ATLAS_MIGRATE_ON_START`
      docs vs the shipped surface) as part of AC-8.
- [ ] **AC-9.** Watchtower interaction documented: if the migrate service is
      watchtower-managed, the label + ordering is spelled out so an operator
      running Watchtower gets migrate-then-serve, not serve-on-stale.

## Constitutional invariants honored

- **Fail-closed integrity** — the backend never serves against a partial schema
  (mirrors the evidence-engine "evaluation never corrupts the record" spirit;
  canvas §4.3).
- **#6 RLS / role separation** — migrate runs as `atlas_migrate`, serving as
  `atlas_app`; the boundary is unchanged (`internal/db/integration_test.go`
  role model).
- **Honest operator surfaces** — a deploy-time failure is surfaced at deploy
  time with a clear message, not masked as a runtime "action failed" (the
  slice-367 hardening is correct; this slice makes the _deploy_ path honest so
  the masked-500 class can't recur).

## Canvas references

- `Plans/canvas/09-tech-stack.md` — deployment (docker-compose self-host;
  Helm follow-on).
- `Plans/canvas/04-evidence-engine.md` §4.3 — fail-closed / no-corruption
  invariant (the integrity rationale for not serving on a partial schema).

## Dependencies

- Relates to **#464** (`atlas evidence verify` CLI + SELF_HOSTING migrate-cmd
  drift — `ready`): AC-8 reconciles the migrate-command documentation drift 464
  flagged. Not a hard blocker; can land independently or pair.
- Relates to **#432** (merged — backup/restore + upgrade runbooks): AC-8 extends
  the upgrade runbook.
- The migrate logic lives in the `security-atlas-bootstrap` image + the compose
  files under `deploy/docker/`.

## Anti-criteria (P0 — block merge)

- **P0-473-1.** Does NOT start the atlas backend against a partially-migrated DB
  (the `service_completed_successfully` gate is load-bearing).
- **P0-473-2.** Does NOT auto-run down-migrations or any destructive rollback.
- **P0-473-3.** Does NOT widen the `atlas_migrate` role's privileges or run
  migrations as a superuser.
- **P0-473-4.** Idempotent — re-running against a current DB applies nothing and
  does NOT re-seed / re-import demo or SCF data (the one-shot seed steps stay
  one-shot; only the migrate step becomes always-run).
- **P0-473-5.** Does NOT silently swallow a migration failure — it exits
  non-zero with the failing migration named.
- **P0-473-6.** Does NOT use vendor-prefixed test fixture tokens; neutral
  `test-*` only.

## Skill mix (3-5)

- `grill-with-docs` — align on the bootstrap/migrate/seed separation + the
  watchtower interaction
- `database-designer` — confirm the migrate-step role + the schema_migrations
  runner reuse
- `tdd` — the AC-7 reproduce-the-incident CI proof (up at N → add migration →
  re-up → assert applied + serve-after)
- `security-review` — the fail-closed gate + no-privilege-widening
- `ship-gate` — verify the backend genuinely cannot serve on a partial schema

## Notes for the implementing agent

- **The exact bug (confirmed):** `deploy/docker/docker-compose.edge.yml` —
  `atlas-edge` has the watchtower-enable label, `atlas-bootstrap-edge` does not
  and is `restart: "no"`, and `atlas-edge depends_on atlas-bootstrap-edge:
condition: service_started`. So Watchtower updates the binary, the migrate
  one-shot never re-runs, and nothing gates serving on a completed migrate. The
  bundled `docker-compose.yml` (`atlas` / `atlas-bootstrap`) has the same shape —
  fix both.
- **Design choice (the JUDGMENT call):** the cleanest factoring is probably to
  SPLIT a dedicated `atlas-migrate[-edge]` service (always-run, idempotent,
  `service_completed_successfully`-gated) out of the existing
  `atlas-bootstrap[-edge]` one-shot, leaving seed/SCF/bundle as the
  first-boot-only steps. Decide and record in the decisions log; AC-1 allows
  either factoring.
- **Idempotency is already there at the runner level** — `schema_migrations` is
  filename-tracked and the migrations are individually `IF EXISTS`/`IF NOT
EXISTS`-guarded (verified during the incident: applying the 3 pending
  migrations by hand was a clean no-op-on-re-apply). The work is the compose
  ordering + making the step always-run + fail-closed, not a new runner.
- **AC-7 is the load-bearing test** — it reproduces the exact production failure
  mode. Without it, this slice could regress invisibly (the original gap had no
  test catching "binary newer than DB").
- **Registration note (slice-382):** this slice's `_STATUS.md` row is NOT
  registered on this `docs/473` branch — the slice-382 CI guard rejects
  `_STATUS.md` edits from non-`chore/status-batch` branches. The orchestrator
  registers the `ready` row via a `chore/status` action.
