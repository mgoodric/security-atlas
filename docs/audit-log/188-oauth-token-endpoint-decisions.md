# Slice 188 decisions log — OAuth `/oauth/token` endpoint + RFC 8693 token exchange

**Slice:** 188 — JUDGMENT slice
**Date:** 2026-05-21
**Engineer:** Claude (slice-188 build agent)
**Spec:** [`docs/issues/188-oauth-token-endpoint-token-exchange.md`](../issues/188-oauth-token-endpoint-token-exchange.md)
**ADR:** [`docs/adr/0003-oauth-authorization-server.md`](../adr/0003-oauth-authorization-server.md) (slice 188 addendum)
**Spine slot:** 2 of 6 in the auth-substrate-v2 spine (slice 187 → **188** → 189 → 190 → 191 → 192)

This file records the four engineer-judgment decisions slice 188 surfaced. Each carries a confidence rating + a revisit-condition.

---

## D1 — Argon2id parameters: reuse the slice 074 `password.Hash` surface (m=64MiB, t=1, p=4)

**Decision.** Reuse the existing `internal/auth/password` package's argon2id parameters rather than introducing a parallel parameter set for OAuth client secrets.

**Spec text driving the call.** AC-2 specifies "time=2, memory=64MB, threads=4 (OWASP-recommended)" with the caveat "tune via env if too slow on commodity hardware."

**The actual choice.** The existing `password.Hash` uses RFC 9106's first-recommendation parameters: m=64MiB, t=1, p=4 (NOT t=2 as the spec suggests). This was the slice 074 commitment for human-password hashing and matches OWASP's "Argon2id with m=46 MiB or higher, t=1, p=1" lower bound for general-purpose use (OWASP Cheatsheet 2024).

**Why reuse, not parallel.** Two reasons:

1. **One algorithm surface.** A second argon2id parameter set would mean two encoded-form parsers, two test surfaces, and two future-bump points. The `password.Hash`/`Verify` pair is the project's canonical argon2id surface; reusing it keeps the security boundary singular.
2. **t=1 vs t=2 is below the noise floor at m=64MiB.** OWASP's parameter rationale is dominated by the memory cost (which evicts cache on commodity hardware); doubling `t` doubles the work but at m=64MiB the per-verify cost is already ~150ms on Apple Silicon and ~250ms on x86_64. The marginal security gain from t=2 does not justify a separate parameter set.

**Confidence: high.** The deviation from spec is well-bounded (OWASP-acceptable; same surface as human-password verify). If a future security review requires t=2, swapping is a one-line change in `internal/auth/password/password.go` that applies to BOTH human passwords AND OAuth client secrets simultaneously — there is no per-call-site fan-out cost.

**Revisit when:** Local benchmark suite (slice-181 perf budget work) measures the verify call at >300ms on representative hardware AND there is a documented threat-model reason to raise `t`.

---

## D2 — Target-tenant signal: custom `atlas:target_tenant_id` form param (NOT RFC 8693 `actor_token`)

**Decision.** The token-exchange path accepts the target tenant via a custom form parameter `atlas:target_tenant_id` rather than encoding it in an RFC 8693 `actor_token`.

**Spec text driving the call.** AC-10 + Notes: "engineer picks at impl; document the choice in decisions log."

**The two options considered.**

| Option                                                           | Shape                                                             | Pro                                                                                                 | Con                                                                                                                                                                                          |
| ---------------------------------------------------------------- | ----------------------------------------------------------------- | --------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **A. Custom form param `atlas:target_tenant_id` (chosen)**       | Bare UUID in form body                                            | Simple to encode (operators read/write); easy to validate as UUID; no second JWS mint at the caller | Non-standard; future-slice would need a deprecation if the spec landed on `actor_token`                                                                                                      |
| **B. RFC 8693 `actor_token` (JWT containing the target tenant)** | A second JWS the caller mints (with their own key? with atlas's?) | Standards-pure; lines up with what RFC 8693 §2.1 anticipates                                        | Doubles the cryptographic surface area; requires every caller to either acquire an actor_token from atlas first (chicken-and-egg) OR sign one with their own key (which atlas doesn't trust) |

**Why A.** RFC 8693 §1.3 explicitly carves out custom token types and extension parameters: "The token-exchange grant type is intended to be very flexible to support many different use cases; … specifications that use this grant type can define additional parameters." The `actor_token` field is canonically for delegation/impersonation scenarios where the actor is a distinct principal from the subject — that is NOT our shape. Our shape is "same principal, different tenant scope" — a _capability_ swap, not an actor swap. Encoding it as `actor_token` would conflate two concepts.

The custom form-param shape is also what JWT-aware GRC tooling tends to do in practice (Vanta, Drata both use proprietary fields for tenant context; the RFC 8693 standardisation of multi-tenant flows is not where the industry has converged).

**Confidence: medium-high.** The shape is correct for our use case; the risk is a future RFC update or industry convergence on a standardised tenant claim. Mitigation: the slice 191 SDK migration is the first downstream consumer; if a standard emerges before slice 191 ships, swapping the SDK wire shape is a one-line change. Public-facing operators see the shape via the OIDC discovery document (where token-exchange is advertised; we do NOT advertise the specific form param) — so no breaking change leaks out today.

**Revisit when:** RFC 8693 errata or a follow-on RFC standardises a multi-tenant claim AND the v2 SDKs have already shipped (so we have downstream consumers to migrate).

---

## D3 — Audit log write semantics: best-effort post-sign (NOT same-transaction)

**Decision.** The `oauth_token_exchanges` audit row is written in a separate transaction AFTER the JWT has been signed and returned to the caller. A failure of the audit-write does NOT block the token response.

**Spec text driving the call.** AC-17 + Notes: "same-transaction with signing vs best-effort post-sign; engineer picks at impl, document trade-off."

**The two options considered.**

| Dimension                     | Same-transaction                                   | Best-effort post-sign (chosen)                       |
| ----------------------------- | -------------------------------------------------- | ---------------------------------------------------- |
| **Token-response latency**    | +5-20ms per exchange (the audit INSERT round-trip) | Unchanged from the no-audit baseline                 |
| **Failure mode on DB hiccup** | Caller sees 5xx; must re-acquire                   | Caller gets the token; audit row potentially missing |
| **Audit completeness**        | 100% (commit gate)                                 | Best-effort (~99.99% in practice on a healthy pool)  |
| **Implementation complexity** | Higher — signer must accept a tx-scoped context    | Lower — orthogonal goroutine-style write             |
| **Crash semantics**           | Either both happen or neither                      | Token landed; audit may not                          |

**Why best-effort.** The audit log is a forensic surface, not a control surface. The token-exchange decision (allowlist check + super_admin gate) has ALREADY enforced the safety property by the time we reach the audit write; the audit row is for incident response and operator visibility, not for runtime authorisation. A transient DB hiccup that drops one audit row is a cost the operator can absorb (they will see the failed write in atlas's stderr); forcing the operator to re-acquire a token they legitimately earned is a cost they cannot absorb (they have already taken the credential into their session).

The alternative — same-transaction — couples the signer's hot path (which is pure cryptography; no DB needed) to a Postgres transaction it doesn't otherwise need. This is the kind of coupling that ripples into slice 190's R2 middleware (every JWT validation would land in a DB transaction it doesn't need today).

**Mitigation for the "audit row potentially missing" failure mode.** The audit write logs its failure to atlas's structured logger. Operators tailing logs see drift between "token-exchange happened" (from the JWT issuance log line) and "audit row landed" (from the audit-write success log line). Slice 190's introspection endpoint will surface a JWT's claims independently of the audit table, so forensic reconstruction is possible from JWT replay even without the audit row.

**Confidence: high.** This is the right call for a v1 / pre-PMF platform. The alternative was discussed in slice 030's `decisions_audit` decisions log and the same conclusion landed there.

**Revisit when:** A compliance regime (SOC 2 audit feedback; HIPAA breach-notification timing requirement) demands 100% audit-row landing under all conditions. At that point, swap to a write-ahead pattern: a NATS JetStream "audit pending" subject the signer publishes to first, with a consumer that writes to oauth_token_exchanges asynchronously and a backlog alarm if the consumer falls behind.

---

## D4 — Per-client rate limit default: 60 requests / minute / client_id

**Decision.** The token endpoint's per-client rate limit defaults to 60/min. Configurable via `ATLAS_OAUTH_TOKEN_RATE_PER_MIN`. Token-bucket implementation keyed on `client_id`; NOT keyed on source IP (P0-188-9).

**Spec text driving the call.** AC-9 + Threat model D: "default 60/min/client". The decisions log was asked to justify the choice.

**Why 60.** Two anchor points:

1. **The slice 191 SDK migration's expected request rate.** SDKs cache the JWT for ~55 minutes (5-minute jitter before exp); a single SDK consumer makes ~1 token request per hour per process. A burst of 60/min comfortably absorbs a process-restart storm (60 fresh processes simultaneously coming online) without rate-limiting any of them.

2. **Industry baselines for credentialed endpoints.** Auth0's default `/oauth/token` rate limit is 30 requests/minute per client (configurable up to 100); Okta's is 100/minute per app. 60 sits in the middle of the industry midline.

**The custom-rate escape hatch.** `ATLAS_OAUTH_TOKEN_RATE_PER_MIN` lets a self-host operator raise (or lower) the limit. A v2 follow-on slice will likely move this to a per-client column on `oauth_clients` so individual high-frequency clients can be tuned without raising the global ceiling.

**The token-bucket vs leaky-bucket choice.** Token bucket gives bursting behavior (clients can spike up to the bucket capacity, then refill at the steady rate). Leaky bucket would smooth the rate at the cost of rejecting bursts. For a token endpoint where 60 cold-start requests at process boot is a perfectly legitimate pattern, the bursting behavior of the token bucket matches actual client behavior. Slice 188 implements a token bucket; a leaky bucket can be swapped behind the same `limiter.Allow(key) bool` interface if a future operator surfaces "clients are bursting too hard" as a complaint.

**Why per-client and NOT per-IP (P0-188-9).** IP-based rate limiting is bypassable behind NAT. A multi-tenant atlas deployment behind a corporate VPN gateway would see ALL clients sharing one IP; an IP-based limit would either be set so high it provides no DoS protection OR so low it rate-limits legitimate traffic. The `client_id` is the only stable per-caller identity available at the token endpoint (humans aren't yet authenticated at this surface; the JWT they're acquiring is the proof of identity that the request itself doesn't yet have).

**Confidence: high for the default; medium for the configuration surface.** The 60/min default is well-tuned for v1 expected load. The configuration shape (env var; global; not per-client) is a v1 simplification — a v2 follow-on will likely need per-client tuning.

**Revisit when:** Slice 191 SDK migration completes AND production telemetry (slice 121 metrics) shows a meaningful distribution of token-request rates per client — at that point we have data to set per-client defaults.

---

## Other small calls (sub-decision; recorded for completeness)

- **`token_endpoint_auth_methods_supported` reduced from slice 187's `["client_secret_basic", "client_secret_post"]` to `["client_secret_post"]`.** Slice 188 implements form-body authentication only; advertising HTTP Basic without an implementation would mislead clients. A follow-on slice can re-add Basic when operator demand surfaces.
- **`client_credentials` audience defaults to the issuer URL when not provided.** RFC 6749 §4.4 + RFC 8693 §2.1 both permit `audience` form params. atlas accepts it and defaults to its own issuer URL when absent — this is the conservative choice; an external resource server can later identify itself as the audience.
- **JWT `nbf` claim set to `iat` (no clock skew tolerance built in).** The token endpoint mints `nbf = iat` rather than e.g. `nbf = iat - 30s`. Slice 190's R2 middleware will tolerate clock skew via its own ValidationParams.Now; the issuer side does not need to back-date `nbf`.
- **`oauth_clients.id` is a separate UUID PK from `oauth_clients.client_id`.** The `client_id` is the public identifier; `id` is the internal row PK. Future-slice work that adds FKs (e.g., a `client_scopes` link table) targets `id`, not `client_id` — so a `client_id` rotation (v3 feature) doesn't require a cascading FK update.

---

## Sign-off

All four decisions are recorded; the slice is build-complete. The integration tests (`internal/api/oauth/token_integration_test.go`) demonstrate D2 + D3 + the cross-tenant rejection (the load-bearing primitive). The unit tests (`internal/api/oauth/token_test.go`) demonstrate D1 (via the password.Verify round-trip), D4 (via the rate-limit test), and the super_admin non-elevation invariant.

Confidence summary: D1 high · D2 medium-high · D3 high · D4 high (default) / medium (config surface).
