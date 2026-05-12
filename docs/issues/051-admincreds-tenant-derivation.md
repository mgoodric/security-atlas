# 051 — admincreds Issue/List derive tenant from credential, not request body

**Cluster:** Multi-tenancy / auth
**Estimate:** 0.5d
**Type:** AFK (P0 follow-up to slice 033)

## Narrative

P0 authorization fix surfaced by slice 033 (`feat(auth): Postgres RLS enforcement on every tenant-scoped table + tenancy middleware (#033)`, gh#27, merged 2026-05-12 as `c534c85`). The handlers `admincreds.Issue` and `admincreds.List` in `internal/api/admincreds/http.go` source the tenant id from caller-supplied input (`IssueRequest.TenantID` body field, `?tenant_id` query parameter) and then explicitly call `tenancy.WithTenant(ctx, requestTenantID)` — overriding the `app.current_tenant` GUC that slice-033's `tenancymw.Middleware` already lifted from `cred.TenantID`.

The handler is internally consistent: it sets the GUC and writes the row under the same attacker-supplied tenant id. RLS therefore does NOT catch this — it sees a tenant-B GUC writing a tenant-B row and waves it through. The result is that a Tenant-A admin can mint an admin credential into Tenant B (Issue) and enumerate credentials in Tenant B (List).

This contradicts slice-033's ratified design decision D1 — *"tenancy.Middleware sets app.current_tenant strictly from cred.TenantID; no handler-level overrides"* — and constitutional invariant 6 (canvas §5.4, tenant isolation enforced at the DB layer; application code is not the trust boundary).

`admincreds.Rotate` and `admincreds.Revoke` already derive tenant from `cred.TenantID` and trust the middleware-set GUC; slice 033 fixed them as part of the cleanup pass and explicitly left Issue + List flagged as a separate authz follow-up. This slice closes that follow-up.

## Threat model

| Actor | Pre-fix capability | Post-fix capability |
| --- | --- | --- |
| Tenant-A admin holding a valid admin bearer | Mint an admin credential into ANY tenant by setting `tenant_id` in the JSON body | Mint an admin credential into Tenant A only — the value of any caller-supplied `tenant_id` is rejected with HTTP 400 |
| Tenant-A admin holding a valid admin bearer | Enumerate any tenant's admin credentials by setting `?tenant_id=` on `GET /v1/admin/credentials` | List Tenant A's admin credentials only |
| Non-admin bearer | 403 (unchanged) | 403 (unchanged) |
| Unauthenticated request | 401 (unchanged) | 401 (unchanged) |

The exploit only requires possession of any admin bearer, not super-admin status. No production tenant has ever had cross-tenant admin issuance enabled; the bug is theoretical for v1 self-host but is a real vulnerability under the multi-tenant SaaS shape called out in canvas §5.4.

## Acceptance criteria

- [ ] AC-1: `Issue` rejects any non-empty `IssueRequest.tenant_id` with HTTP 400 and a descriptive error message
- [ ] AC-2: `Issue` derives the tenant strictly from `authctx.CredentialFromContext(r.Context()).TenantID` and passes that to `apikeystore.Store.Issue`
- [ ] AC-3: `List` rejects any non-empty `?tenant_id` query parameter with HTTP 400 and a descriptive error message
- [ ] AC-4: `List` derives the tenant strictly from the calling credential
- [ ] AC-5: `Rotate` and `Revoke` handler bodies are byte-identical to their pre-slice-051 state (verified via `git diff` produces no hunks inside their function bodies)
- [ ] AC-6: New integration tests `TestIssue_RejectsCrossTenantInRequestBody` and `TestList_DoesNotPermitCrossTenantQuery` assert the 400 + message on both paths
- [ ] AC-7: CHANGELOG announces the breaking API contract change under `## [Unreleased]` so OSS clients discover it on upgrade

## Constitutional invariants honored

- **Invariant 6 (RLS at DB layer):** the fix removes the only application-code path that could escape RLS in admincreds. The middleware is now the sole writer of `app.current_tenant`.
- **Slice 033 design decision D1:** "no handler-level overrides" — enforced.
- **Anti-pattern rejected:** "application code is not the trust boundary" — Issue/List no longer trust caller-supplied tenant ids.

## Canvas references

- `Plans/canvas/05-scopes.md` §5.4 (Postgres RLS named explicitly)
- `CLAUDE.md` Invariant 6
- `internal/api/tenancymw/middleware.go` (docstring: "every other handler MUST inherit from this middleware")

## Dependencies

- #033 (merged) — `tenancymw.Middleware` is the GUC setter the fix depends on
- #034 (merged) — `apikeystore.Store` is what Issue/List call into

## Anti-criteria (P0 — block merge)

- Does NOT touch `Rotate` or `Revoke` handler bodies — they are already correct
- Does NOT add a new `tenancy.WithTenant(ctx, …)` call on the Issue or List paths — the middleware is the sole writer
- Does NOT introduce a "super-admin can cross tenants" code path — there is no `IsSuperAdmin` flag and tenant comes solely from `cred.TenantID`
- Does NOT silently drop the `IssueRequest.TenantID` JSON field — it stays (with `omitempty`) so legacy callers receive a clear 400 instead of a JSON-decode failure or, worse, silent acceptance
- Does NOT use vendor-prefixed tokens in test fixtures (neutral `test-*` only)

## Skill mix (3–5)

- HTTP handler refactor (Go, chi)
- Negative-test discipline (assert a 400 BEFORE any DB call)
- Conventional Commits + breaking-change CHANGELOG hygiene
- Context-derived authorization (`authctx.CredentialFromContext`)
