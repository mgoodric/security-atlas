# 613 — Web settings toggle for the per-tenant control-bundle gate policy

**Cluster:** control-as-code
**Estimate:** S (0.5d)
**Type:** code
**Status:** `ready` (parent #608 — the API it drives merged first)

## Narrative

Slice 608 added the per-tenant control-bundle upload gate policy
(`tenants.bundle_gate_mode` ∈ `strict | advisory | mandatory_tests`, default
`strict`) and exposed it on the existing admin-gated `PATCH /v1/tenants/{id}`
endpoint (alongside `name`). A tenant admin can set the policy today via the
API, but there is no web UI control for it — slice 608 deferred the toggle to
keep that slice from ballooning into web/ + Playwright work (608 decisions-log
D5).

This slice adds the Settings control so a tenant admin can choose the gate
policy from the UI.

## Acceptance criteria

- [ ] **AC-1.** The tenant Settings page renders a control (segmented control or
      select) for `bundle_gate_mode` with the three options, pre-selected to the
      tenant's current value (read via the existing tenant GET-shape / the
      `PATCH` response shape).
- [ ] **AC-2.** Changing the control PATCHes `/v1/tenants/{id}` with
      `{bundle_gate_mode}` and reflects the persisted value on success.
- [ ] **AC-3.** Each option carries a one-line explanation of its effect
      (strict = block red tests, allow no-tests; advisory = accept red with a
      warning; mandatory_tests = reject a bundle with no tests).
- [ ] **AC-4.** The control is shown only to a tenant admin / super_admin
      (the same authority that gates the PATCH).

## Notes

- The web e2e spec asserting the server-backed selection MUST route-mock the BFF
  GET (not rely on the shared-DB seed) — see the b219 lesson in `CLAUDE.md`.
- No backend change: the API surface (`PATCH /v1/tenants/{id}` accepting
  `bundle_gate_mode`) already shipped in slice 608.

Parent slice: #608 (`docs/issues/608-per-tenant-bundle-test-gate-policy.md`).
Deferred by slice 608 decisions-log D5
(`docs/audit-log/608-per-tenant-bundle-gate-policy-decisions.md`). Do NOT edit
`_INDEX.md` or `_STATUS.md`.
