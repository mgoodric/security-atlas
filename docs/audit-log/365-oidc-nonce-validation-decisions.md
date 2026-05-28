# Slice 365 — OIDC ID-token nonce generation + validation — decisions log

**Parent slice:** 327 (`docs/audit-log/327-security-audit-security-auditor-decisions.md`) finding **H-1** (severity High)
**Branch:** `auth/365-oidc-nonce-validation`
**Type:** JUDGMENT (per `Plans/prompts/04-per-slice-template.md` slice types)
**Date:** 2026-05-28

This slice closes the High-severity H-1 finding from the slice 327
security audit: the OIDC Relying-Party flow in
`internal/auth/oidc/oidc.go` previously generated and validated
`state` + PKCE `code_verifier` but did NOT generate or validate the
OIDC `nonce` parameter. OIDC Core §3.1.2.1 mandates `nonce` for ID
Token replay protection on the authorization code flow; RFC 9700
§4.5.3 repeats the requirement. The gap was a textbook
spec-mandated defense missing from the v1 auth substrate — exactly
the finding a sophisticated third-party security review would
surface.

This log captures the build-time judgment calls.

---

## D1 — New sentinel `ErrNonceMismatch` (not reusing `ErrStateMismatch`)

**Decision:** Add a **new** error sentinel `ErrNonceMismatch` instead
of reusing the existing `ErrStateMismatch` for nonce-related
failures.

**Rationale.**

1. **Forensic clarity at the audit-log layer.** A user-visible 400
   response is identical for both classes, but the audit-log row
   carrying the sentinel name lets an operator triage whether the
   failure was a CSRF attempt (`ErrStateMismatch` — browser-side
   issue, possibly user reload or extension interference) or an
   ID-token-replay attempt (`ErrNonceMismatch` — more sinister, may
   indicate upstream IdP cache compromise). The two operational
   responses are different — the second warrants pagerduty, the
   first usually doesn't.

2. **Future-proofing.** If we later wire different HTTP status
   codes or different rate-limiting buckets per failure class, the
   type-level distinction is already in place. Folding both into
   `ErrStateMismatch` would force a later refactor to disentangle.

3. **Cost is one line.** A two-line `errors.New` declaration plus
   updated handler-side `errors.Is` checks (in the api/auth handler,
   if/when added) — no downside.

**Trade-off acknowledged.** Slice AC-3 left this an engineer-call
("`ErrStateMismatch` OR a new `ErrNonceMismatch`"); the slice author
expressed neutral preference. The PR body surfaces this for the
maintainer's awareness.

---

## D2 — Manual claim comparison after `verifier.Verify`, NOT `Config{Nonce: ...}`

**Decision:** After `verifier.Verify(ctx, rawIDToken)` returns
`*coreos.IDToken`, manually compare `idTok.Nonce` against the cookie
value. Return `ErrNonceMismatch` on mismatch.

**Why this diverges from slice AC-4 verbatim.** Slice AC-4 reads:

> `HandleCallback` builds the go-oidc verifier with
> `Config{ClientID: cfg.ClientID, Nonce: <cookie value>}` so the
> library rejects ID tokens whose `nonce` claim doesn't match.

The `go-oidc/v3` pinned version in `go.mod` is **v3.18.0**.
Inspecting the upstream source:

- `Config` struct (`vendor → oidc/oidc.go`): exposes
  `ClientID`, `SupportedSigningAlgs`, `SkipClientIDCheck`,
  `SkipExpiryCheck`, `SkipIssuerCheck`, `Now`,
  `InsecureSkipSignatureCheck`. **No `Nonce` field.**
- `verify.go:189`: explicit comment — `"Verify does NOT do nonce
validation, which is the callers responsibility."`
- `verify.go:336`: the library exposes `oidc.Nonce(nonce)
oauth2.AuthCodeOption` — this is for the **authorize URL** side
  (which we do use in `BeginLogin`), not for verification.

The slice author's mental model likely tracked an earlier or
different OIDC library (or a hypothesized API). The SPIRIT of AC-4
— "the verifier path rejects ID tokens whose nonce claim doesn't
match" — is fully satisfied by the manual post-`Verify` claim
comparison: `verifier.Verify` parses the ID token (so `idTok.Nonce`
is populated from the parsed claim), and we immediately compare it
against the cookie value. Mismatch returns `ErrNonceMismatch`
before any user upsert / session establishment.

The PR body surfaces this divergence for the slice author so the
canvas reference to the go-oidc nonce API can be tightened on a
future docs pass if desired.

---

## D3 — Integration-test fixture: in-process httptest + go-jose-signed tokens (no Postgres)

**Decision:** Build the integration test on top of an in-process
`httptest.Server` that serves the minimum OIDC discovery surface
(`/.well-known/openid-configuration` + `/jwks` + `/token`) and mint
ID tokens locally via `github.com/go-jose/go-jose/v4` (already a
project dep, pinned by slice 187 D2). Skip the Postgres-backed
integration harness pattern used elsewhere in `internal/auth/`.

**Rationale.**

1. **The unit under test has no DB interaction.** `BeginLogin` and
   `HandleCallback` use an injected `IdpResolver` interface and
   never touch `sql.DB` / `pgxpool.Pool`. A Postgres-backed test
   would test the resolver shim, not the nonce verification path.

2. **CI parity preserved.** The test still carries the
   `//go:build integration` tag so it runs in the
   `Go · integration (Postgres RLS)` job alongside the rest of the
   integration suite. The job's name mentions Postgres-RLS but the
   `go test -tags=integration ./internal/...` command picks up any
   `integration`-tagged test in the tree — including this one.

3. **Latency budget.** RSA 2048 keygen + 3 httptest servers + 3
   discovery + token round-trips clock under 0.5s wall-clock on a
   laptop. Postgres-backed tests in the same job are >2s each.

**Implementation note.** The fixture mints tokens with a single
configurable nonce field (`idp.mintNonce`), so the three test cases
(mismatch / match / state-still-enforced) all share one fixture
construction and toggle only what they care about.

---

## D4 — Reuse `randomState()` helper for nonce generation

**Decision:** Use the existing `randomState()` package-private
helper to generate the nonce, the same way the state value is
generated. The helper does 16-byte `crypto/rand` + base64-url
encoding — exactly what the slice prescribes.

**Rationale.**

1. **P0-365-5 compliance.** The anti-criterion requires
   `crypto/rand`. The helper uses `crypto/rand` directly
   (`oidc.go:280`). Reusing the helper guarantees the same
   randomness source as the existing state — one less place to
   regress.

2. **DRY.** Two identical 16-byte crypto/rand+base64 paths would be
   noise; one helper handles both.

3. **No risk of nonce/state collision.** `randomState()` returns a
   fresh 16-byte value on each call; the cryptographic distance
   between two consecutive calls is the keyspace (2^128). For
   practical purposes the two values are guaranteed distinct.

---

## Engineer-as-collaborator: adjacent OIDC defenses noted (NOT touched in this slice)

Per the slice-365 engineer-as-collaborator brief
("if you spot another spec-mandated OIDC defense that's missing,
do NOT touch in this slice — but note in PR body"):

1. **`at_hash` claim verification.** OIDC Core §3.1.3.6 — when an
   access_token is returned alongside the ID token in the
   authorization code flow with response_type=code, the ID token
   MAY include an `at_hash` claim that binds the access token to
   the ID token. The current RP does not verify `at_hash`. Atlas
   does not currently use the access token (only the ID token),
   but if a future slice exchanges the access token for IdP-side
   user-info or back-channel API access, `at_hash` verification
   becomes a load-bearing replay defense.

2. **Strict issuer-claim binding to IdpConfig.IssuerURL.**
   go-oidc's `verifier.Verify` does check issuer match by default
   (unless `SkipIssuerCheck` is set, which we don't). This is
   honored — no action needed.

3. **`max_age` + `auth_time` claim binding.** OIDC Core §3.1.2.1
   permits an RP to send `max_age=N` in the authorize URL and
   verify the IdP's returned `auth_time` is within the window. Not
   currently implemented. Low-risk; revisit if compliance work
   (FedRAMP step-up auth) lands.

4. **JARM (JWT-secured Authorization Response Mode).** Hardens the
   authorize-response transport against query-string mutation.
   Not yet implemented; lower priority than at_hash given the
   threat model.

These are flagged in the PR body for the slice author's awareness;
none are addressed here.

---

## Verification surface summary

| Surface                                                               | Coverage                                                                                                                                                                   |
| --------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Go unit (`./internal/auth/oidc`, no tag)**                          | 8 tests — sentinel distinctness, cookie name + MaxAge lock, BeginLogin nonce-cookie shape, authorize-URL nonce param, state+PKCE preservation, resolver-error propagation. |
| **Go integration (`./internal/auth/oidc`, `//go:build integration`)** | 3 tests — nonce mismatch rejected, nonce match accepted, state still enforced when nonce matches. The first is the explicit H-1 RED→GREEN tracer bullet.                   |
| **`go build ./...`**                                                  | Clean.                                                                                                                                                                     |
| **`pre-commit run --all-files`**                                      | Run pre-push (per `feedback_local_ci_parity` memory).                                                                                                                      |

---

## References

- `docs/issues/365-oidc-nonce-validation.md`
- `docs/audits/327-security-audit-security-auditor-report.md` finding H-1
- OIDC Core 1.0 §3.1.2.1 (Authentication Request — nonce)
- RFC 9700 (OAuth 2.0 Security Best Current Practice) §4.5.3
- go-oidc v3.18.0 `verify.go` (line 189 "Verify does NOT do nonce validation"; line 336 `Nonce()` helper)
- `internal/auth/oidc/oidc.go` (modified)
- `internal/auth/oidc/oidc_test.go` (added)
- `internal/auth/oidc/oidc_nonce_integration_test.go` (added)
