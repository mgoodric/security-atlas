# 608 — Per-tenant control-bundle upload test-gate policy (`require_bundle_tests_pass`)

**Cluster:** control-as-code
**Estimate:** S (0.5–1d)
**Type:** JUDGMENT (policy resolution + settings surface)
**Status:** `ready` (parent #574 — upload test-gate — merged first)

## Narrative

Slice 574 wired the control-bundle upload test-gate into
`POST /v1/controls:upload-bundle`: a bundle's `tests/` cases run through the
slice-496 runner before persisting, and a failing/errored case hard-blocks the
upload with a per-case report. Slice 574 ships a single **global v0 policy**
(decisions log D-POLICY-1/D-POLICY-2): hard-block when a bundle ships tests and
any case is red; allow-with-warning when a bundle ships no tests. The slice doc
floated a per-tenant `require_bundle_tests_pass` flag as the JUDGMENT axis, and
slice 574 explicitly deferred it (D-POLICY-3) — a per-tenant flag needs a
tenant-settings column (a migration) + a settings surface to set it, which is
disproportionate when the global default is correct for the solo-leader persona.

This slice adds the per-tenant policy so a team can opt into a different posture
than the global default. Two opt-in dimensions surfaced by slice 574:

1. **Advisory mode** — accept a bundle with red tests but attach the report as a
   warning (for tenants authoring iteratively, who want the gate's feedback
   without it blocking the upload).
2. **Mandatory-tests mode** — reject a bundle that ships NO `tests/` (the
   opposite of the global allow-with-warning default), for tenants who want
   every control test-backed.

## Design calls (the JUDGMENT surface)

- **Column shape.** A single `require_bundle_tests_pass BOOLEAN` is the minimal
  shape, but the two dimensions above may want two flags (`gate_mode
ENUM('strict','advisory')` + `require_tests BOOLEAN`). Decide the smallest
  shape that covers the real demand; prefer one flag if one suffices.
- **Migration filename** — after the slice-574 era; the next free
  `20260608030000+` timestamp (slice 574 itself ships no migration).
- **Resolution precedence** — global default applies when the tenant row is
  unset (NULL), so existing tenants keep the slice-574 behaviour with no backfill.
- **Settings surface** — a tenant-admin setting (API + UI). Reuse the existing
  tenant-settings plumbing rather than a bespoke endpoint.

## Acceptance criteria

- [ ] **AC-1.** A per-tenant policy column (or columns) is added via a migration
      timestamped after the slice-574 era, defaulting to the slice-574 global
      behaviour for unset tenants.
- [ ] **AC-2.** The upload gate resolves the effective policy from the tenant row
      (falling back to the global default) before deciding block vs warn.
- [ ] **AC-3.** Advisory mode: a red bundle is accepted with the report attached
      as a warning (not a 400).
- [ ] **AC-4.** Mandatory-tests mode: a bundle with no `tests/` is rejected.
- [ ] **AC-5.** A tenant-admin can read + set the policy via the settings API.
- [ ] **AC-6.** Integration test: the same red bundle is rejected under strict
      and accepted-with-warning under advisory for two different tenants.

## Dependencies

- **#574** (upload test-gate) — provides the gate (`internal/api/controls/gate.go`)
  whose policy this slice makes per-tenant. Merge 574 first.

## Notes

Parent slice: #574. Deferred by slice 574 decisions-log D-POLICY-3
(`docs/audit-log/574-control-bundle-test-upload-gate-decisions.md`). Do NOT edit
`_INDEX.md` or `_STATUS.md`.
