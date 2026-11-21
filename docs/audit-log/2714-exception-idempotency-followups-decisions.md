# 2714 — Exception idempotency follow-ups: decisions log

Issue: mgoodric/security-atlas#2714 (follow-ups from the review of #2710 /
#2618).

## Detection-tier classification

- **detection_tier_actual:** manual_review. The unbounded key table and the
  key-order-sensitive hash were found reviewing #2710.
- **detection_tier_target:** integration (the sweep) and unit (the hash).

## Decisions

### D1 — expired keys are swept per tenant on the app role

`exception_idempotency_keys` had no DELETE grant or policy, and a key was
only overwritten when its requester reused it, so a key sent once stayed
forever. `exception.IdempotencySweeper` deletes rows with
`created_at <= now() - IdempotencyWindow`, at start and hourly.

It takes the control import sweeper's two-pool shape: the migrator pool
lists tenant IDs only, and each tenant's DELETE runs on `atlas_app` under
`tenancy.WithTenant`, so the write is RLS-bound (invariant #6). Migration
`20261274000000_exception_idempotency_keys_sweep.sql` adds the
`tenant_delete` policy and `GRANT DELETE`. A single cross-tenant DELETE on
the migrator role was simpler but would be the one write to this table that
RLS does not bind.

The bound is the complement of `GetExceptionIdempotencyKey`
(`created_at > now() - window`), so the sweep never deletes a key that
could still replay. No index on `created_at`: the table holds one row per
keyed request for a day.

### D2 — the request hash is canonical

`requestHash` now re-encodes the predicate through `encoding/json` (map
keys sorted, `UseNumber` so large numbers keep their literal text), treats
an omitted predicate as `{}` (the stored spelling), and treats
`compensating_controls` omitted, `null` and `[]` as one value. The order of
`compensating_controls` still counts: the stored array is ordered.

One-time effect at upgrade: a key recorded by the previous build whose
predicate keys were not already sorted hashes differently now, so a retry
of it inside the first 24h after the upgrade gets 422 instead of a replay.
Accepted: the window is short and the client can resend without the key.

### D3 — revoked-jti TTL: confirmed, pinned

Self-revocation stores the token's own `exp` as `expires_at`
(`internal/api/oauth/revoke.go`), the sweeper deletes only
`expires_at < now()`, and `ON CONFLICT (jti) DO NOTHING` means a second
revoke cannot move it. `TestIntegrationRevoke_SelfRevocationLastsUntilExp`
pins all three. Observed failing with the handler recording `exp - 30m`.

### D4 — `forwardedForHeaders`: confirmed, no change

The BFF does not forward a browser-supplied `X-Forwarded-For`.
`web/lib/client-ip.ts` returns an address only when the
`web/peer-chain.cjs` preload is installed, which replaces the header with
the address it resolved from the socket peer and `WEB_TRUSTED_PROXY_CIDRS`;
without it the BFF forwards nothing (tested in `web/lib/client-ip.test.ts`).
On the API side the revoke throttle keys on `auth.ClientIP`
(`cmd/atlas/main.go`), which walks the header right to left only through
`TRUSTED_PROXY_CIDRS` peers. A caller cannot pick its revoke bucket unless
an operator lists an untrusted network as a proxy.

### D5 — replay returns current state, documented

A replay re-reads the exception by id, so it returns what the exception is
now (it may be approved, denied or active), not the first response. The
route summary in `internal/api/openapi/routes.go` says so;
`docs/openapi.yaml` is regenerated from it.
