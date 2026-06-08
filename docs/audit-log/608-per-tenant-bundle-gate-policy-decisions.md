# 608 — Per-tenant control-bundle upload gate policy: JUDGMENT decisions log

Slice type: JUDGMENT. The slice doc named the policy-value shape, the default,
the resolution precedence, and the settings surface as the implementing agent's
call. This file records those calls + their rationale. It does NOT block merge —
the maintainer iterates post-deployment from the "Revisit once in use" list.

- detection_tier_actual: none
- detection_tier_target: none

No shipped-behavior defect surfaced during the build. The per-tenant resolution
and the two new policy branches (advisory, mandatory_tests) were written
test-first at the gate-function unit tier and re-proven at the HTTP integration
tier against real Postgres; the default-preserved path is exercised by both the
existing slice-574 strict tests (unchanged) and the new "no tenants row →
strict" test. No bug escaped to a later tier, so `detection_tier_actual = none`.

## Design calls

### D1 — Policy shape: ONE `bundle_gate_mode TEXT` enum, not two boolean flags

- **Options considered:** (a) a single `require_bundle_tests_pass BOOLEAN`
  (the name the slice doc floated); (b) two orthogonal flags
  (`gate_mode ENUM('strict','advisory')` + `require_tests BOOLEAN`); (c) a single
  three-valued enum `bundle_gate_mode IN ('strict','advisory','mandatory_tests')`.
- **Chosen:** (c).
- **Rationale:** the slice surfaced two opt-in dimensions — advisory (don't block
  on red) and mandatory-tests (block on absent tests). (a) cannot express both.
  (b) makes them orthogonal, but the cross-product is partly incoherent: "advisory
  AND mandatory_tests" would mean "don't block on red but DO block on absence",
  a confusing posture nobody asked for, and it forces a 4-way resolution matrix.
  A single three-valued enum is the smallest shape that covers the real demand
  from AC-3 + AC-4 exactly once each, keeps resolution a single switch, and reads
  cleanly in the settings API. The slice doc's own guidance ("prefer one flag if
  one suffices") points here. `advisory` is the only escape hatch from a hard
  block; there is deliberately NO `off`/`disabled` value for v0 — disabling the
  gate entirely would let a provably-wrong control reach the catalog silently
  (the canvas anti-pattern), and `advisory` already gives the non-blocking
  feedback an iterating author wants while still surfacing the red report.

### D2 — Default: `strict` (preserves slice 574)

- **Chosen:** the column DEFAULTs to `'strict'`, and the resolver maps an absent
  tenants row (or any unrecognised stored value) to `strict`.
- **Rationale:** a tenant that does nothing MUST keep the safe slice-574 behaviour
  — that is the whole point of a default. `strict` reproduces slice 574 exactly:
  red tests hard-block (574 D-POLICY-1), a no-tests bundle uploads with a warning
  (574 D-POLICY-2). No backfill is needed: existing tenants (whether they have a
  `tenants` row from slice 144 or are still a bare UUID) resolve to `strict`. The
  resolver's "unrecognised value → strict" fallback is fail-safe-toward-the-default
  (never a looser posture than strict, even if the DB CHECK were somehow bypassed).
  A unit test pins `DefaultGateMode == GateModeStrict` so a future edit cannot
  silently weaken it.

### D3 — Resolution: read tenant policy at upload time, RLS-scoped, read-only

- **Chosen:** the handler resolves the mode once per upload via
  `control.Store.BundleGateMode(ctx)`, which reads `tenants.bundle_gate_mode`
  inside a READ-ONLY tenant transaction (reusing `WithReadOnlyTenantTx`). The
  gate function itself (`runGate`) takes the resolved `GateMode` as a pure input.
- **Rationale:** keeping the DB read in the handler (not inside the gate) keeps
  `runGate` pure and fully unit-testable with no database — the four mode×outcome
  branches are table-tested at the unit tier. The read is RLS-scoped, so tenant A
  cannot read tenant B's policy (proven by `TestGate_TenantIsolation`). The read
  is read-only (invariant #2): resolving the policy never writes. A nil store
  (unit servers) resolves to `strict` so the no-pool path degrades to the safe
  default, never panics.

### D4 — Settings surface: extend the existing `PATCH /v1/tenants/{id}` (slice 144)

- **Options considered:** (a) a bespoke `PUT /v1/tenants/{id}/gate-policy`
  endpoint; (b) the `feature_flags` table (slice 059); (c) extend the existing
  admin-gated `PATCH /v1/tenants/{id}` to also accept `bundle_gate_mode`.
- **Chosen:** (c), with the column living on the `tenants` row.
- **Rationale:** the slice doc says "reuse the existing tenant-settings plumbing
  rather than a bespoke endpoint." `feature_flags` is a BOOLEAN-per-key store keyed
  to a fixed Seed list — a three-valued enum does not fit it. `tenants` is already
  the per-tenant identity row, already under FORCE RLS with the slice-002
  four-policy pattern, and already has an admin/super_admin-gated mutator
  (`PATCH /v1/tenants/{id}`). Adding the column there + accepting it in the
  existing PATCH is the minimum surface: no new route, no new table, no new RLS
  policy. The PATCH now accepts `name`, `bundle_gate_mode`, or both; each mutator
  writes its own me_audit_log row (`tenant_rename` / `tenant_gate_policy_update`),
  so the audit trail stays per-field. An out-of-enum value is a 400 at the handler
  allow-list (`ParseGateMode`) with the DB CHECK as the second leg.

### D5 — Web settings toggle: DEFERRED to spillover slice 613

- **Chosen:** ship the API surface (PATCH field + GET-shape) in this slice; defer
  a dedicated web Settings toggle.
- **Rationale:** the API field is the load-bearing surface (a tenant admin can set
  the policy today via PATCH). A web toggle would balloon the slice into web/ +
  Playwright work for a low-frequency admin setting; per the slice-development
  norm it is filed as spillover `docs/issues/613-web-bundle-gate-policy-toggle.md`
  (parent 608), status blocked-on-nothing (the API it drives is in this slice).

## Scope honored

- Migration `20260608030000_*` (after the slice-574 era), reversible
  (`*.down.sql` drops the column + restores the me_audit_log action CHECK to the
  slice-478 baseline), and idempotent (`ADD COLUMN IF NOT EXISTS` /
  `DROP CONSTRAINT IF EXISTS`). Verified applies + reverses + re-applies on a
  fresh Postgres via the self-host-style ordered glob.
- One sqlc query pair added; dbx regenerated with the pinned v1.31.1 — no drift.
- No new floored package (touched packages already floored); no new integration
  package (controls/tenants already enrolled).
- No `_INDEX.md` / `_STATUS.md` edits (slice 382).

## Revisit once in use

- **`off`/`disabled` mode** — deliberately omitted for v0 (advisory is the escape
  hatch). Add only if an operator genuinely needs the gate fully off.
- **Per-implementation_type mandatory tests** — slice 574 floated requiring tests
  specifically for `automated` controls; now that a per-tenant enum exists, a
  finer per-type policy is a natural refinement.
- **Web settings toggle** — slice 613.
