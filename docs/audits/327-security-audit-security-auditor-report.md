# 327 — Security audit report (voltagent-qa-sec:security-auditor)

**Date:** 2026-05-28
**Auditor agent:** voltagent-qa-sec:security-auditor (loaded as Engineer context)
**Audit type:** Read-only-with-findings; JUDGMENT slice (no code changes in this slice)
**Audit scope:** v1-complete `main` at HEAD `83e6ffbe` (post-slice-363)
**Demo seed only:** slice 205 dataset; no production tenant data examined

---

## Executive summary

The v1 auth substrate (slices 187-198), tenant isolation (slice 002 + descendants), evidence integrity (slice 013 + descendants), and AI-assist boundary (slice 182 + mcp_write_proposals) are **exceptionally well-built** and broadly conform to canvas invariants #1-#10. The substrate composes correctly: signature verification precedes claim validation precedes revocation check precedes tenant GUC application, with no fall-through gaps. RLS is `FORCE`-enabled on every tenant-scoped table audited, isolation is enforced via the DB-layer GUC (not application code), and the evidence ledger is append-only at the policy layer (no UPDATE / DELETE policies on `evidence_records`).

**No Critical findings** were identified.

| Severity      | Count | Notes                                                                                                         |
| ------------- | ----- | ------------------------------------------------------------------------------------------------------------- |
| Critical      | 0     | RCE / auth bypass / RLS bypass / secrets-in-repo — none                                                       |
| High          | 1     | OIDC nonce missing from ID-token validation                                                                   |
| Medium        | 3     | Key rotation not implemented; verbose error reflection; OSCAL signing uses ad-hoc ed25519 instead of cosign   |
| Low           | 3     | CSP `unsafe-inline`; auth-code audit IP gap; oauth_clients global scope                                       |
| Informational | 3     | Board-narrative AI-assist schema (v2+ deferred); device-flow client_secret optional; bearer hash key rotation |

**Spillover slices filed:** 4 (High + 3 Medium) — slots 365, 366, 367, 368.

- 365 — High H-1 OIDC nonce
- 366 — Medium M-1 JWT signing key rotation
- 367 — Medium M-2 error-detail leakage
- 368 — Medium M-3 OSCAL cosign migration

Low findings and informational items remain in this report for maintainer triage; no follow-up slices are filed for them.

---

## Audit surface coverage

Per slice doc AC-7. Each surface was visited; the per-surface findings are tabulated below.

| Surface                                                        | Files examined                                                                                                                                                                                                                                                               | Findings                                                                         |
| -------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------- |
| **OIDC RP** (slices 187-198)                                   | `internal/auth/oidc/oidc.go`                                                                                                                                                                                                                                                 | **H-1** (nonce missing)                                                          |
| **OAuth Authorization Server** (slices 187-192)                | `internal/api/oauth/{authorize,token,revoke,introspect,device_authorization,device_code_grant,device_approve,pkce}.go`; `internal/auth/{jwt,tokensign,keystore/fsstore,oauthclient,oauthcode,revocation,sessions,bearer,password}/*.go`; `internal/auth/jwtmw/middleware.go` | **M-1** (key rotation), I-2 (device-flow secret), I-3 (bearer hash key rotation) |
| **RLS tenant isolation** (slices 002, 013, 014, 017, 033, 065) | `migrations/bootstrap/01-roles.sql`, `migrations/sql/20260511000000_init.sql`, `internal/tenancy/{apply,context}.go`, sampled migrations with `FORCE ROW LEVEL SECURITY`                                                                                                     | (No findings — RLS is correctly applied)                                         |
| **Evidence integrity** (slices 013, 028)                       | `migrations/sql/20260511000004_evidence_ledger.sql`, `internal/evidence/ingest/ingest.go`, `internal/audit/period/period.go`, `internal/oscal/sign.go`                                                                                                                       | **M-3** (cosign migration)                                                       |
| **AI-assist boundary** (slice 182 + mcp_write_proposals)       | `migrations/sql/20260520030000_mcp_write_proposals.sql`, `internal/board/*.go`                                                                                                                                                                                               | I-1 (board-narrative schema deferred to v2+)                                     |
| **OWASP top-10 spot-check**                                    | Cross-cutting grep across `internal/`, `cmd/`, `web/`                                                                                                                                                                                                                        | **M-2** (error reflection); L-1, L-2, L-3                                        |
| **Cross-cutting** (secrets, headers, rate limits, audit log)   | `internal/api/securityheaders/middleware.go`, `internal/api/httpserver.go`, `internal/auth/bearer/bearer.go`, repo grep for hardcoded secrets                                                                                                                                | (Strong baseline; see Low + Informational below)                                 |

---

## Findings

### H-1 (HIGH) — OIDC ID-token nonce missing

- **OWASP:** A07 Identification & Authentication Failures
- **CWE:** CWE-294 — Authentication Bypass by Capture-Replay
- **File:** `internal/auth/oidc/oidc.go`
- **Lines:** 128 (auth URL construction), 199-202 (ID token verifier)
- **Canvas invariant:** #6 (tenant isolation enforced at DB layer — adjacent: auth substrate must be trustworthy)
- **Cross-reference:** ADR-0003 (OAuth Authorization Server), canvas §9 (tech stack — OIDC RP)

**Description.** `Authenticator.BeginLogin` constructs the upstream IdP authorization URL via `oauth2.Config.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier))` and persists `state` + PKCE `verifier` to short-lived cookies. The code path correctly defends against CSRF via the `state` parameter and against code-interception via PKCE. However, the OIDC `nonce` parameter is never generated, never sent to the IdP, never persisted to a cookie, and never validated when the ID token returns. The callback path resolves the ID token via `provider.Verifier(&coreos.Config{ClientID: cfg.ClientID})` then calls `verifier.Verify(ctx, rawIDToken)` — no `Nonce` field is supplied to the Config, so go-oidc does not check it.

**Impact.** Without nonce binding, a maliciously crafted (or captured-and-replayed) ID token could in principle be injected into a victim's session if an attacker compromises an upstream IdP cache or finds a way to coerce the browser into redeeming a stolen ID token. OIDC Core §3.1.2.1 mandates nonce for ID Token replay protection on the authorization code flow; OpenID-AppAuth-style hardening (RFC 9700 OAuth 2.0 Security Best Current Practice §4.5.3) repeats the requirement. State + PKCE protect different attack classes — they do not substitute for nonce.

**Reproduction sketch.** Inspect a captured `BeginLogin` redirect; the URL will contain `state=...&code_challenge=...&code_challenge_method=S256` but no `nonce=...`. Inspect the callback verifier construction; no `Nonce` field is set.

**Recommended mitigation.**

1. Generate a 16-byte random `nonce` per flow alongside the existing `state`.
2. Persist it in a fourth flow cookie (`atlas_oidc_nonce`).
3. Pass via `oauth2.SetAuthURLParam("nonce", nonce)` to `AuthCodeURL`.
4. On callback, set `Nonce` on the go-oidc `Config{}` so the verifier rejects ID tokens whose `nonce` claim doesn't match the cookie value.
5. Add an integration test (RED first) that asserts a forged ID token with a wrong nonce is rejected.

**Spillover slice:** 365 — `auth/365-oidc-nonce-validation`

---

### M-1 (MEDIUM) — JWT signing key rotation not implemented

- **OWASP:** A02 Cryptographic Failures
- **CWE:** CWE-321 — Use of Hard-coded Cryptographic Key (adjacent: long-lived static keys)
- **File:** `internal/auth/keystore/fsstore/fsstore.go`
- **Lines:** 108-111 (Rotate stub)
- **Canvas invariant:** #6 (defense-in-depth at the DB layer extends to defense-in-depth on signing keys)
- **Cross-reference:** ADR-0003 §Key rotation strategy

**Description.** The filesystem keystore's `Rotate` method returns `keystore.ErrRotateUnsupported`. The interface declares Rotate so callers can adopt the rotation API today, but no implementation exists. ADR-0003 explicitly defers the end-to-end rotation flow to a follow-on slice.

**Impact.** A production deployment cannot rotate its JWT signing key without manual file-system intervention (delete `.key` file, restart binary so `Open` regenerates). NIST SP 800-57 Part 1 §5.3.6 recommends max cryptoperiod of 1-3 years for signing keys; operators running self-hosted instances beyond that window operate with a single long-lived static key. The compromise blast radius is the entire signing-key lifetime.

**Note.** This is a documented deferred decision in ADR-0003. The filesystem store does have multi-key load (verification key list, sorted-by-time KeyIDs) — the rotation machinery is half-built, missing only the orchestration (generate-new + retain-old-for-overlap-window + eventual prune).

**Recommended mitigation.**

1. Implement `fsstore.Rotate(ctx)`: generate fresh ES256 keypair → write to `<dir>/<new-kid>.key` → atomically reload via existing `load()` path so the new key becomes signing and the old key remains in `verify[]`.
2. Add an overlap window (configurable, default 24h after rotation) before pruning the old verification key.
3. Surface a manual-trigger CLI (`atlas keys rotate`) AND a scheduled rotation job (default annual, configurable).
4. Document operational runbook for emergency rotation (suspected key compromise).

**Spillover slice:** 366 — `auth/366-jwt-key-rotation`

---

### M-2 (MEDIUM) — Verbose error detail in 500 JSON responses

- **OWASP:** A09 Security Logging & Monitoring Failures (related: information leakage via error messages)
- **CWE:** CWE-209 — Generation of Error Message Containing Sensitive Information
- **Files (sample):** `internal/api/schemaregistry/http.go:56,85`; `internal/api/vendors/handlers.go:401`; `internal/api/artifacts/handlers.go:281`; `internal/api/controlstate/handlers.go:181`; multiple `export.go` handlers (`internal/api/evidence/export.go:185`, `internal/api/adminauditlog/export.go:163`, etc.)
- **Canvas invariant:** None directly; defense-in-depth concern

**Description.** A grep across `internal/api/` finds 36 sites where `err.Error()` is reflected verbatim into the JSON response body. At `StatusBadRequest`/`StatusConflict` the practice is acceptable (the error is about user input) — at `StatusInternalServerError` the raw error can leak DB schema details (column names, table names from pgx errors), file paths (filesystem errors), or driver-level state. The most concerning sites are:

```
internal/api/schemaregistry/http.go:56:  writeJSON(w, http.StatusInternalServerError,
    map[string]string{"error": "list: " + err.Error()})
internal/api/vendors/handlers.go:401:    writeJSON(w, http.StatusInternalServerError,
    map[string]string{"error": op + ": " + err.Error()})
internal/api/artifacts/handlers.go:281:  writeJSON(w, http.StatusInternalServerError,
    map[string]string{"error": op + ": " + err.Error()})
internal/api/controlstate/handlers.go:181: writeJSON(w, http.StatusInternalServerError,
    map[string]string{"error": err.Error()})
```

**Impact.** An attacker probing the API can map internal schema and library versions from error messages — a recon enabler that shortens reconnaissance time but does not itself escalate.

**Recommended mitigation.**

1. Audit every `writeJSON(... http.StatusInternalServerError ... err.Error())` site.
2. Replace with: write generic message client-side (`"internal error; see request id <uuid>"`), log full error server-side keyed by request ID via `slog`.
3. Add a lint rule (custom golangci-lint check or `analysistest`) preventing the pattern in new code.

**Spillover slice:** 367 — `infra/367-error-detail-leakage-audit`

---

### M-3 (MEDIUM) — OSCAL export bundle signing uses in-process ed25519, not cosign

- **OWASP:** A08 Software & Data Integrity Failures (related: signature provenance)
- **CWE:** CWE-1395 — Dependency on Vulnerable Third-Party Component (adjacent; in this case the gap is _absence_ of the canvas-mandated cosign tool)
- **File:** `internal/oscal/sign.go`
- **Lines:** 25-28 (decision comment), 56-65 (NewSigner), 106-121 (SignBundle)
- **Canvas invariant:** #2 (separation of ingestion and evaluation — adjacent: integrity of exports)
- **Cross-reference:** Canvas §9 ("cosign signing of audit-export bundles"); slice 030 decisions log §D1

**Description.** The OSCAL export bundle is signed with **in-process ed25519** detached signatures, not cosign. The slice-030 decisions log explicitly documents this as a deliberate trade — shelling out to the cosign binary would add a fragile external dependency to every export — and flags "swap for cosign keyless + Fulcio transparency log" as a v3 revisit item. The cryptographic shape (ed25519 detached over content digest) matches what cosign-sign-blob produces, so the signature is verifiable; what's missing is the **Sigstore ecosystem integration** — transparency log entries, Fulcio-issued OIDC identities, ecosystem tooling compatibility.

**Impact.** Auditors and downstream verifiers cannot use stock cosign tooling to verify atlas exports without the per-bundle ad-hoc ed25519 public key the manifest embeds. The signature is valid, but the chain of identity (who signed? when? against what OIDC identity?) is opaque. For a GRC tool whose pitch is "survive a third-party security review," shipping with a non-standard signature ecosystem is a notable friction point.

**Recommended mitigation.**

1. Implement `internal/oscal/cosign.go` that wraps `cosign sign-blob` against the bundle digest.
2. Default to keyless mode with Fulcio (`COSIGN_EXPERIMENTAL=1`) when a tenant has OIDC identity available; KMS-backed mode (AWS KMS, GCP KMS, Azure Key Vault) when configured.
3. Preserve the in-process ed25519 path as a `--signing-mode=embedded-ed25519` fallback for fully-air-gapped deployments where cosign cannot reach Fulcio.
4. Update OSCAL export manifest to include a `signature_mode` discriminator field.
5. Provide an `atlas oscal verify` CLI that auto-detects the signing mode.

**Spillover slice:** 368 — `oscal/368-cosign-signing-migration`

---

### L-1 (LOW) — CSP includes `'unsafe-inline'` in style-src

- **OWASP:** A05 Security Misconfiguration
- **CWE:** CWE-1021 — Improper Restriction of Rendered UI Layers or Frames (adjacent)
- **File:** `internal/api/securityheaders/middleware.go:57` and `web/proxy.ts:87`
- **Canvas invariant:** None

**Description.** Both the Go backend and the Next.js proxy ship CSP `style-src 'self' 'unsafe-inline'`. Tailwind + shadcn inject inline `<style>` blocks at runtime; nonce/hash migration would require non-trivial frontend work. The CSP also currently ships in report-only mode (intentional — Next.js hydration scripts violate `script-src 'self'` without a nonce).

**Disposition:** Documented compromise. No follow-up slice; revisit when frontend tightening becomes practical.

---

### L-2 (LOW) — Authorization-code redemption audit does not capture IP

- **OWASP:** A09 Security Logging & Monitoring Failures
- **CWE:** CWE-778 — Insufficient Logging
- **File:** `internal/api/oauth/pkce.go:134` (writeAuthCodeAudit — `ip_address` written as NULL)
- **Canvas invariant:** None directly

**Description.** The slice-189 author noted "ip_address NULL in best-effort audit — slice 190 audit will tighten" in the code comment. Slice 190 shipped without addressing this; the auth-code redemption audit row's IP column remains NULL.

**Disposition:** Documented gap. Forensic value of capturing IP at the redemption (vs. authorize) step is debatable — the authorize step already captures it via the session row. No follow-up slice.

---

### L-3 (LOW) — `oauth_clients` table is platform-global (not tenant-scoped)

- **OWASP:** A01 Broken Access Control (defense-in-depth concern only)
- **CWE:** CWE-284 — Improper Access Control (defense-in-depth)
- **File:** `migrations/sql/20260521000010_oauth_clients.sql` + `internal/auth/oauthclient/oauthclient.go`
- **Canvas invariant:** #6 (RLS at DB layer — `oauth_clients` is intentionally out-of-scope)

**Description.** The `oauth_clients` table holds platform-global machine identities (no tenant_id, no RLS). Documented in slice 188's package header. A misconfigured operator on a SaaS multi-tenant deployment could in principle give Tenant A's machine identity to Tenant B's caller; the JWT itself stamps `current_tenant_id` from the redeeming user / token-exchange, so the actual blast radius is bounded — but the policy gap exists by design.

**Disposition:** Architectural decision. No follow-up slice; documented in slice 188 already.

---

### I-1 (INFORMATIONAL) — Board-narrative AI-assist schema constraint not yet present

- **Type:** Schema gap matching documented v2+ deferral
- **Files:** `migrations/sql/20260511000031_board_briefs.sql`, `migrations/sql/20260511000032_board_packs.sql`
- **Canvas reference:** CLAUDE.md "AI-assist boundary (hard)" + "Board-narrative AI-assist"

**Description.** The CLAUDE.md schema-level enforcement (`ai_assisted=true ↔ human_approved=true → human_approver IS NOT NULL` + `prompt_version`/`model_name`/`model_version`/`model_provider` columns) is currently present **only** on `mcp_write_proposals` (slice 182's foundation). The `board_briefs` and `board_packs` tables do not yet have the constraint or the audit columns. CLAUDE.md explicitly states this is v2+ work tied to board-narrative v0 shipping. The slice-182 foundation is in place; what's missing is the per-table extension, which is correctly scoped to land alongside the v2 feature.

**Disposition:** Matches documented timeline; no action.

---

### I-2 (INFORMATIONAL) — Device authorization endpoint does not require client_secret

- **File:** `internal/api/oauth/device_authorization.go:188-200`
- **RFC reference:** RFC 8628 §5.1

**Description.** The device-authorization endpoint validates `client_id` exists but does NOT require `client_secret`. This is RFC 8628 §5.1-acceptable for public CLI clients (where holding a static secret offers no real security since the binary ships to end-user machines). A future hardening option for confidential clients would be to extend `oauth_clients` with a `client_type ('public' | 'confidential')` column and require secret on `client_type='confidential'`. Documented as a known design choice in code comments.

**Disposition:** Documented design choice; no action.

---

### I-3 (INFORMATIONAL) — Bearer hash key (`BEARER_HASH_KEY`) rotation not implemented

- **File:** `internal/auth/bearer/bearer.go:43-49`
- **ADR reference:** ADR-0002 (bearer token storage)

**Description.** `BEARER_HASH_KEY` is required to be ≥32 bytes; cmd/atlas refuses to boot otherwise. There is no rotation mechanism — a compromised key would require operators to re-hash every stored bearer hash (multi-step procedure with operational risk). Bearer tokens are post-slice-197 only on legacy paths (the JWT migration has retired them on the hot path), so the surface is shrinking; nonetheless an operational runbook for key rotation would be valuable.

**Disposition:** Acceptable v1; revisit in v2 alongside the wider keystore rotation work (M-1).

---

## Verified positive observations

The audit also surfaced patterns that explicitly **strengthen** the security posture and are worth recording:

1. **Algorithm allowlist locked at parser layer** (`tokensign.AllowedAlgs = []jose.SignatureAlgorithm{jose.ES256}`) — prevents algorithm confusion attacks per RFC 8725 §2.1.
2. **Fail-closed semantics on revocation DB error** (`jwtmw.Middleware` line 156-162) — a DB outage on the revocation store fails to 401, not to "pass through".
3. **PKCE S256 mandatory at both application and DB CHECK layer** (slice 189 P0-189-1).
4. **One-shot auth code redemption via atomic UPDATE-RETURNING** (`oauthcode.ConsumeOnce`).
5. **Constant-time comparisons throughout** (`password.Verify`, `oauthclient.Verify`, `constantTimeEqualString` for PKCE).
6. **No fall-through after JWT-shape verification failure** (`jwtmw.extractJWT` — P0-190-1 prevents the legacy-bearer fall-through that would be an auth bypass).
7. **WWW-Authenticate header per RFC 6750** on all 401 responses.
8. **Tenant GUC set from VERIFIED claim only** (P0-190-3) — request headers are NEVER read for tenant override.
9. **FORCE ROW LEVEL SECURITY on every tenant-scoped table audited** — the table owner (atlas_migrate) is bound too; only BYPASSRLS roles bypass.
10. **`current_tenant_matches` returns false on missing GUC** — `current_setting('app.current_tenant', true)` returns NULL when unset; the comparison `row_tenant::text = NULL` is NULL (false in WHERE) — RLS denies on missing context as the canvas invariant requires.
11. **Evidence ledger is append-only at the policy layer** — slice 013 dropped tenant_isolation and replaced with tenant_read + tenant_insert; no UPDATE/DELETE policy under FORCE means atlas_app cannot mutate the ledger.
12. **Audit-period freeze enforces `observed_at <= populations.frozen_at`** at query path level — point-in-time replay invariant honored.
13. **CORS exact-match only** (`http://localhost:3000` dev origin) — no wildcard, no Origin reflection.
14. **Argon2id RFC 9106 parameters** (m=64MiB, t=1, p=4) for password storage with constant-time verify.
15. **0600 file mode on keystore PEM files** with belt-and-suspenders chmod after atomic rename.
16. **No `InsecureSkipVerify` anywhere in production code; no `os/exec` shell-out paths; no `fmt.Sprintf`-into-SQL with user input.**

---

## Methodology and integrity

- Audit was conducted by reading source files end-to-end against the slice-doc surface enumeration.
- Cross-referenced against canvas invariants #1-#10 + ADRs 0001-0006 + CLAUDE.md constitutional principles.
- Severity rubric from slice 327 doc (Critical = RCE/auth-bypass/RLS-bypass/secrets-in-repo; High = priv-esc/cross-tenant data/broken crypto; Medium = within-tenant disclosure/DoS/missing security header; Low = hardening; Informational = observation).
- Demo seed only (slice 205); no production tenant data examined.
- Read-only audit; no code modifications in this slice's diff.

### Surface coverage attestation

Per slice doc AC-7:

| Surface from slice doc narrative                                                       | Examined?        | Notes                             |
| -------------------------------------------------------------------------------------- | ---------------- | --------------------------------- |
| AuthN (OIDC RP slices 187-198)                                                         | YES              | H-1 surfaced                      |
| AuthZ (OAuth AS, JWT, RFCs 8693/7009/7662/7636)                                        | YES              | M-1 surfaced                      |
| Tenant isolation (RLS at DB layer)                                                     | YES              | No findings — strong baseline     |
| Evidence integrity (sha256 + cosign)                                                   | YES              | M-3 surfaced                      |
| Secrets handling (JWT keystore, cosign keys, IdP secrets, connector creds, DB strings) | YES              | No hardcoded secrets in repo      |
| AI-assist boundary schema enforcement                                                  | YES              | I-1 (v2+ deferred — matches plan) |
| OWASP top-10 across Go + TS + Python                                                   | YES (spot-check) | M-2 surfaced                      |

### Audit agent identity per AC-8

The audit was conducted via the primary Engineer agent persona (Marcus Webb), loading the `voltagent-qa-sec:security-auditor` persona file as Engineer context. The persona's `tools: Read, Grep, Glob` boundary was respected — no DB credentials, no production identity, no super-admin token was used during the audit. The audit reads a developer-level local checkout.

---

## Open questions for maintainer triage

1. Should H-1 (OIDC nonce) ship as a hot-fix slice or roll into the next batch? The fix is small (≈30 LoC + integration test) but conceptually load-bearing for the v1 binary success criterion ("survive third-party security review").
2. M-3 (cosign migration) is genuinely a multi-week effort if done properly (Fulcio integration, KMS modes, fallback path, CLI). Should this be scoped to v2 alongside the wider Sigstore work, OR shipped piecemeal (KMS-backed cosign now, Fulcio later)?
3. Should I-1 (board-narrative schema) be promoted to a pre-commitment slice now (mirroring slice 182's foundation pattern) so the constraint is in place before any board v0 work, OR remain deferred?

---

## Companion document

- **Decisions log:** `docs/audit-log/327-security-audit-security-auditor-decisions.md` — JUDGMENT calls made by this audit (severity rubric, scope choices, spillover allocation).
- **Spillover slices:**
  - `docs/issues/365-oidc-nonce-validation.md` (auth — H-1)
  - `docs/issues/366-jwt-key-rotation.md` (auth — M-1)
  - `docs/issues/367-error-detail-leakage-audit.md` (infra — M-2)
  - `docs/issues/368-cosign-signing-migration.md` (oscal — M-3)
