# Slice 325 — Decisions log

> JUDGMENT slice. Implementing agent records the build-time calls inline; maintainer iterates post-deployment per the JUDGMENT-slice discipline in `Plans/prompts/04-per-slice-template.md`.

## D1. File-location choice — `docs-site/docs/oauth-grants.md`

**Decision.** Land the landing map at `docs-site/docs/oauth-grants.md` (published mkdocs site) with a nav entry under the top-level `OAuth grants map`. Reject the alternative `docs/oauth-grants-map.md` (internal-only).

**Why.** The reviewer framing ("future contributors will benefit") and the actual audience for a "where does each grant land" page is two-fold: community contributors browsing the substrate, AND self-hosting operators debugging an auth issue. Both groups read `docs-site/` rather than `docs/`. The slice's "Notes for the implementing agent" section recommended this path; the on-disk reality of `docs-site/docs/` (already carries `tenant-membership.md`, `connector-authoring.md`, and other contributor + operator references) confirms it.

**Trade-off.** A `docs-site/` doc is mkdocs-built and goes through the strict-link-check. The doc has to keep its cross-references in sync with the published site's link layout (relative paths to `docs/adr/0003-oauth-authorization-server.md` are `../../docs/adr/...`). Acceptable cost.

**Confidence:** high.

## D2. mkdocs nav placement — adjacent to `REST API reference`

**Decision.** Add the nav entry as a top-level `OAuth grants map: oauth-grants.md` directly after `REST API reference: api/index.md`. Reject placement inside a new `Reference:` parent (would require restructuring three existing top-level entries) and reject placement under `Design decisions:` (the map is reference, not a decision record — the decision record is ADR-0003).

**Why.** The existing IA has flat top-level entries with two-level nesting only where multiple sibling docs exist (`Measuring your program`, `Primitives`, `Walkthroughs`, `Troubleshooting`, `Design decisions`). A single new reference doc most naturally sits as a peer of the existing reference doc (`REST API reference`). Future expansion into a `Reference:` parent is reversible if more reference docs land.

**Confidence:** high.

## D3. Endpoint coverage divergence from AC-2 expected list

**Decision.** The crawl of `internal/api/oauth/*.go` resolved AC-2's expected list as follows:

| AC-2 expected entry                                                         | Status in `main`                                                                    | Disposition                                                                               |
| --------------------------------------------------------------------------- | ----------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------- |
| `/oauth/authorize` (RFC 6749 §4.1)                                          | Implemented                                                                         | Included.                                                                                 |
| `/oauth/token` `grant_type=authorization_code`                              | Implemented                                                                         | Included.                                                                                 |
| `/oauth/token` `grant_type=password`                                        | **NOT implemented**                                                                 | Omitted. Documented under "Grants intentionally omitted" with the reason.                 |
| `/oauth/token` `grant_type=refresh_token`                                   | **NOT implemented**                                                                 | Omitted. Documented under "Grants intentionally omitted" with the reason.                 |
| `/oauth/token` `grant_type=urn:ietf:params:oauth:grant-type:device_code`    | Implemented                                                                         | Included.                                                                                 |
| `/oauth/token` `grant_type=urn:ietf:params:oauth:grant-type:token-exchange` | Implemented                                                                         | Included.                                                                                 |
| `/oauth/token` `grant_type=client_credentials`                              | Implemented (the slice spec didn't pre-list this but the code has it)               | Included.                                                                                 |
| `/oauth/device_authorization`                                               | Implemented                                                                         | Included.                                                                                 |
| `/oauth/device_approve` (slice doc shorthand)                               | Implemented at `/oauth/device_authorization/approve` (constant `PathDeviceApprove`) | Included with the actual registered path.                                                 |
| `/oauth/device_authorization/deny` (additional surface not in AC-2)         | Implemented                                                                         | Added. AC-2 explicitly permits adding endpoints the crawl finds beyond the expected list. |
| `/oauth/revoke`                                                             | Implemented                                                                         | Included.                                                                                 |
| `/oauth/introspect`                                                         | Implemented                                                                         | Included.                                                                                 |
| `/.well-known/openid-configuration`                                         | Implemented                                                                         | Included.                                                                                 |
| `/oauth/jwks.json` (slice doc shorthand)                                    | Implemented at `/.well-known/jwks.json` (constant `PathJWKS`)                       | Included with the actual registered path.                                                 |

The slice doc explicitly allows omitting expected-list entries that are not in `main` ("If any in this list are not present in `main`, omit them (no aspirational rows)"). The `password` and `refresh_token` grants are absent from `token.go`'s `switch r.FormValue("grant_type")` dispatch. `client_credentials` IS present and is included even though the AC-2 prose did not pre-list it (the AC-2 list said "at minimum").

**Confidence:** high. The dispatch in `internal/api/oauth/token.go` lines 184-198 enumerates exactly four grant_type cases plus the unsupported-grant fallback.

## D4. AC-5 claim list — five atlas-namespaced claims, not four

**Decision.** The `jwt.AtlasClaims` struct in `internal/auth/jwt/claims.go` defines FIVE atlas-namespaced claims, not four. The slice's AC-5 listed `atlas:current_tenant_id`, `atlas:available_tenants`, `atlas:roles`, `atlas:super_admin`. The actual struct also carries `atlas:idp_issuer` (the upstream OIDC issuer that authenticated the human). The landing map documents all five so the reference matches reality.

**Why.** AC-5's intent is "document the JWT claim shape accurately." A four-claim subsection that omits `atlas:idp_issuer` would be a partial truth and would mislead the contributor a year from now. The struct definition is the canonical source per the package doc comment: "Custom claims (locked at canvas OQ #21 Reading D, 2026-05-20)" — and OQ #21's resolution explicitly includes `atlas:idp_issuer` as one of the locked claims.

**Confidence:** high. Verified by direct read of `internal/auth/jwt/claims.go` lines 42-52.

## D5. Common-validator symbol names — corrected from AC-4 expected names

**Decision.** AC-4 named symbols that don't exist verbatim in the substrate. Corrections, all verified by direct read:

| AC-4 expected symbol                  | Actual symbol in code                                                                                                | File                                       |
| ------------------------------------- | -------------------------------------------------------------------------------------------------------------------- | ------------------------------------------ |
| `oauthclient.Authenticate`            | `oauthclient.Store.Verify` (also `Store.Lookup` for no-secret paths, `Store.Issue` for provisioning)                 | `internal/auth/oauthclient/oauthclient.go` |
| `pkce.VerifyChallengeS256`            | `computePKCEChallengeS256` + `constantTimeEqualString` (both file-local in `internal/api/oauth/pkce.go`)             | `internal/api/oauth/pkce.go`               |
| `tokensign.Sign` / `tokensign.Verify` | `tokensign.Signer.Sign`, `tokensign.Signer.Verify` (methods on the `Signer` value returned by `tokensign.New`)       | `internal/auth/tokensign/tokensign.go`     |
| `keystore.ActiveKey`                  | `keystore.KeyStore.Get` (returns active `SigningKey` + full `[]VerificationKey` set in one call)                     | `internal/auth/keystore/keystore.go`       |
| `jwtmw.Middleware`                    | `jwtmw.Middleware(signer, revoked, opts)` — three-argument constructor returning a `func(http.Handler) http.Handler` | `internal/auth/jwtmw/middleware.go`        |

AC-4's intent ("Confirm the actual package + symbol names by `rg` against `internal/auth/` and `internal/api/oauth/` before publishing") explicitly licensed this correction. The landing map uses the actual symbols so `rg` against the listed names returns hits.

**Confidence:** high. Verified by direct `grep` against each named package.

## D6. RFC-section citation choices

**Decision.** Each row cites the most specific RFC section that defines the wire shape of that grant:

| Grant / endpoint                                | Cited RFC section           | Why this section                                                                                                                              |
| ----------------------------------------------- | --------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------- |
| `/oauth/authorize`                              | RFC 6749 §4.1 + RFC 7636 §4 | §4.1 is the authorization-code-grant top section; §4 is PKCE's protocol definition. AC-2 + the slice doc both cite these sections explicitly. |
| `/oauth/token` `authorization_code`             | RFC 6749 §4.1.3             | §4.1.3 is the access-token-request half of the authorization-code grant.                                                                      |
| `/oauth/token` `client_credentials`             | RFC 6749 §4.4               | §4.4 is the Client Credentials Grant definition.                                                                                              |
| `/oauth/token` `token-exchange`                 | RFC 8693 §2.1               | §2.1 is the token-exchange request definition. Slice doc explicitly cited this section.                                                       |
| `/oauth/token` `device_code`                    | RFC 8628 §3.4               | §3.4 is the device access-token request half. (§3.1 is the device-authorization request, §3.2 the response, §3.5 the polling rules.)          |
| `/oauth/device_authorization`                   | RFC 8628 §3.1, §3.2         | §3.1 is the request; §3.2 is the response shape. Both ship.                                                                                   |
| `/oauth/device_authorization/approve` + `/deny` | atlas-internal (not RFC)    | Explicitly NOT in RFC 8628 per `device_approve.go` top-of-file comment. Labeled as atlas-internal.                                            |
| `/oauth/revoke`                                 | RFC 7009 §2.1               | §2.1 is the revocation request shape. §2.2 (response semantics — 200 for unknown tokens) is referenced inline in the Notes column.            |
| `/oauth/introspect`                             | RFC 7662 §2.1               | §2.1 is the introspection request shape; §2.2 (response semantics — `{"active": false}`) is referenced inline in the Notes column.            |
| `/.well-known/openid-configuration`             | OIDC Discovery 1.0          | OIDC Discovery is not an RFC; cited by its specification name.                                                                                |
| `/.well-known/jwks.json`                        | RFC 7517 JWKS               | RFC 7517 defines the JWKS format.                                                                                                             |

**Why.** AC-2 and the slice's "RFC citation discipline" note both require section-level citations. Where the slice doc supplied a specific section, the map uses it; where multiple sections apply, the map cites the request-shape section in the main column and references the response-semantics section inline in the Notes column.

**Confidence:** high for the request-shape citations (these are the canonical defining sections per the RFCs). Medium for the choice to put response-semantics citations inline rather than as their own column — that's a layout call, not a correctness call.

## D7. Spillover identified — OpenAPI spec is missing every OAuth endpoint

**Decision.** `docs/openapi.yaml` does not enumerate ANY of the OAuth endpoints. Per the slice's spillover discipline ("An endpoint that's registered in Go but missing from `docs/openapi.yaml` → file a separate small slice. Do NOT bundle into this PR"), this finding is recorded here and noted in the landing map under "Endpoints not in the OpenAPI spec." A separate follow-on slice should be filed before this PR merges (or as a soon-after item).

**Action item.** File a small spillover slice: "OpenAPI spec drift: OAuth endpoints not enumerated in `docs/openapi.yaml`." Parent: slice 325. Type: docs (likely JUDGMENT — choosing operationId names and request/response schemas requires reading every handler). Estimate: 0.5d-1d depending on schema-precision target.

**Confidence:** high that the gap exists (verified by `grep -in "oauth\|jwks\|openid\|well-known" docs/openapi.yaml` returning zero hits). Medium-confidence on the spillover scope estimate; the actual line count and schema density will determine the size.

## D8. ADR-0003 pointer placement — References section

**Decision.** The slice's AC-6 says "add a one-line `See also:` pointer at the bottom of its 'Decision' or 'Implementation' section." ADR-0003 does not have an "Implementation" section, and putting a `See also:` line inside the "Decision" section would interrupt the substantive decision text. The pointer was instead added at the bottom of the `References` section (after the RFC links), where existing readers already look for cross-references. The line is verbatim short ("See also: …") so the AC-6 intent (a discoverable pointer, not a structural change) is met.

**Why.** Reads cleanly. Existing References section already lists ADR-0002, slice issue docs, and RFCs as cross-references; the landing map fits the same shape. Adding a "See also:" inside the Decision section would feel like a footnote in the middle of a paragraph.

**Confidence:** high. The alternative (a dedicated "See also" subsection between References and the footer-navigation row) would have been overkill for one link.

## Confidence summary

| Decision                                                                                         | Confidence                                      |
| ------------------------------------------------------------------------------------------------ | ----------------------------------------------- |
| D1. File location → `docs-site/docs/oauth-grants.md`                                             | high                                            |
| D2. mkdocs nav placement adjacent to REST API reference                                          | high                                            |
| D3. Endpoint coverage divergence (password + refresh_token omitted, client_credentials included) | high                                            |
| D4. AC-5 → document five atlas-namespaced claims, not four                                       | high                                            |
| D5. Common-validator symbol-name corrections                                                     | high                                            |
| D6. RFC-section citation choices                                                                 | high (citations) / medium (Notes-column layout) |
| D7. OpenAPI spec drift identified as spillover (not fixed in this PR)                            | high (gap exists) / medium (scope estimate)     |
| D8. ADR-0003 pointer at References section                                                       | high                                            |
