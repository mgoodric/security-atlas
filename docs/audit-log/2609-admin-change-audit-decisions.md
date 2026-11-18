# #2609 — audit rows for tenant admin changes — decisions log

**Ticket:** https://github.com/mgoodric/security-atlas/issues/2609
**Date:** 2026-11-18

Matt's priority (2026-11-18): organisation admins can now change their own
tenant's SSO config (#2632) and SCIM credentials (#2636), and those writes
must be audited before #2503 closes. The ticket's scope grew twice, by
comments on it, to group-role mappings and API credentials. The work lands in
two PRs:

| PR    | Audits                                                              | Migration                                    |
| ----- | ------------------------------------------------------------------- | -------------------------------------------- |
| #2638 | `PATCH /v1/admin/sso`; SCIM credential issue / revoke               | `20261262000000_sso_scim_audit`              |
| #2644 | group-role mapping create / delete; API key issue / rotate / revoke | `20261263000000_group_mapping_api_key_audit` |

The ticket's other items (generic pre-auth errors, one issuer policy, key
validation in every mode, Helm / `.env` key, SSO page wording) follow in a
third PR. Key rotation stays in the ticket.

**Detection-tier classification (slice 353 / Q-13):**

- `detection_tier_actual`: none (a missing audit surface, not a bug found
  during the work).
- `detection_tier_target`: integration. Each audited store has an
  integration test that reads the row back under RLS, checks its content and
  actor, forces the audit write to fail and checks the change rolled back,
  and checks another tenant cannot read it.

## D1. One me_audit_log row, in the change's own transaction

Every audited write inserts its `me_audit_log` row inside the transaction
that makes the change, through the same `dbx.Queries`. A failed audit write
returns an error and the deferred rollback undoes the change, so a change
without its row cannot commit. This is the pattern #2224 set for the LLM
routing settings (`internal/llm/cloud/store.go`) and #2129 for vendor owners.

`me_audit_log` rather than a new table: it is the tenant's admin ledger
(`admin_audit_log_v` already reads it), it carries `actor_principal` (#2329),
and its RLS is append-only.

## D2. The actor

`user_id` is `credstore.CallerUserID(cred)`, `uuid.Nil` for a caller that is
not a user (bootstrap admin, unbound key). `actor_principal` is
`credstore.PrincipalOf(cred)`, which names the caller either way. This matches
the LLM routing handler. The SCIM handler keeps its existing `parseUserID`
for `scim_credentials.issued_by` and uses it for the audit `user_id` too, so
the two columns never disagree.

## D3. What a row records, and what it never records

| Action                                  | before / after                                                                                                                                          |
| --------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `tenant_sso_config_update`              | every config field; `after.secret_changed` true / false                                                                                                 |
| `scim_credential_issue` / `_revoke`     | `credential_id`, `last4`, `description`, `revoked`                                                                                                      |
| `group_role_mapping_create` / `_delete` | `mapping_id`, `idp_config_id`, `group_ref`, `role`                                                                                                      |
| `api_key_issue` / `_rotate` / `_revoke` | `key_id`, `last4`, `is_admin`, `is_approver`, `owner_roles`, `allowed_kinds`, `scope_predicate`, `ttl_seconds`, `rotated_from`, `revoked`, `retires_at` |

No audit struct has a field that can hold a secret: not the SSO client secret,
its ciphertext, a hash or a length; not a SCIM or API bearer token or its hash.
`last4` is already shown in every credential list, so it adds nothing an
admin cannot see. The tests search each row's full `to_jsonb` text for the
secret, its hex hash, and any `token` / `client_secret` key.

Rotate records the predecessor before (active) and after (with its
`retires_at`) and the successor, in one row: one admin action, one row.

## D4. Empty diffs

- An SSO PATCH that changes no field and no secret writes no row (the
  slice 108 ISC-A5 empty-diff rule).
- Revoking an already-revoked SCIM credential or API key writes no row: the
  state did not change.
- A group-role mapping create that matches an existing mapping **does** write
  a row. `InsertGroupRoleMapping` is idempotent and returns the existing row,
  so the store cannot tell a new mapping from a repeat without changing the
  query; the admin did ask for the mapping, so recording the request is the
  safe side.

## D5. Serialising the SSO before value

`PATCH /v1/admin/sso` takes a per-tenant advisory lock (`LockAdminSSO`,
namespace 11, `advisorylock.TenantSSOConfig`) before reading the current
config `FOR UPDATE`. A row lock alone cannot cover a tenant's first config,
where there is no row to lock, and two first PATCHes would each record "no
config" as their before. Same reasoning as `LockTenantLLMRouting` (#2329).
The "secret required on first config" check moved into that transaction.

## D6. Forcing the audit write to fail in tests

`adminsso` and `scim` replace the insert through a test-only hook
(`export_test.go`). `apikeystore` and `grouprole` are exercised from other
test packages, which cannot see an `export_test.go`, so their tests make the
write fail for real instead: an `actor_principal` longer than the column's
256-character CHECK (#2329). No production code carries a hook for them.

## D7. Migration numbering

`20261260000000` was first used here and collided with #2640
(`vendor_audit_log`), which had not landed either. Renumbered to
`20261262000000` (#2638) and `20261263000000` (#2644). #2640 does not touch
the `me_audit_log` action CHECK, so the CHECK chain is
`20261226000000` → `20261262000000` → `20261263000000`.

## D8. Older migration tests on a shared local database

The `20261120000000` and `20261226000000` migration tests applied their down
migration after clearing only their own actions, so they failed on a database
that also held rows with later actions. They now clear every row the narrowed
CHECK refuses, inside their rolled-back transaction. In CI, `internal/db` runs
on leg A, which no package writing the new actions shares, so CI was not
affected. Other older tests of the same shape (for example
`vendor_initial_review`, `risk_review_cadence`) still need a fresh local
database; they are left as they are.

## D9. The gRPC admin key service is not audited here, and an org admin cannot reach it

`AdminCredentialsService` (`internal/api/admin/service.go`) issues, rotates
and revokes keys through the in-memory `credstore.Store` and its
`apikeystore.Persister`, which take no context or actor. A same-transaction
audit row there means replumbing that path, so it is a separate ticket
(filed by the dispatcher). It does not block #2503, because no organisation
admin can call it:

- The gRPC server authenticates with `authInterceptor`
  (`internal/api/server.go`), which calls `credstore.Store.Authenticate`
  only: an API bearer key. A JWT session (password, OIDC, PKCE, device
  grant) is not accepted there at all.
- Every RPC gates on `admin.RequireAdmin`, which checks `cred.IsAdmin`, not
  `authz.IsTenantAdmin`. A tenant admin by role does not carry `IsAdmin`.
- Only two kinds of key carry `IsAdmin`: the bootstrap admin credential
  (`credstore.IssueFixedAdmin`, `IssueAdmin` and the bootstrap-token file;
  outside tests only the first-install bootstrap mints them) and an
  `api_keys` row with `is_admin = true`. A key whose `owner_roles` label
  holds `admin` passes `authz.IsTenantAdmin` but not `cred.IsAdmin`, so it
  is refused too.
  At boot `Persister.LoadActive` loads every active `api_keys` row into the
  in-memory store, so such a row does reach gRPC. Those rows are minted only
  by `POST /v1/admin/credentials`, whose handler gate is still `cred.IsAdmin`,
  and `credentials` is not in `admin.rego`'s `tenant_admin_segments`. The
  gRPC `Issue` (`IssueMinted`) never sets `IsAdmin`.

So reaching the gRPC admin service needs a credential that already holds
`IsAdmin`. If a later change opens `/v1/admin/credentials` to tenant admins,
or lets one set `is_admin` on a key, this stops holding and the gRPC path
must be audited before that lands.
