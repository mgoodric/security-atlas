# 378 — Hot-reload authz bundle without server restart (close slice 332 F-OPA-2)

**Cluster:** Performance / Operability
**Estimate:** 1d
**Type:** AFK (mechanically verifiable)
**Status:** `in-review`

## Narrative

Closes slice 332 finding **F-OPA-2 (HIGH)**. The authz `Engine`
(`internal/authz/decision.go:46–65 NewEngine`) loads the embedded
`policies/authz/*.rego` bundle ONCE at construction. There is no
exposed `Reload()` or `WithPolicy()` method. A policy change today
requires restarting the atlas binary.

For v1 single-tenant operator-restart deployments this is acceptable;
for v2 atlas-edge multi-tenant deployments the restart cost is
unacceptable (slice 023's authz substrate explicitly defers
hot-reload to a follow-up — this is that follow-up).

This slice adds `(*authz.Engine).Reload(ctx context.Context, modules
[]*ast.Module) error` that prepares a new query and atomically swaps
the stored query, AND wires a maintainer-only HTTP endpoint
`POST /v1/admin/authz-bundle/reload` (super_admin-gated) that
triggers the reload from the embedded bundle.

### Why now

Slice 332 H severity escalates this from "deferred indefinitely" to
"H = must fix before v2 atlas-edge ships". Performing this work now
de-risks the v2 multi-tenant rollout.

### Trigger

Slice 332 performance audit, surface 3b (request-time authz),
finding F-OPA-2.

### Disposition

Code change: net new `(*Engine).Reload` method + atomic query-pointer
swap + super_admin-gated HTTP endpoint + integration test.

## Threat model

This is a **high-risk authz-surface change**. STRIDE:

- **S:** No new spoofing surface — the reload endpoint is
  super_admin-gated and runs through the existing OIDC RP +
  super_admin authorization stack.
- **T:** **Load-bearing.** The reload mechanism MUST atomically swap
  the prepared-query — partial swap during a concurrent Decide()
  call could leak the wrong allow/deny decision. Atomic.Pointer
  semantics are the defense.
- **R:** Reload events surface in `atlas_audit_log` with the
  super_admin actor + before/after bundle SHA-256.
- **I:** No information disclosure.
- **D:** **Load-bearing.** A malformed bundle reload that compiles
  to a permissive policy could fail-open. The reload MUST run the
  full slice-026 authz matrix test against the new bundle BEFORE
  atomically swapping; matrix failure aborts the reload.
- **E:** **Load-bearing.** A privilege-escalation path through this
  endpoint is the worst case. Authorization is super_admin-required
  AND the endpoint is rate-limited per-super-admin.

**Constitutional invariants honored**: tenant isolation (#6) is
unchanged — the bundle is global, not tenant-scoped, and the
slice-027 admit-set discipline still applies.

## Acceptance criteria

- [ ] **AC-1.** New method `(*authz.Engine).Reload(ctx
context.Context, modules []*ast.Module) error` prepares a new
      query and atomically swaps the stored query via
      `sync/atomic.Pointer[rego.PreparedEvalQuery]`.
- [ ] **AC-2.** Concurrent `Decide()` calls during a `Reload()` see
      EITHER the old query OR the new one — never a partial swap.
      Asserted by a race-test under `-race`.
- [ ] **AC-3.** `Reload()` runs the slice-026 authz matrix test
      against the new modules BEFORE swapping. Failure to compile
      OR failure of the matrix → return error, do NOT swap.
- [ ] **AC-4.** New HTTP endpoint
      `POST /v1/admin/authz-bundle/reload` (super_admin-gated)
      triggers a reload from the embedded `policies/authz/*.rego`
      bundle.
- [ ] **AC-5.** Endpoint is rate-limited to 1 reload per 60s per
      super_admin.
- [ ] **AC-6.** Audit-log row written on every reload with
      actor_user_id + before-bundle-sha256 + after-bundle-sha256.
- [ ] **AC-7.** Integration test asserts: reload swaps the query;
      Decide() with old role-shape gets old answer pre-reload; same
      Decide() gets new answer post-reload.
- [ ] **AC-8.** Integration test asserts: malformed bundle reload is
      rejected without swap (engine continues to serve old query).
- [ ] **AC-9.** No regression to existing `internal/authz`
      integration tests.
- [ ] **AC-10.** `pre-commit run --files` passes.

## Anti-criteria (P0)

- **P0-1.** Does NOT bypass super_admin authorization on the
  reload endpoint.
- **P0-2.** Does NOT swap the query atomically without running the
  slice-026 matrix first — a permissive bundle reload must not
  reach production.
- **P0-3.** Does NOT introduce a non-atomic swap path (`sync.Mutex`
  with copy-on-read is rejected — the swap MUST be atomic).
- **P0-4.** Does NOT widen the reload surface to allow uploaded /
  user-authored bundles — slice 378 only reloads the embedded
  bundle. Custom tenant bundles are v3+ work (canvas §4.4 custom
  policies). Per-tenant bundles need a separate tenant-scoped
  engine pool, which this slice does NOT introduce.
- **P0-5.** Does NOT auto-merge.

## Dependencies

- **#332** (performance audit) — `merged`. Source finding.
- **#023** (authz substrate) — `merged`. Owner of `internal/authz/`.
- **#026** (authz matrix) — `merged`. The pre-swap validation
  surface.
- **#027** (admit-set discipline) — `merged`. Authoritative admit
  list applied to the new bundle on reload.
- **#142** (super_admin management surface) — `merged`. The actor
  identity layer the endpoint authenticates against.

## Notes for the implementing agent

1. The atomic swap is the load-bearing primitive. Use
   `sync/atomic.Pointer[rego.PreparedEvalQuery]` — Go 1.19+.
2. The pre-swap matrix run MUST use the NEW prepared query, NOT
   the existing one. A common bug shape: run the matrix against the
   currently-loaded engine and then swap to the new one. That
   defeats the point.
3. The audit-log row write happens INSIDE the reload tx so a
   partial reload is invisible in the audit trail (consistent with
   the slice-062 admin audit log discipline).
4. Do NOT widen scope to atlas-edge per-tenant bundles — that's v3+
   work blocked on canvas §4.4 custom-control-policy authoring.
5. The HTTP endpoint should require `Content-Type:
application/json` even though the body is empty, so curl
   `--data ''` is the canonical invocation.
