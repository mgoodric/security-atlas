# #2652 — audit API key changes on the gRPC admin service — decisions log

**Ticket:** https://github.com/mgoodric/security-atlas/issues/2652
**Date:** 2026-11-20
**Follows:** #2609 (`docs/audit-log/2609-admin-change-audit-decisions.md`, D9)

**Detection-tier classification (slice 353 / Q-13):**

- `detection_tier_actual`: manual_review (the gap was found while scoping
  #2609, not by a test).
- `detection_tier_target`: integration.
  `internal/auth/grpc_apikey_audit_integration_test.go` drives the gRPC
  service against real Postgres and reads the rows back under RLS.

## D1. The audit row is written by the Persister, inside its transaction

`AdminCredentialsService` changes keys in the in-memory `credstore.Store`,
which writes through to `apikeystore.Persister`. The Persister already owns
the one transaction each change runs in, so it writes the `me_audit_log` row
there, through the same `dbx.Queries`. A failed write returns an error, the
transaction rolls back, and the credstore leaves its maps untouched, because
it only updates them after the write-through succeeds. A change without its
row cannot commit, in the database or in memory.

`credstore` cannot import `apikeystore` (the dependency runs the other way),
so the audited calls go through a new optional interface,
`credstore.AuditedPersister` (`InsertAudited`, `RotateAudited`,
`RevokeAudited`), each taking a `context.Context` and the caller's
`credstore.Credential`. `apikeystore.Persister` implements it. The Store
gains `IssueMintedAudited`, `RotateAudited` and `RevokeAudited`; the gRPC
`Issue`, `Rotate` and `Revoke` call those. The unaudited `IssueMinted`,
`Rotate` and `Revoke` stay for their other callers (install reset, tests).
`Rotate` and the audited rotate share one `rotateLocked`.

## D2. The same row as the HTTP path

Action names, actor and content match #2609 D2 and D3 exactly: the row is
built by the same `keyAuditOf` and written by the same function
(`Store.audit` became the package function `writeAudit`, shared by `Store`
and `Persister`). The actor comes from `apikeystore.ActorOf`, which the HTTP
handler's `auditActor` now also uses: `user_id` is `CallerUserID(cred)` or
`uuid.Nil`, `actor_principal` is `PrincipalOf(cred)`. No field can hold the
bearer or its hash; the integration test searches each row's full
`to_jsonb` text for both and for any `token` key.

The issue row describes the key from the credential the credstore built,
not from a reread of the inserted row; the two are the same values
(`insertRow` round-trips through `credentialFromRow`, OE-435).

## D3. Memory-only keys and empty diffs

- **Revoke of a memory-only key** (the bootstrap admin key, which has no
  `api_keys` row) still writes a revoke row: an admin revoked a credential,
  and the ledger is the only durable trace of that.
- **Rotate of a memory-only key** inserts the successor and writes the
  rotate row; the predecessor has no row to retire, as before.
- **Revoking a revoked key** writes nothing. In memory the Store returns
  early; for a persisted key whose row another replica already revoked, the
  Persister reads the row and writes nothing (#2609 D4).

## D4. Fail closed, except where there is no database

- A Persister attached that does not implement `AuditedPersister` makes the
  audited calls fail with `credstore.ErrAuditUnavailable`; they never fall
  back to an unaudited change. Production attaches `apikeystore.Persister`
  (`cmd/atlas`), which does.
- A Store with no Persister (DB-less dev mode, unit tests) has no table to
  audit into and no durable state at all; its changes stay memory-only, as
  every change on such a store already does.

## D5. Who can reach this service (re-verified on `main`, 2026-11-20)

#2609 D9 still holds. Only a credential that already holds `IsAdmin` can
call `AdminCredentialsService`, so it is not an organisation-admin surface
and does not block #2503:

- The gRPC server authenticates with `authInterceptor`
  (`internal/api/server.go`), which calls `credstore.Store.Authenticate`
  only. A JWT session (password, OIDC, PKCE, device grant) is not accepted.
- `Issue`, `Rotate`, `Revoke` and `List` gate on `admin.RequireAdmin`, which
  checks `cred.IsAdmin`, not `authz.IsTenantAdmin`.
- `IsAdmin` keys come from: the first-install bootstrap
  (`Server.IssueAdmin` / `IssueFixedAdmin`, `ATLAS_BOOTSTRAP_TOKEN` and the
  bootstrap-token file); the recovery key `IssueRecoveryAdmin` minted by
  reset-bootstrap, which admits only a super admin or an existing `IsAdmin`
  credential (`authorizeResetBootstrap`); and `api_keys` rows with
  `is_admin = true`, rehydrated at boot by `Persister.LoadActive`.
- A tenant admin cannot create or rotate such a row:
  `admincreds.mintWithinCaller` refuses `is_admin` / `is_approver` for a
  caller without `cred.IsAdmin`, and `callerMayHandle` applies it to rotate
  and revoke. The gRPC `Issue` never sets `IsAdmin`.

The audit now covers this path regardless, so if a later change does let a
tenant admin hold `IsAdmin`, the gRPC path is no longer an unaudited way in.
