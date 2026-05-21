# 187 — OAuth AS scaffolding · Decisions log

**Slice:** [`docs/issues/187-oauth-as-scaffolding-jwt-signing.md`](../issues/187-oauth-as-scaffolding-jwt-signing.md)
**Type:** JUDGMENT
**Filed:** 2026-05-20
**Built:** 2026-05-20

This slice is the foundation of the auth-substrate-v2 spine (187 → 188 → 189 → 190 → 191 → 192). Every subjective call recorded here is something the maintainer should revisit once the spine lands and the platform sees real OAuth-grant traffic — the calls made cold here are defensible best-fits for an internet-shaped, standards-based OAuth 2.0 Authorization Server inside atlas, not load-tested choices.

## Decisions made

### D1 — JWT signing algorithm: **ES256 (ECDSA P-256)**

**Options considered:**

- RS256 (RSA-2048, RFC 7518 §3.3) — the most-deployed JWT signing algorithm; ~256-byte signatures; ~10ms sign on commodity hardware.
- ES256 (ECDSA P-256, RFC 7518 §3.4) — modern recommendation; ~64-byte signatures; faster sign than RSA-2048; native NIST P-256 support in Go's stdlib.
- EdDSA (Ed25519, RFC 8037) — newer, also fast and short; less library coverage; not on the RFC 9068 explicit-recommendation list.

**Chosen:** ES256.

**Rationale:** atlas has no legacy clients to maintain compatibility with — the entire consumer surface (SDKs ×4, CLI, browser, future external integrations) is being designed in slices 188-192. ES256 is the right default for a green-field internal AS: smaller tokens (matters when JWTs flow through query strings during PKCE), faster signing on the hot path that slices 188's `/oauth/token` will hammer, and broad support across every OIDC RP we are likely to interoperate with (Auth0, Okta, Google IdP, Microsoft Entra ID, Keycloak all sign ES256 today). RS256 was a reasonable alternative; the decision favors the modern default over the conservative one because we are pre-PMF and can afford to set the higher bar.

**Confidence:** high.

**Revisit if:** a customer-specific integration (e.g., a HSM-only enterprise IdP) cannot verify ES256. JWKS publishes the `alg` per-key, so a future slice can add a second key in a different algorithm without breaking existing verifiers.

### D2 — JWT library: **`github.com/go-jose/go-jose/v4`**

**Options considered:**

- `github.com/golang-jwt/jwt/v5` — most popular Go JWT library; ~7k stars; covers RFC 7519 well; weaker JWK / JWKS surface than go-jose.
- `github.com/go-jose/go-jose/v4` — comprehensive JOSE library (JWS + JWE + JWK + JWS); already an indirect-dep via `github.com/coreos/go-oidc/v3` (slice 034); covers RFC 7515/7516/7517/7518/7519 directly.
- `github.com/lestrrat-go/jwx/v2` — also comprehensive; comparable to go-jose; not currently in the dep tree.

**Chosen:** go-jose/v4.

**Rationale:** go-jose is already transitively present in `go.sum` via the slice-034 OIDC RP path — promoting it to a direct dep adds zero supply-chain surface. Its JWK / JWKS support is first-class (the `jose.JSONWebKey` + `jose.JSONWebKeySet` types are what we marshal to the wire for the JWKS endpoint). The library cleanly separates JWS layer (this slice) from the OAuth protocol layer (slices 188-192) — exactly the layering we want. It also enforces the algorithm-allowlist pattern (`jose.ParseSigned(tok, allowedAlgs)`) that defends against RFC 8725 algorithm confusion attacks at the parser level, not after the fact.

go-jose covers RFC 9068 (it's just RFC 7519 + extra `iss/aud/exp/iat/jti` discipline at the JWT layer, plus shape rules at the OAuth layer — go-jose owns the JWT layer; the OAuth-layer rules are enforced in `internal/auth/jwt/Validate` + the upcoming slice 190 middleware) and RFC 8693 (token exchange happens at the OAuth protocol layer, not the JWS layer — go-jose provides the sign+verify primitives the exchange flow will use).

**Confidence:** high.

**Revisit if:** the library hits an EoL announcement or a CVE that doesn't get patched on a reasonable cadence. The wrapping in `internal/auth/tokensign` keeps the library swap-able — every other package depends on `tokensign.Signer`, not on go-jose directly.

### D3 — Filesystem keystore path override env var: **`ATLAS_KEYSTORE_PATH`**

**Options considered:**

- `ATLAS_KEYSTORE_PATH` — matches the `ATLAS_*` convention used throughout `cmd/atlas` (`ATLAS_BOOTSTRAP_TENANT`, `ATLAS_BOOTSTRAP_TOKEN`, `ATLAS_DATA_DIR`, `ATLAS_METRICS_FALLBACK_ENABLE`, etc.).
- `ATLAS_AS_KEYSTORE_PATH` — more specific, but adds a 3-char prefix that doesn't disambiguate anything since this is the only keystore.
- `ATLAS_OAUTH_KEYSTORE` — slightly more discoverable, but breaks the convention.

**Chosen:** `ATLAS_KEYSTORE_PATH`.

**Rationale:** convention alignment. The default value (`/var/lib/security-atlas/keys/`) follows the FHS pattern used by every other system-installed service.

**Confidence:** high.

**Revisit if:** a second keystore lands (e.g., evidence-signing keys, audit-export bundle signing). At that point the env var probably becomes `ATLAS_OAUTH_KEYSTORE_PATH` to disambiguate.

### D4 — Key rotation strategy (designed, not implemented end-to-end in slice 187)

The keystore + JWKS handler ship multi-key support today; the rotation flow itself lands in a follow-on slice. ADR-0003 captures the designed-shape decisions so the follow-on slice has a starting point rather than a clean-room design:

- **Rotation cadence:** 90 days. The threat model treats a rotated key as a defense-in-depth measure against undiscovered key exposure, not as a response to known compromise; 90 days is the industry midpoint (AWS KMS recommends 365d, GCP KMS supports 30d/90d/365d defaults, NIST SP 800-57 recommends 1-2 years for signing keys — 90d is a conservative default that operators can lengthen at config time when key rotation lands).
- **Overlap window:** 24 hours. JWTs default to 1h access lifetimes (slice 188 will lock this); a 24h overlap window is 24× the access-token TTL and gives every refresh-token holder a window to obtain a new access token signed under the new key.
- **JWKS cache TTL:** 1 hour (`Cache-Control: max-age=3600` on the JWKS response). Verifiers MUST re-fetch JWKS hourly; during the 24h overlap window, every verifier will see both keys at least 24× before the old key disappears.
- **Existing token treatment:** tokens signed with the rotated-out key keep working until natural expiry (≤ 1h after rotation completes). NO revocation on rotation. Rotation is for forward security, not for incident response — incident response uses the `/oauth/revoke` endpoint (slice 190).

**Confidence:** medium.

**Revisit if:** a customer demands shorter-than-24h overlap (more aggressive rotation against an active threat model) or longer-than-90d cadence (less operator burden). Both are config-time knobs in the future rotation slice.

### D5 — JWKS + OIDC discovery routing: **direct `root.Get` on the existing chi router**

**Options considered:**

- Mount the OAuth routes on a sub-router via `root.Mount("/.well-known", ...)` and `root.Mount("/oauth", ...)`.
- Register routes directly on the existing root router via `root.Get(...)` and `root.Post(...)`.

**Chosen:** direct registration on the root router.

**Rationale:** the established parallel-batch convention (slices 014, 017, 018, 019, 024, 036, 009) is to avoid a second `chi.NewRouter().Mount("/", ...)` because chi panics on a double Mount at the same path. The other top-level routes (`/health`, `/v1/version`, `/v1/install-state`, `/v1/anchors/export`) all use direct registration. Slice 187 follows the same pattern. The handler's `Mount(r chi.Router)` method takes any `chi.Router` so a future slice can sub-mount if it wants — but in v1 the routes are wired directly.

**Confidence:** high.

**Revisit if:** the `/.well-known/` route set grows large enough that sub-routing becomes cleaner. With only two routes today (JWKS + discovery), the flat registration is the right shape.

## Revisit once in use

- **Re-evaluate D1 (ES256)** if an enterprise customer's IdP cannot verify ES256 signatures.
- **Re-evaluate D2 (go-jose)** if a CVE or EoL announcement lands.
- **Re-evaluate D4 (rotation strategy)** when the rotation slice actually ships — the 90d / 24h numbers are starting points, not commitments.
- **Re-evaluate `id_token_signing_alg_values_supported`** in the discovery doc when slice 188 ships `/oauth/token` and the first IdTokens get issued — if multi-alg support is needed, the JWKS handler already supports multi-key but the discovery doc currently advertises a single algorithm.
- **Re-evaluate the keystore filesystem default path** (`/var/lib/security-atlas/keys/`) once the docker-compose self-host bundle's volume layout (slice 037) intersects with the keystore — a Helm chart upgrade may want a different convention.
- **Re-evaluate the 501 stub `slice_pending` response shape** when slice 188 lands the real `/oauth/token`. The current shape (`{"error":"slice_pending","slice":"188"}`) is convenient for spine development but not RFC 6749 §5.2-shaped. The future real handler returns RFC 6749-shape errors (`{"error":"invalid_grant", ...}`); the 501-stub shape stays in place only until the future slice ships, then disappears entirely.

## Confidence summary

| Decision                                       | Confidence |
| ---------------------------------------------- | ---------- |
| D1 — ES256 signing                             | high       |
| D2 — go-jose/v4                                | high       |
| D3 — `ATLAS_KEYSTORE_PATH` env var             | high       |
| D4 — 90d rotation cadence + 24h overlap window | medium     |
| D5 — direct chi `root.Get` routing             | high       |
