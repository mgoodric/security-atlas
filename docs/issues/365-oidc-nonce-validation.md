# 365 — OIDC ID-token nonce generation + validation

**Cluster:** Auth
**Estimate:** 1d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Slice 327's security audit (`docs/audits/327-security-audit-security-auditor-report.md` finding **H-1**, severity **High**) surfaced that the OIDC Relying-Party flow in `internal/auth/oidc/oidc.go` generates and validates `state` + PKCE `code_verifier` but does **NOT** generate or validate the OIDC `nonce` parameter.

OIDC Core §3.1.2.1 mandates `nonce` for ID token replay protection on the authorization code flow. RFC 9700 (OAuth 2.0 Security Best Current Practice) §4.5.3 repeats the requirement. State + PKCE protect different attack classes — they do not substitute for nonce. The gap means a captured-and-replayed ID token, or a token injected via an upstream IdP cache compromise, has no nonce binding to reject it.

The v1 binary success criterion ("survive third-party security review", canvas §6) elevates spec-mandated defenses above their pure exploit-difficulty grade. A textbook OIDC requirement absent from the auth substrate is exactly the finding a sophisticated external auditor will surface.

### What ships

1. **Generate nonce in `BeginLogin`.** A 16-byte crypto/rand value, base64-url encoded, alongside the existing `state` + PKCE `verifier`.
2. **Persist nonce in a flow cookie.** A fourth cookie `atlas_oidc_nonce` mirroring the existing state/verifier/idp cookies (HttpOnly + SameSite=Lax + Path=/auth/oidc + 10-minute MaxAge).
3. **Set nonce on the authorize URL.** Pass `oauth2.SetAuthURLParam("nonce", nonce)` to `AuthCodeURL` (already accepts variadic `oauth2.AuthCodeOption`).
4. **Validate nonce on callback.** Set `Nonce` on the go-oidc `Config{}` so the verifier rejects ID tokens whose `nonce` claim doesn't match the cookie. The library compares `Config.Nonce` against `IDToken.Nonce` automatically.
5. **Clear nonce cookie on success.** Extend `ClearFlowCookies` to include the nonce cookie name.
6. **Integration test (RED first).** A forged ID token whose `nonce` claim does NOT match the persisted cookie value MUST be rejected with `ErrStateMismatch` (or a new `ErrNonceMismatch` — engineer's call). A control test confirms the happy-path still works.

### Why this matters

OIDC nonce is the spec-mandated defense against ID token replay. A GRC product whose central thesis is "survive third-party security review" cannot ship with a missing textbook OIDC requirement.

### Why now

H-1 graded High in the slice-327 audit. The fix is small (≈30 LoC + integration test). Highest-severity finding from a comprehensive read-only audit of the v1-complete substrate.

**Trigger:** filed 2026-05-28 from slice 327 audit.

## Threat model

STRIDE pass:

- **S (Spoofing):** Primary attack class. An attacker possessing a previously-issued ID token (captured via TLS-MITM, IdP log access, or upstream IdP cache compromise) could in principle inject it. Nonce binding makes the replay window single-flow.
- **T (Tampering):** N/A.
- **R (Repudiation):** Adding nonce to the verified-claim set strengthens the audit trail.
- **I (Information disclosure):** N/A.
- **D (Denial of service):** N/A (no quota / amplification concerns).
- **E (Elevation of privilege):** Primary impact. A successfully replayed ID token would grant the attacker the identity claims of the original subject. Nonce closes the gap.

## Acceptance criteria

- [ ] **AC-1.** `Authenticator.BeginLogin` generates a fresh 16-byte crypto/rand-backed nonce per flow and includes it in the returned `LoginResult.Cookies` as `atlas_oidc_nonce`.
- [ ] **AC-2.** The authorize URL returned from `BeginLogin` contains a `nonce=<value>` query parameter matching the cookie value.
- [ ] **AC-3.** `HandleCallback` reads `atlas_oidc_nonce` cookie; missing or empty cookie returns `ErrStateMismatch` (or a new `ErrNonceMismatch`).
- [ ] **AC-4.** `HandleCallback` builds the go-oidc verifier with `Config{ClientID: cfg.ClientID, Nonce: <cookie value>}` so the library rejects ID tokens whose `nonce` claim doesn't match.
- [ ] **AC-5.** `ClearFlowCookies` clears the nonce cookie alongside state/verifier/idp.
- [ ] **AC-6.** Integration test (`oidc_nonce_integration_test.go` with `//go:build integration`): forged ID token with mismatched nonce is rejected; happy path with matching nonce is accepted.
- [ ] **AC-7.** Unit test: `BeginLogin` returns a non-empty nonce in the cookie list; the cookie's MaxAge matches `FlowCookieMaxAge`.
- [ ] **AC-8.** No regression in existing OIDC tests (state + PKCE protections remain unchanged).
- [ ] **AC-9.** `pre-commit run --all-files` passes; CI green.

## Constitutional invariants honored

- **Survive third-party security review (canvas §6).** This slice closes a textbook OIDC requirement that external auditors will flag.
- **Defense-in-depth (canvas §5.4 invariant #6 spirit).** Nonce + state + PKCE compose to cover spoofing, CSRF, and code-injection respectively.
- **Tenant isolation (canvas §5.4).** Untouched; OIDC RP runs before tenant context is established.

## Canvas references

- `Plans/canvas/09-tech-stack.md` — OIDC RP architecture
- `Plans/canvas/01-vision.md` §6 — "survive third-party security review"
- ADR-0003 (OAuth Authorization Server)
- Audit report `docs/audits/327-security-audit-security-auditor-report.md` finding H-1

## Dependencies

- **#198** (OIDC first-install bootstrap) — `merged`. OIDC RP code path is the modified surface.
- **#187-198** auth substrate spine — `merged`. Foundation in place.

## Anti-criteria (P0 — block merge)

- **P0-365-1.** Does NOT remove or weaken existing `state` parameter validation. Nonce is ADDITIVE.
- **P0-365-2.** Does NOT remove or weaken existing PKCE `code_verifier` validation. Nonce is ADDITIVE.
- **P0-365-3.** Does NOT log the nonce value in plaintext (defense-in-depth; same discipline as state/verifier).
- **P0-365-4.** Does NOT bypass nonce validation in any code path. Missing cookie + missing claim must both reject.
- **P0-365-5.** Does NOT use predictable nonce sources (must be `crypto/rand`).
- **P0-365-6.** Does NOT auto-merge.

## Skill mix

- `tdd` — RED-first integration test
- `simplify` — pre-PR quality pass

## Notes for the implementing agent

The go-oidc library v3 supports nonce checking natively: set `Nonce` on the `oidc.Config{}` passed to `Provider.Verifier(...)`. The library compares the claim against the configured value; mismatch returns an error from `verifier.Verify`.

For the `oauth2.SetAuthURLParam` path: confirm the helper accepts variadic options on the version pinned in `go.mod` (`golang.org/x/oauth2`). The fallback is constructing the URL manually with `url.Values{"nonce": [...]}.Encode()` appended.

Severity-grading rationale is in `docs/audit-log/327-security-audit-security-auditor-decisions.md` §D6 (High, not Medium, with calibration caveat).
