# 339 — OpenAPI spec drift: 12 OAuth endpoints not enumerated in `docs/openapi.yaml`

**Cluster:** Docs / Infra
**Estimate:** 0.5-1d
**Type:** JUDGMENT
**Status:** `ready` (header corrected 2026-06-14 by slice 339 implementation: the prior `merged` was stale/wrong — `docs/openapi.yaml` had zero oauth entries and the routes exist in code; this slice builds it fresh)

## Narrative

Surfaced during slice 325 (OAuth grants landing map), captured as
follow-up per continuous-batch policy.

While building the landing map at `docs-site/docs/oauth-grants.md`,
the engineer cross-referenced the 12 OAuth endpoints registered by
`internal/api/oauth/oauth.go` against the canonical OpenAPI spec
(`docs/openapi.yaml`) and found **zero hits** — none of the OAuth /
JWKS / OIDC-discovery endpoints are documented in the OpenAPI surface.

Verified via:

```bash
grep -in "oauth\|jwks\|openid\|well-known" docs/openapi.yaml
# (no matches)
```

The 12 endpoints (per `internal/api/oauth/oauth.go` Mount lines
~194-226) are:

1. `GET  /.well-known/openid-configuration` — OIDC discovery
2. `GET  /.well-known/jwks.json` — JWKS publication
3. `POST /oauth/token` — RFC 6749 token endpoint (dispatches 4 grants)
4. `GET  /oauth/authorize` — RFC 6749 §4.1.1 authorization endpoint (+ PKCE per RFC 7636)
5. `POST /oauth/authorize` (consent submission)
6. `POST /oauth/device_authorization` — RFC 8628 device authorization endpoint
7. `POST /oauth/device_authorization/verify` — user verification
8. `POST /oauth/device_authorization/approve` — user approval
9. `POST /oauth/device_authorization/deny` — user denial
10. `POST /oauth/revoke` — RFC 7009 revocation
11. `POST /oauth/introspect` — RFC 7662 introspection
12. `POST /oauth/userinfo` — OIDC userinfo

**Disposition:** OpenAPI-spec docs work. No code changes expected.

**Scope discipline.** This slice updates `docs/openapi.yaml` to add
the 12 OAuth endpoint paths + their request/response schemas.

It does NOT:

- Change any code in `internal/api/oauth/` or `internal/auth/`
- Touch the landing map at `docs-site/docs/oauth-grants.md` (slice 325 owns that)
- Touch ADR-0003 (the architectural decision — system-of-record)
- Add new endpoints (the surface is what it is)
- Touch the mkdocs site config

**Provenance:** D7 of `docs/audit-log/325-oauth-grants-landing-map-decisions.md` documents this drift; the landing map calls it out via an "Endpoints not in the OpenAPI spec" callout. Slice 339 closes that loop.

## Threat model

OpenAPI spec drift is a docs/maintainability issue, not a runtime
security surface. STRIDE pass:

- **S/T/R/D/E:** all CLEAN. No new surface introduced; the endpoints
  already exist in code and are tested by slices 187-198.
- **I (Information disclosure):** the OpenAPI spec is a public artifact
  (published to the docs site). Risk: documenting internal-only
  endpoint shapes that should not be in the public spec. Mitigation:
  audit each schema for fields that could be sensitive (e.g. raw
  refresh tokens in response bodies — should be flagged as
  `format: opaque` not full structured). No real-tenant data flows
  through the spec.

## Acceptance criteria

- [ ] **AC-1.** `docs/openapi.yaml` adds path entries for all 12 OAuth
      endpoints listed above.
- [ ] **AC-2.** Each path has `summary`, `description`, request schema
      (where applicable), response schemas (per RFC), and error
      responses (400/401/403/429 as relevant).
- [ ] **AC-3.** Operation IDs follow the existing OpenAPI spec's
      naming convention (camelCase per `docs/openapi.yaml`; verify by
      inspecting other operations in the file).
- [ ] **AC-4.** Schemas reference shared components where possible
      (e.g. `#/components/schemas/Error` for error responses).
- [ ] **AC-5.** `swagger-cli validate docs/openapi.yaml` passes (or
      whatever the existing CI lint is — verify the spec doesn't break
      any existing tooling).
- [ ] **AC-6.** Operations are tagged with `OAuth AS` or similar so
      generated docs group them together.
- [ ] **AC-7.** Token responses do NOT include refresh tokens with
      full structured fields; use `format: opaque` or string-only
      types for sensitive token material.
- [ ] **AC-8.** `pre-commit run --files docs/openapi.yaml` passes.
- [ ] **AC-9.** The "Endpoints not in the OpenAPI spec" callout in
      `docs-site/docs/oauth-grants.md` is removed (or refactored to
      a "fully documented" note) — coordinate via a follow-up commit
      in this same PR.

## Constitutional invariants honored

- **OSCAL is the wire format**, NOT the daily data model (canvas §3.4).
  OpenAPI documents the platform's HTTP API; OSCAL is a separate
  export-side concern. This slice does not touch OSCAL.
- **AI-assist boundary:** OAuth endpoints are not AI-assist surfaces.
  No new tone discipline applies.

## Canvas references

- `Plans/canvas/09-tech-stack.md` (Authorization Server row — OAuth
  RFCs supported)
- ADR-0003 (`docs/adr/0003-oauth-authorization-server.md`)

## Dependencies

- **#325** (OAuth grants landing map) — `in-review` at PR #746;
  expected to merge in batch 128. The cross-link from landing map
  → openapi.yaml depends on 325 landing first.

## Anti-criteria (P0 — block merge)

- **P0-339-1.** Does NOT change any code in `internal/api/oauth/` or
  `internal/auth/`. Spec-only.
- **P0-339-2.** Does NOT add aspirational endpoints (e.g. `password`
  or `refresh_token` grants if they're not in `main`). Spec must
  match shipped behavior.
- **P0-339-3.** Does NOT expose secrets in example values. Use
  redacted placeholders (`<redacted>`, `example: "..."`, etc.).
- **P0-339-4.** Does NOT touch the landing map's substantive content;
  only AC-9's callout removal. Slice 325 owns the landing map.
- **P0-339-5.** Does NOT modify ADR-0003. The ADR is the architectural
  decision; OpenAPI is the wire-format surface. Different documents.

## Skill mix

- **OpenAPI 3.x authoring:** path objects, components/schemas,
  parameters, security schemes
- **OAuth/OIDC RFC familiarity:** RFC 6749 (token + authorize), RFC
  7636 (PKCE), RFC 7662 (introspection), RFC 7009 (revocation), RFC
  8628 (device authorization), OIDC Core 1.0 (discovery + userinfo)
- **swagger-cli or similar OpenAPI validator** (verify the existing
  CI hook + use the same)
- **Slice 325's decisions log** (`docs/audit-log/325-oauth-grants-landing-map-decisions.md`)
  D7 — the immediate provenance

## Notes for the implementing agent

**Source-of-truth for endpoint shapes:** read `internal/api/oauth/*.go`
directly. Do NOT trust the slice 325 landing map for schema details
— it's a reference index, not a specification. Each handler in
`internal/api/oauth/` defines its request body shape, response body
shape, and error responses; transcribe those into OpenAPI.

**Operation ID convention:** inspect existing operations in
`docs/openapi.yaml` to see the pattern. Likely camelCase like
`getControl`, `postEvidence`. OAuth equivalents would be:

- `getOIDCDiscovery`
- `getJWKS`
- `postOAuthToken`
- `getOAuthAuthorize`
- `postOAuthAuthorize`
- `postDeviceAuthorization`
- `postDeviceAuthorizationVerify`
- etc.

Adjust to match the file's actual convention.

**Tagging:** look for existing tags in the spec; add `OAuth AS` (or
match the existing tag naming if one already exists like `Auth`).

**Security schemes:** the OAuth endpoints themselves don't require
auth (they ARE the auth). Document this clearly — most paths should
have `security: []` (empty array, explicitly no auth requirement)
EXCEPT `/oauth/introspect` and `/oauth/revoke` which require client
auth (RFC 6749 §2.3.1).

**Spillover discipline:** if you find additional spec drift while
working (e.g. other non-OAuth endpoints missing from the spec, or
schemas that are out of date), file separate slices. Do NOT bundle.

**No-auto-merge consideration:** unlike slice 323 (README refresh),
this slice CAN auto-merge — the OpenAPI spec is mechanical
transcription of code; the AC-5 validator + maintainer-pulled review
on the diff is sufficient. No screenshot verification needed.
