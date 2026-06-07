# Slice 456 — `internal/api/oauth` residual coverage: decisions log

**Type:** AFK (autonomous; no subjective product calls — the calls below are
mechanical scope/measurement decisions the slice spec pre-authorizes).

**Parent:** slice 422 (floor 72 → 77, RFC error branches). Toward the
slice-350 90% security-critical advisory.

---

## Outcome

| Metric                                 | Before (slice 422) | After (slice 456) |
| -------------------------------------- | ------------------ | ----------------- |
| `internal/api/oauth` merged measured   | 79.0%              | **80.8%**         |
| `internal/api/oauth` hard floor        | 77                 | **78**            |
| `$security_critical_packages` advisory | 90 (unchanged)     | 90 (unchanged)    |

**Floor derivation:** `max(0, floor(80.8 − 2))` = `floor(78.8)` = **78**
(slice 069 ratchet: floor = measured − 2pp noise band, never above measured).
The +1 lift leaves a 2.8pp margin above the new floor.

**Measurement flow (the real gate, not a guess):** per-package merged
unit + integration profiles, mirroring CI's slice-279 gocovmerge path:

```
go test -covermode=atomic -coverpkg=./internal/api/oauth/... \
  -coverprofile=unit.cov  ./internal/api/oauth/...
go test -tags=integration -p 1 -covermode=atomic \
  -coverpkg=./internal/api/oauth/... \
  -coverprofile=int.cov   ./internal/api/oauth/...
go run ./cmd/scripts/coverage-gate -profile=unit.cov -extra-profile=int.cov
  → internal/api/oauth: got 80.8% ; HARD FLOORS PASS
```

Run against a per-worktree `postgres:16-alpine` (port 55456) bootstrapped
with `migrations/bootstrap/01-roles.sql` + all up migrations and
`ALTER ROLE atlas_app PASSWORD 'ci-ephemeral'`, `DATABASE_URL_APP` pointed
at the `atlas_app` role — the same shape CI uses.

---

## Arms covered (AC-by-AC)

### AC-1 — best-effort audit-write failure arms (REPUDIATION surface, D3)

`writeAudit` (token.go) and `writeAuthCodeAudit` (pkce.go) are best-effort:
a failure MUST NOT block nor corrupt the token response.

| Arm                             | File:line                  | Tier        | How driven                                                                                                       |
| ------------------------------- | -------------------------- | ----------- | ---------------------------------------------------------------------------------------------------------------- |
| nil-pool early return           | token.go:392 / pkce.go:90  | unit        | `ExportTokenEndpointForAudit(.., nil, ..)` + seam                                                                |
| BeginTx failure                 | token.go:405 / pkce.go:101 | integration | closed `*pgxpool.Pool`                                                                                           |
| Exec failure (missing relation) | token.go:445 / pkce.go:135 | integration | `AfterConnect` strips `search_path` to an empty schema → unqualified `oauth_token_exchanges` resolves to nothing |
| empty-jti `"unknown"` fallback  | token.go:429               | integration | claim with `ID == ""`; verified via RLS-scoped read that jti=="unknown"                                          |
| from_tenant non-nil + NULL      | token.go:416               | integration | exercised across the Exec-fail + happy seams                                                                     |
| jti truncation (>64 chars)      | pkce.go:116                | integration | 100-char `ac.Code`                                                                                               |
| happy INSERT + Commit           | pkce.go:126                | integration | explicit row-shape assertion (from_tenant NULL, to_tenant=current)                                               |

Every failure-arm test asserts the **non-blocking** outcome (the seam
returns, no panic) — not merely an HTTP status (P0-456-3).

### AC-2 — signer-failure `server_error` (500) arms (AVAILABILITY surface)

Driven by a **verify-ok / sign-fail keystore**: the active signing key is a
non-ES256 **P-384** ECDSA key, so `tokensign.Sign` fails its `isES256Key`
curve check, while `Verify` still resolves the real fsstore verification
keys by kid (so a self-minted subject_token / a real redemption verifies,
then the handler's own mint step 500s).

| Mint site          | File:line                | Tier        | Reachability                                                                                                              |
| ------------------ | ------------------------ | ----------- | ------------------------------------------------------------------------------------------------------------------------- |
| token-exchange     | token.go:366             | **unit**    | Verify on self-minted subject + allowlist pass, then Sign fails — no DB needed                                            |
| client_credentials | token.go:272             | integration | needs a real `oauth_clients` row to pass Verify first                                                                     |
| authorization_code | authorize.go:479         | integration | needs a real PKCE auth-code redemption first                                                                              |
| device_code        | device_code_grant.go:127 | integration | needs an approved device-code redemption first (approve BEFORE first poll → single poll reaches Sign, no slow_down sleep) |

Each asserts 500 + the RFC `server_error` code; the token-exchange unit test
adds a no-internal-detail-leak assertion (no "keystore"/"es256"/"curve"/
"tokensign"/"sql"/"pgx"/"panic" in the body) composing with slice 367.

### AC-3 — rate-limiter internals

| Arm                                  | File:line        | Tier | How                                                                                 |
| ------------------------------------ | ---------------- | ---- | ----------------------------------------------------------------------------------- |
| overflow cap (`tokens > rate` clamp) | token.go:574     | unit | injected clock, long idle then burst of `rate` calls all succeed, next refused      |
| `WindowSeconds` rate≤0 default (60)  | token.go:590     | unit | table: 0, −5 → 60                                                                   |
| `max(1, 60/rate)` floor + else arm   | token.go:593/597 | unit | high rate → 1; 30/min → 2                                                           |
| `NewTokenEndpoint` rate≤0 fallback   | token.go:150     | unit | rate=0 config → request reaches 503 (not 429), proving the default rate took effect |

All driven through new unexported `export_test.go` seams
(`ExportNewTokenBucketLimiter` / `ExportLimiterAllow` /
`ExportLimiterWindowSeconds`) — `New*Endpoint` unchanged (P0-456-2, slice 409
precedent).

### AC-4 — material rise + documented residual

79.0% → 80.8% (+1.8pp). Per-function the named arms moved decisively:
`handleClientCredentials` 92→**100**%, `handleTokenExchange` 94→**100**%,
`NewTokenEndpoint` 91→**100**%, limiter `Allow` 94→**100**%, `WindowSeconds`
67→**100**%, `writeAudit` 81.5→88.9%, `writeAuthCodeAudit` 72.7→81.8%,
`handleAuthorizationCode` 86→90.7%, `handleDeviceCode` 83→88.9%.

The package-level number rose only +1.8pp (not "mid-80s") because the
remaining gap is **outside this slice's three named arm-categories** — see
the residual below.

### AC-5 — floor lifted same PR (78). AC-6 — advisory 90 untouched.

---

## Residual gap to 90 (documented per AC-4)

1. **`ApplyTenant`-failure arms** (token.go:410 / pkce.go:106) — effectively
   **defensive dead code**. `ApplyTenant` runs `SELECT set_config(...)`
   (a `SET LOCAL`) which always succeeds on a healthy transaction; the arm
   is only reachable on a connection broken _mid-transaction_ after BeginTx
   succeeded — not deterministically forceable without a fault-injection
   wrapper around `pgx.Tx` that the slice's scope discipline (P0-456-2,
   no runtime change beyond an optional unexported seam) does not warrant.
   Left as residual; not worth a fault-injecting tx mock for ~2 statements.

2. **Out-of-named-scope surfaces** filed as **spillover slice 472**:

   - `device_approve.go` `ServeApprove` (34%) / `ServeDeny` (44%) — the
     device-approval _browser_ flow (slice 191 surface).
   - `device_authorization.go` `Approve` (37%) / `Deny` (0%) /
     `Consume` (79%) / `LookupByUserCode` (0%).
   - `user_resolver.go` BYPASSRLS authPool path:
     `NewDBUserResolverWithAuthPool` (0%) / `enumerateMemberships` (0%) /
     `lookupSuperAdmin` (0%) — the slice-192 cross-tenant membership +
     super_admin enumeration (a real security surface, but a distinct one).

   These belong to slices 191/192's coverage, not 456's named arms
   (audit-write + signer-failure + rate-limiter). Covering them here would
   contradict the slice's scope discipline; the spec itself anticipates the
   gap to 90 spilling.

---

## Detection-tier classification (slice 353, Q-13)

- `detection_tier_actual`: **none** — no production bug surfaced; this is a
  pure coverage-lift slice. The verify-ok/sign-fail keystore confirmed the
  existing signer-failure handlers already behave correctly (500 +
  `server_error`, no leak); the audit-write best-effort arms already swallow
  failures correctly (no caller-visible error). No fix-forward needed.
- `detection_tier_target`: **none** (no defect).

The one _latent_ observation (not a bug): the `ApplyTenant`-failure arms are
unreachable defensive code — noted above, not filed as a defect.

---

## Scope-discipline confirmations

- `New*Endpoint` byte-for-byte unchanged; only unexported `export_test.go`
  seams added (P0-456-2 / slice 409 precedent).
- No AS runtime behavior changed — tests + floor lift only.
- `_STATUS.md` / `_INDEX.md` untouched by this slice's commits (P0-456-4).
- No JWT/vendor-shaped fixture literals — every token minted in-process by
  the real fsstore signer; the sign-failure is a P-384 curve, not a literal.
