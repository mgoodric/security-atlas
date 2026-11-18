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
  in-memory store, so such a row does reach gRPC. Those rows are minted by
  `POST /v1/admin/credentials`. The gRPC `Issue` (`IssueMinted`) never sets
  `IsAdmin`.
- #2655 opened `/v1/admin/credentials` to tenant admins, but its guard keeps
  them from creating such a key. `mintWithinCaller` refuses an `is_admin` or
  `is_approver` key on issue for any caller without `cred.IsAdmin`, and
  `callerMayHandle` applies the same check to rotate and revoke, so a tenant
  admin cannot rotate an existing admin key into a fresh plaintext either.

So reaching the gRPC admin service needs a credential that already holds
`IsAdmin`. If a later change lets a tenant admin set `is_admin` on a key, or
removes the rotate check, this stops holding and the gRPC path must be
audited before that lands.

## D10. Part 3: generic pre-auth errors

`/auth/oidc/login` and `/auth/oidc/callback` answer before anyone is signed
in. An unusable saved config used to reach the caller as
`OIDC begin: oidc: IdP config unusable: <reason>`, and the reason describes
the organisation's configuration ("issuer_url must be https", "re-enter it on
the SSO settings page"). Both handlers now answer 503 with
`ErrMsgSSOConfigUnusable`. The reason stays in the server log, where
`idpconfig.Resolver.unusable` already writes it with the tenant and config
ids. 503 rather than 400: the caller did nothing wrong, and the organisation's
SSO is not available until an admin fixes it. The callback also clears the
flow cookies, as its other refusals do.

The sentinel moved to `oidc.ErrIdpConfigUnusable` so the handlers can match it
without importing `idpconfig`. `idpconfig.ErrConfigUnusable` is the same
value, so existing `errors.Is` checks keep working. Other fall-through errors
(`OIDC begin: ...`, `OIDC callback: ...`) are unchanged: the ticket names
this one, and the others carry no organisation configuration.

## D11. One issuer policy

PATCH validated the issuer with `PreflightOptions.AllowPrivateIPs`, and the
resolver with `issuerPolicy.AllowLoopback`. Both came from the same env var,
so they agreed in practice, but through two booleans derived in two places.
Now `idpconfig.ValidateIssuerURL` takes a `netguard.Policy`. PATCH, preflight
and the resolver all call it with the policy `oidc.IssuerPolicyFromEnv`
returns: `register_admin` hands it to the SSO handler as
`PreflightOptions.IssuerPolicy`, which replaces `AllowPrivateIPs`, and
`cmd/atlas` hands it to the resolver. Preflight now applies the same scheme
rule, so under the dev opt-in it refuses an `http://` issuer that is not
loopback, as PATCH and sign-in always did.

## D12. The secret key is validated in every mode

`cmd/atlas` read `ATLAS_OIDC_SECRET_KEY` only when local mode was off, so a
malformed key passed the boot check in local mode, and `register_admin` then
silently left PATCH without a crypter. The resolver choice moved into
`oidcResolverFromEnv`, which reads the key first, in every mode: a key that is
set but malformed (not base64, wrong length, unreadable `_FILE`) stops the
boot. An unset key is still not an error. `register_admin`'s comment, which
says cmd/atlas refuses to boot on a malformed key, is now true in every mode.

## D13. Helm: the key is a mounted file

`oidc.secretKey.existingSecret` names a Secret the chart mounts read-only
at `/etc/security-atlas/oidc/`, and the chart sets
`ATLAS_OIDC_SECRET_KEY_FILE`. The key is kept apart from
`secrets.existingSecret`, which the chart feeds to the pod as environment
variables through `envFrom`, so that it never sits in the environment.
`secrets.oidcSecretKey` renders it into the inline dev Secret instead. The
compose `.env.example` already carried the key; it now notes the every-mode
check.

**Review fix: the file must be readable by the server.** The first version
mounted the file root-owned, mode 0400, with no `fsGroup`. The atlas image
runs as distroless `nonroot` (uid/gid 65532), so the read failed, D12 made
that a fatal boot error, and `values-production.yaml` turned the mount on by
default: every production install would crash-loop. Now:

- the volume is mode 0440 (owner root, group the pod's `fsGroup`);
- the atlas pod gets `fsGroup: 65532` whenever the key is mounted, merged
  into `podSecurityContext` so an explicit `podSecurityContext.fsGroup`
  wins. It is added to the atlas pod only, not to the chart-wide
  `podSecurityContext` default, because that default is shared with the
  MinIO and NATS pods, whose PVCs `fsGroup` would recursively re-chown on
  every mount;
- `values-production.yaml` leaves `existingSecret` empty and shows the
  Secret name as a commented example, so an upgrade without a pre-created
  Secret does not hang in ContainerCreating;
- `scripts/test-helm-oidc-key.sh`, run by the `Helm chart · lint + template`
  CI job, asserts on every render that a key volume implies an `fsGroup`,
  and checks the mode, the override, the production values and that NATS
  is untouched. The repo has no kind / cluster smoke job, so this is a
  render assertion, not a boot check.

## D14. SSO page wording

With `trust_unverified_email_for_link` on, `emailDomainAllowed` counts an
email with no `email_verified` claim as verified, so the flag widens the
domain allowlist as well as SCIM linking. The SSO page help, the in-app help
module and `oidc-setup.md` now say so.

The admin help modules' "Who can do this" sections (`admin-sso`,
`admin-users`, `admin-api-keys`) still say "Only super admins", which #2503
made stale. That is outside this ticket and is left for a follow-up.
