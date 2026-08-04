# OE-435 — Persist push credentials so an atlas restart cannot invalidate them · decisions log

**Type:** JUDGMENT · **Requested by:** chaos gap G-1
(`docs/audit-log/356b-atlas-restart-mid-push-chaos-decisions.md`) ·
**Issue:** OPENENGINE-435 · **Date:** 2026-08-04

> Naming note: the `435-` prefix here is the OPENENGINE issue number
> (the OE-596 precedent), NOT repo slice 435 — that slice's log is
> `435-dbtest-harness-decisions.md`. The two are unrelated.

- detection_tier_actual: `none` (no new bug surfaced during this work; the
  gap itself was found by the 356b chaos run at `manual_review`)
- detection_tier_target: `integration` — and this work closes 356b's stated
  tier gap: `internal/auth/credpersist_integration_test.go` now restarts
  the process shape and keeps pushing on a pre-restart credential, exactly
  the sequence no tier could previously reach.

---

## What this closes

Slice 356b measured that a `docker compose restart atlas` mid-push turned
every subsequent push into `Unauthenticated`, permanently: 49 of 60
evidence records lost, and a credential proven working before the restart
never worked again without operator re-issuance. Root cause (356b D4): the
credstore was two in-memory maps with no backing table and no load-at-boot
path. After this change, credentials issued via the AdminCredentials
service write through to the `api_keys` table and are rehydrated at boot.

## D1 — Persistence backend: the existing slice-034 `api_keys` table, no migration

The issue named `api_keys` as the obvious candidate and it survived
contact: every field the credstore carries already has a column
(`is_admin`, `is_approver`, `owner_roles`, `last4`, `ttl_seconds`,
`retires_at`, `rotated_from`, `scope_predicate`, `allowed_kinds`,
`token_hash`), so **no migration was needed** and the "conflicting
migration" blocked-path in the issue body never arose. The table is
already tenant-scoped and RLS-enforced (constitutional invariant #6) and
already stores HMAC-SHA256 token hashes per ADR 0002 — the two properties
the issue forbade weakening are inherited, not re-implemented.

Alternatives considered and rejected:

- **A new credstore-owned table** — duplicates a table that already models
  exactly this data; two sources of truth for API keys is how the gap
  happened in the first place (slice 068's correction note shows the
  bootstrap path was already _believed_ to populate `api_keys`).
- **File-based snapshot** — no RLS, no tenant scoping, a second at-rest
  secret surface to audit, and diverges from the single-Postgres
  operational model.
- **Re-mint-from-env for everything** — works only for credentials whose
  plaintext lives in config (the `ATLAS_BOOTSTRAP_TOKEN` asymmetry 356b
  identified); connector/CI credentials have no config home by design.

## D2 — Shape: a `Persister` interface in credstore, implemented by `apikeystore`

`credstore.Persister` (NewID / HashToken / Insert / Rotate / Revoke /
LoadActive) is defined in `internal/api/credstore` and implemented by
`internal/auth/apikeystore.Persister`, so credstore never imports the DB
stack and the existing `Store` surface is unchanged — no caller changed.
Stores without a Persister (unit tests, in-memory servers) behave exactly
as before. `cmd/atlas` wires it via `srv.AttachCredstorePersistence(...)`
inside the existing pool block.

Credential ids are minted by the Persister as `key_<uuid>` so the id a
credential authenticates under today is the `api_keys` primary key it
reloads under after a restart — receipts keep the same `credential_id`
across the boundary (asserted by the integration test).

## D3 — Write-through fails closed

`Issue`/`Rotate` return an error (and commit nothing to the in-memory
maps) when the durable insert fails; `Revoke` refuses to mark a key
revoked in memory if the durable revocation fails. Rationale, both ways:

- A credential issued but not persisted silently reproduces the exact
  chaos failure at the next restart — worse than a failed issuance the
  caller can see and retry.
- A revocation recorded only in memory would silently **resurrect** the
  key at the next boot — a security hole rather than an availability one.

Same posture at boot: if `LoadActive` fails, `cmd/atlas` exits 1 rather
than starting with an empty credstore, because "boots fine, rejects every
issued credential" is precisely the outage 356b measured. Trade-off
accepted: issuance and boot now have a hard Postgres dependency. Both are
rare, operator-visible operations; the authentication hot path still runs
entirely from the in-memory maps and takes no new dependency.

## D4 — Bootstrap credentials stay memory-only, by design

The cmd/atlas bootstrap issuances run BEFORE the persistence attach and
are not retroactively persisted; `IssueFixedAdmin` never writes through
even after attach. Three reasons:

- They are re-minted from the environment on every boot — they already
  survive restarts by construction (the 356b "tell"), so persistence adds
  nothing but `api_keys` clutter.
- Persisting the deterministic `ATLAS_BOOTSTRAP_TOKEN` hash each boot
  would collide with the table's token-hash uniqueness.
- Their rows would go stale the moment the operator rotates the env value,
  leaving zombie rows that authenticate nothing.

The integration test asserts a bootstrap credential still authenticates
AND leaves zero `api_keys` rows behind. A rotation whose predecessor is
memory-only persists the successor with `rotated_from` cleared — the FK
target row does not exist.

## D5 — Tenant scoping: writes on the RLS pool, the boot scan on BYPASSRLS

All writes (insert / rotate / revoke) run on the `atlas_app` pool inside a
`tenancy.ApplyTenant` transaction — RLS-enforced like every other
tenant-scoped write (invariant #6). Only the one-shot `LoadActive` boot
scan runs on the BYPASSRLS pool, mirroring the existing
`GetAPIKeyByHash` posture: it executes before any tenant context exists,
and the `tenant_id` each loaded row carries is what every subsequent
authorization decision is scoped by. When `DATABASE_URL` is unset the
scan would see zero rows through FORCE RLS, so cmd/atlas warns loudly
that previously persisted credentials cannot be rehydrated in that
configuration. The integration test asserts tenant B cannot see tenant
A's rows through the app pool.

## D6 — Two hash spaces, one lookup

Persisted credentials key the in-memory map by the Persister's
HMAC-SHA256 (ADR 0002) so loaded rows and live issues share one scheme;
pre-attach (bootstrap) records remain under the legacy unkeyed SHA-256
and authenticate via an explicit fallback in `lookupTokenLocked`. The
alternative — rehashing bootstrap records at attach time — is impossible:
the store holds hashes, not plaintexts, which is the property we are
preserving. `last_used_at` stays an in-memory-only cosmetic on the auth
hot path; the durable column is not touched per-request.

## D7 — Known limit: multi-replica issuance visibility

Rehydration happens at boot. In a hypothetical multi-replica deployment a
credential issued on replica A after replica B booted would not be in B's
memory until B restarts. v1's deployment target is a single VM /
single-process docker-compose (AGENTS.md), so this is recorded as a limit,
not fixed; a shared-cache or read-through design is a v2+ slice if
horizontal scaling of the API tier ever lands.

## Verification

- Unit: `internal/api/credstore/persist_test.go` — write-through,
  fail-closed, legacy fallback, fixed-admin exclusion, rotate/revoke
  durability against a fake Persister. Package coverage 69 → 73.3%;
  floor ratcheted 69 → 73 in `cmd/scripts/coverage-thresholds.json`
  (same-PR lift per the ratchet rule).
- Integration (real Postgres, RLS enforced, `internal/auth` shard B1):
  `credpersist_integration_test.go` — issue → restart → push (the
  headline AC and the exact 356b failure sequence), revoke and rotate
  surviving restart, bootstrap memory-only, RLS isolation, and
  hashed-at-rest (stored `token_hash` = HMAC of bearer, never plaintext,
  only `last4` beyond it).
- The SDK retry configuration is untouched (356 P0-3); the retry gap
  remains OPENENGINE-436's scope.

## Acceptance criteria — status

| AC                                                      | Status                                                             |
| ------------------------------------------------------- | ------------------------------------------------------------------ |
| Credential issued before restart authenticates after    | MET — `TestCredPersist_IssueRestartPush`                           |
| Hashed at rest, no plaintext bearer persisted           | MET — inherited HMAC-SHA256 (ADR 0002); asserted at-rest           |
| Rows tenant-scoped and RLS-enforced                     | MET — writes via `tenancy.ApplyTenant`; cross-tenant read asserted |
| Integration test: issue → restart → push, fails if lost | MET — `internal/auth/credpersist_integration_test.go`              |
| Bootstrap issuance paths unchanged                      | MET — pre-attach + fixed-admin stay memory-only; asserted          |
| Decisions log records choice + trade-offs               | MET — this document                                                |

## Cross-references

- Chaos finding: `docs/audit-log/356b-atlas-restart-mid-push-chaos-decisions.md` (G-1, D1, D4)
- Token hashing: `docs/adr/0002-bearer-token-storage.md` (HMAC-SHA256 at rest), slice 034 `api_keys`
- Tenant isolation: constitutional invariant #6, `docs/architecture/rls.md`
- Retry gap (explicitly NOT remedied here): OPENENGINE-436
