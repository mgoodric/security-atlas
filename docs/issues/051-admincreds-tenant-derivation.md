# 051 — admincreds tenant derivation fix (P0 follow-up from slice 033)

**Cluster:** Multi-tenancy / auth
**Estimate:** 0.5d
**Type:** AFK
**Severity:** P0 (cross-tenant escalation)

## Narrative

Fix the pre-existing authorization bug surfaced by slice 033: `admincreds.Issue` and `admincreds.List` read `tenant_id` from request body / query rather than from the calling credential. A tenant-A admin can mint a tenant-B credential today. RLS does NOT catch this because the handler explicitly calls `tenancy.WithTenant(ctx, req.TenantID)` (line 70 of `internal/api/admincreds/http.go`), overriding 033's `tenancy.Middleware` — the row gets inserted under tenant B's GUC because the handler set it there.

After this fix, tenant is derived strictly from `cred.TenantID` (the credential `requireAdmin` already extracts at line 245). Slice 033's middleware shape becomes load-bearing: the calling credential is the single source of truth for tenant context. Symmetric with the already-correct `Rotate` (line 180) and `Revoke` (line 217) handlers which both use `authctx.CredentialFromContext` + `cred.TenantID`.

The slice delivers value because the multi-tenant guarantee — the entire premise of slice 033 — is restored to genuinely-enforced. Until this lands, the platform's multi-tenancy is on paper only at the admin-credentials boundary.

## Acceptance criteria

- [ ] AC-1: `IssueRequest.TenantID` field removed from the JSON shape; clients that supply it get `400 Bad Request: tenant_id is not accepted; tenant is derived from credential`
- [ ] AC-2: `admincreds.Issue` calls `h.store.Issue(ctx, cred.TenantID, ...)` instead of `req.TenantID`
- [ ] AC-3: `admincreds.List` rejects the `?tenant_id=` query parameter the same way (`400` if supplied)
- [ ] AC-4: `admincreds.List` returns only credentials belonging to `cred.TenantID`
- [ ] AC-5: New integration test `TestIssue_RejectsCrossTenantInRequestBody` proves that an admin credential for tenant A who attempts to issue a credential with `req.TenantID == "<tenant-B-uuid>"` gets a 400 (not a 201 + cross-tenant row)
- [ ] AC-6: New integration test `TestList_DoesNotPermitCrossTenantQuery` proves that an admin credential for tenant A calling `?tenant_id=<tenant-B-uuid>` gets a 400 (not a row dump from tenant B)
- [ ] AC-7: Existing tests for `Rotate`, `Revoke`, the happy-path `Issue` (with no `tenant_id` in body), and the happy-path `List` (with no `tenant_id` query) all continue to pass

## Constitutional invariants honored

- **Invariant 6 (RLS at DB layer enforced by tenant_id GUC):** the bug undermined this; the fix restores it
- **Anti-pattern rejected:** application code is not the trust boundary; the calling credential is the single source of tenant context

## Canvas references

- `Plans/canvas/05-scopes.md` §5.4 (Postgres RLS named explicitly)
- `CLAUDE.md` (Invariant 6 + tenancy-context plumbing rules)
- Slice 033 PR body (`gh#27`) — surfaces this as P0 follow-up
- Slice 034 PR body (`gh#26`) — introduced the buggy handlers

## Dependencies

- #033 (tenancy.Middleware live on main as `c534c85`)
- #034 (admincreds handlers exist on main as `ee0a333`)

## Anti-criteria (P0)

- Does NOT alter the `Rotate` or `Revoke` handlers (those already derive tenant from credential correctly)
- Does NOT bypass slice 033's middleware by adding a new `tenancy.WithTenant` call on the buggy paths
- Does NOT introduce a "super-admin can cross tenants" code path (out of scope; cross-tenant admin operations are a separate slice if ever needed)
- Does NOT silently swallow the breaking API change — clients that supply `tenant_id` get an explicit 400 with a message pointing them to the new contract

## Skill mix (3–5)

- Go HTTP handler refactor (delete one field, swap one variable)
- Integration test discipline (negative tests must prove the fix)
- API-contract change announcement (CHANGELOG entry must surface the breaking change for OSS users)
- Security-review (confirm the diff doesn't leave any req.TenantID/query-tenant_id path in admincreds)
