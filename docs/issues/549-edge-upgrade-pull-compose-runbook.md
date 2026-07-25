# 549 — Edge/self-host upgrade runbook: "pull the compose, not just the image" (migration-drift prevention)

**Cluster:** Docs
**Estimate:** S (0.5d)
**Type:** AFK (operator-runbook documentation; no runtime behavior change)

**Status:** `ready`

> Filed 2026-06-07 from a live operator incident (the SECOND of this class).
> A maintainer hit a masked HTTP 500 assigning a user to a tenant on the edge
> box; root cause was migration drift — Watchtower had advanced the `atlas-edge`
> binary but the box's `docker-compose.edge.yml` predated slice 473, so it had
> NO labeled `atlas-migrate-edge` service to advance the schema. The binary ran
> against a 5-migrations-behind schema → `me_audit_log_action_check` CHECK
> violation. The first occurrence was the 2026-06-05 demo-seed 500 (named in the
> `docker-compose.edge.yml` Watchtower comment). The code fix (slice 473's
> labeled migrate service) is correct and shipped — the gap is that the runbook
> does not tell the operator they must **pull the updated compose**, not just let
> Watchtower update images, for that fix to take effect.

## Narrative

**Why (the recurring trap).** Slice 473 made `atlas-migrate-edge` a
Watchtower-labeled, always-run, fail-closed migrate service that the `atlas-edge`
backend is gated on (`service_completed_successfully`) — so a binary update
always re-runs migrations before serving. That mechanism lives in the current
`deploy/docker/docker-compose.edge.yml`. **But Watchtower updates container
_images_, not the compose _file_.** An operator who set up the edge box before
slice 473 (or who only relies on Watchtower) is running a compose that has NO
`atlas-migrate-edge` service — so Watchtower advances the binary while migrations
only ever ran once, at first-boot, via `atlas-bootstrap-edge`. Every image update
that includes a new migration then drifts the schema, and the next code path that
uses the new column/constraint/table fails with a masked 500 (a CHECK violation,
a missing relation/column). This has now happened twice (2026-06-05 demo-seed;
2026-06-07 user↔tenant assign). `docs/SELF_HOSTING.md` even states "Migrations
apply automatically on every bring-up" — which is only true once the box is on a
slice-473-or-later compose; the doc does not warn that a Watchtower-image-only
upgrade does not satisfy that precondition.

**What (the deliverable).** A short, explicit **upgrade-discipline** section in
the operator-facing docs that:

1. **States the rule:** upgrading the edge/self-host stack means pulling the
   updated `docker-compose.edge.yml` (+ `.env.example` deltas) and re-running
   `docker compose -f docker-compose.edge.yml up -d` — NOT just letting Watchtower
   pull new images. Image-only updates do not change the compose topology, so a
   newly-added service (like slice 473's `atlas-migrate-edge`) never appears.
2. **Explains why:** Watchtower advances labeled _images_; the _compose file_
   (services, labels, gating, volumes, env) is operator-managed. New
   services/labels/migrations land only when the operator pulls the file.
3. **Names the symptom + recovery:** a masked HTTP 500 shortly after an image
   bump, whose backend log shows a CHECK-constraint violation
   (`..._action_check`, SQLSTATE 23514) or a missing relation/column
   (42P01/42703), means migration drift. Recovery: pull the latest compose and
   `up -d` (the now-present `atlas-migrate-edge` applies the gap), OR — as a
   stop-gap — apply the pending `migrations/sql/*.sql` files in `schema_migrations`
   filename order via `psql` and record each in the `schema_migrations` ledger.
4. **Cross-links** the slice-473 mechanism (the labeled migrate service + the
   `service_completed_successfully` gate) so the operator understands the
   precondition, and the slice-432 backup/restore runbook (take a checkpoint
   before upgrading).

**Scope discipline.** Operator-runbook documentation ONLY. It does NOT change the
compose, the migrate service, or the Watchtower setup (slice 473 owns those and
they are correct). It does NOT add a new code path. It does NOT rewrite the
slice-432 backup runbook or the slice-373 BCP/DR plan — it cross-links them.
**Follow-on (out of scope, note for the maintainer):** a code/infra slice could
make the compose itself version-checked (e.g. a startup assertion that the
running compose includes `atlas-migrate-edge`, warning loudly if absent) — that
is a separate slice, not this doc.

## Threat model

STRIDE — verdict **N/A-to-low (documentation-only)**. This slice changes prose in
`docs/SELF_HOSTING.md` + `docs/operations/edge-deploy.md`; it ships no code, no
endpoint, no schema change. The one adjacent consideration: the incident this
documents is an **availability/correctness** issue (a stale schema serving 500s,
and — worse — the risk an operator hand-applies migrations incorrectly or skips
the pre-upgrade backup). The runbook MUST therefore (a) tell the operator to take
a slice-432 backup checkpoint BEFORE any manual migration step, and (b) give the
ledger-recording step so a hand-applied migration does not later get
double-applied by the migrate service. No tenant data is exposed by the doc; no
auth/RLS surface is touched.

- **Tampering / availability (the only real axis):** an under-specified recovery
  step could lead an operator to apply migrations out of order or without
  recording the ledger, risking a half-migrated schema. _Mitigation:_ the
  recovery section prescribes filename-order apply + `schema_migrations`
  ledger insert + a pre-step backup, and prefers the supported "pull compose +
  up -d" path over manual psql.

## Acceptance criteria

- [ ] **AC-1.** `docs/SELF_HOSTING.md` gains (or its existing Watchtower/upgrade
      section is amended with) an explicit "Upgrading: pull the compose, not just
      the image" subsection stating the rule + the one-time step to adopt the
      slice-473 `atlas-migrate-edge` service on a pre-473 box.
- [ ] **AC-2.** The same guidance lands in `docs/operations/edge-deploy.md` (the
      edge-deploy operator runbook the compose comment points to), consistent
      with AC-1 (no contradictory copy).
- [ ] **AC-3.** The docs name the **symptom** (post-image-bump HTTP 500;
      backend log shows a `..._action_check` CHECK violation SQLSTATE 23514, or a
      missing relation/column 42P01/42703) and attribute it to migration drift.
- [ ] **AC-4.** The docs give the **recovery**: (a) preferred — pull the latest
      `docker-compose.edge.yml` + `docker compose ... up -d` (the labeled
      `atlas-migrate-edge` applies the gap); (b) stop-gap — take a backup first
      (cross-link slice 432), then apply pending `migrations/sql/*.sql` in
      filename order via `psql` and INSERT each into the `schema_migrations`
      ledger.
- [ ] **AC-5.** The docs cross-link the slice-473 mechanism (labeled migrate
      service + `service_completed_successfully` gate) and the slice-432 backup
      runbook; no duplication of those.
- [ ] **AC-6.** `mkdocs build --strict` passes (if the edited files are in the
      docs-site nav) and all internal links resolve; a CHANGELOG entry for the
      slice.

## Constitutional invariants honored

- **Self-host target (canvas §10.1).** The guidance targets the docker-compose
  single-VM self-host/edge operator — the v1 primary.
- **Honest operations.** Documents the real precondition for "migrations apply
  automatically" rather than leaving the operator to discover the trap at a 500.

## Canvas references

- `docs/SELF_HOSTING.md` (Watchtower / upgrade / backups sections) — the primary
  edit target.
- `deploy/docker/docker-compose.edge.yml` (the slice-473 Watchtower comment block
  - `atlas-migrate-edge` service) — the mechanism being documented.
- `docs/issues/473-migrate-on-upgrade-self-host.md` (the migrate-service fix),
  `docs/issues/432-backup-restore-upgrade-runbooks.md` (the backup runbook to
  cross-link).

## Dependencies

- **#473** (migrate-on-upgrade self-host) — `merged`. The mechanism this runbook
  documents the precondition for.
- **#432** (backup/restore/upgrade runbooks) — `merged`. Cross-linked for the
  pre-upgrade checkpoint.
- No unmerged technical dependency → `ready`.

## Anti-criteria (P0 — block merge)

- **P0-549-1.** Does NOT change the compose, the migrate service, or Watchtower
  config (slice 473 owns those) — docs only.
- **P0-549-2.** Does NOT rewrite the slice-432 backup runbook or the slice-373
  BCP/DR plan — cross-link only.
- **P0-549-3.** The recovery section does NOT omit the pre-step backup
  (slice-432 checkpoint) before any manual migration, and does NOT omit the
  `schema_migrations` ledger-record step (so a hand-applied migration is not
  later double-applied).
- **P0-549-4.** Does NOT contradict the existing `docs/SELF_HOSTING.md` migrate
  copy — it amends/clarifies it.

## Skill mix (3-5)

- `grill-with-docs` — reconcile the existing SELF_HOSTING.md + edge-deploy.md
  upgrade copy with the slice-473 mechanism; find the exact contradictory line.
- `simplify` — keep it a short, scannable upgrade-discipline subsection, not a
  wall of text.
- `ship-gate` — `mkdocs build --strict` + link check.

## Notes for the implementing agent

- The root incident (2026-06-07): Watchtower advanced `security-atlas-edge-atlas-edge-1`
  to the post-478 image, but the box's compose had no `atlas-migrate-edge`
  service, so the 5 migrations 464/492/498/478/445 never applied →
  `me_audit_log_action_check` lacked `user_tenant_assign` → 500 on
  `POST /v1/admin/users/assign`. The orchestrator hand-applied the 5 migrations +
  recorded the ledger to recover. This runbook prevents the recurrence by making
  the "pull the compose" step explicit.
- The `docker-compose.edge.yml` Watchtower comment (lines ~48-70) already
  explains the mechanism + names the 2026-06-05 first occurrence — mine that
  comment for accurate copy; surface the operator-facing version of it in
  SELF_HOSTING.md + edge-deploy.md.
- Confirm `docs/operations/edge-deploy.md` exists (the compose comment references
  it); if it does not, the AC-2 target is whichever edge operator runbook does
  (grep `docs/` for the edge-deploy / Watchtower runbook) — record the chosen
  file in the decisions log.
- **Registration note (slice-382):** `_STATUS.md` row registered by the
  orchestrator on a `chore/status` branch, not this `docs/549` branch.
