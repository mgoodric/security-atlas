# 2026-Q2 Security Audit — security-atlas

**Auditor:** Claude (orchestrator) **Date:** 2026-05-15 **Scope:** `main` at commit `ac52834` (72/81 slices merged)

## Methodology

Source-code review using structured `grep` passes across the highest-yield attack surfaces, cross-referenced with merged-slice context (slices 034 / 035 / 062 / 073 are the primary auth surfaces; 033 / 068 are the primary RLS surfaces; 036 is the artifact-upload surface). Out of scope: SAST/CodeQL findings (already in pipeline), secrets detection (GitGuardian already in pipeline), dynamic testing, third-party penetration testing.

Audit was a first-pass review intended to surface high-signal findings — not a substitute for a paid pentest engagement.

## Findings summary

| Sev         | Finding                                                                                                                                                                     | Slice      |
| ----------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------- |
| **HIGH**    | Open redirect on `signIn` `from` parameter — `web/app/login/actions.ts:23,55`                                                                                               | **086**    |
| MEDIUM-HIGH | Missing security HTTP headers (HSTS, CSP, X-Frame-Options, X-Content-Type-Options, Referrer-Policy)                                                                         | **087**    |
| MEDIUM      | CLI `http.DefaultClient.Do(req)` calls have no timeout — `cmd/atlas-cli/cmd_features.go:181`, `cmd/atlas-cli/cmd_credentials.go:148`                                        | **088**    |
| MEDIUM      | No dependency-vulnerability scanning beyond Dependabot (no `govulncheck`, no `npm audit`, no Trivy on container images)                                                     | **089**    |
| LOW         | AI-assist boundary not yet schema-enforced (no LLM-touching tables exist on `main` yet, so no constraint exists either)                                                     | _deferred_ |
| LOW         | No login brute-force rate-limiting on `/auth/login` (mitigated by 32-byte bearer entropy; matters only if an OIDC IdP integration enables credential-stuffing-via-flooding) | _deferred_ |

## Detail

### HIGH — Open redirect on `signIn` `from` parameter

**File:** `web/app/login/actions.ts`

The `signIn` server action accepts `from` from the form (originating from `/login?from=...` search param) and passes it directly to Next.js's `redirect(target || "/dashboard")` with no validation.

```ts
const target = String(formData.get("from") ?? "/dashboard");
// ...
redirect(target || "/dashboard");
```

**Attack:** `https://atlas.example.com/login?from=https://evil.example.com/phish`. A user clicking this and signing in successfully is redirected to attacker-controlled origin. The session cookie is `HttpOnly` so it doesn't leak, but the trust signal of "I just signed into my GRC tool, this must also be safe" enables credential-phishing pivots (e.g., the attacker mounts a clone login screen asking for additional credentials).

**Fix:** validate `target` matches `^/[^/]` (relative path starting with `/` but not `//` which would be protocol-relative). Reject anything else, fall back to `/dashboard`. Filed as slice **086**.

**Remediation status:** shipped in slice 086 (commit `<TBD post-merge>`). Helper at `web/lib/safe-redirect.ts`; both `signIn` redirect call sites validated; 9-case unit test enumerating attack/safe variants; Playwright spec under slice-079 quarantine; CONTRIBUTING.md gains "Open-redirect prevention" reviewer guidance. Decisions log: `docs/audit-log/086-fix-open-redirect-signin-from-decisions.md`.

### MEDIUM-HIGH — Missing security HTTP headers

**Files:** `internal/api/httpserver.go`, the broader middleware stack

A grep across `internal/` for `Strict-Transport-Security`, `Content-Security-Policy`, `X-Frame-Options`, `X-Content-Type-Options`, `Referrer-Policy` returns **zero matches**. The platform serves a web UI (slices 005 + 040 + 041 + 042 + 043 + 056 + 060 + 063) and a multipart artifact upload endpoint (slice 036) without any of the standard hardening headers.

Specific risks:

- **No HSTS** — first-visit MITM downgrade possible; user typing `atlas.example.com` (without `https://`) gets a plaintext request that an active network attacker can pivot to a non-HTTPS clone.
- **No CSP** — XSS payloads (if any slip past React's default escaping) execute with the full origin's privileges. Slice 042 had a CodeQL `js/xss-through-dom` finding that was dismissed as "React-escaped, false positive"; CSP would be defense-in-depth against regressions.
- **No X-Frame-Options / `frame-ancestors`** — clickjacking on authenticated sessions: attacker iframes the dashboard, overlays transparent UI, captures clicks.
- **No X-Content-Type-Options: nosniff** — MIME-confusion on uploaded artifacts (slice 036 S3 artifact store): a malicious upload claiming `text/plain` but containing `<script>` could be sniffed by browsers as HTML.
- **No Referrer-Policy** — sensitive URLs (AuditPeriod IDs, evidence record IDs) leak in the `Referer` header when the user clicks an outbound link.

**Fix:** add a `security-headers` middleware run as the first `root.Use(...)` in `httpserver.go`, before the bearer-auth middleware. Sets the canonical hardening set. Filed as slice **087**.

### MEDIUM — CLI `http.DefaultClient.Do(req)` without timeout

**Files:** `cmd/atlas-cli/cmd_features.go:181`, `cmd/atlas-cli/cmd_credentials.go:148`

`http.DefaultClient` has no timeout. If the atlas server is unresponsive (DNS hang, deep TCP retransmits, server pause-the-world), the CLI hangs indefinitely. The atlas-cli is the user's primary administrative entrypoint; a hung CLI is a DoS-against-the-operator, not against the platform.

**Fix:** replace both occurrences with `client := &http.Client{Timeout: 30 * time.Second}` then `client.Do(req)`. Filed as slice **088**.

### MEDIUM — No dependency vulnerability scanning beyond Dependabot

**Files:** `.github/workflows/ci.yml`, `.github/workflows/codeql.yml`

Dependabot opens PRs for available version bumps (and the new `deps:` prefix from slice 077 surfaces them cleanly in release notes). But Dependabot **does not detect known-vulnerable-current versions** — only available upgrades. A package can sit at a vulnerable patch with no newer release available, and Dependabot stays silent.

Missing scanners:

- **`govulncheck`** — Go's official vulnerability scanner. Reads `go.mod` + the actual call graph; flags only vulnerabilities our code actually reaches. Trivial to add as a CI step.
- **`npm audit`** — JavaScript vulnerability scanner from npm itself. The `web/` workspace has React + Next.js + dozens of transitive deps; `npm audit` flags known CVEs.
- **Trivy (container scan)** — slice 037 ships a docker-compose self-host bundle + slice 038 ships a Helm chart with a distroless atlas image. The image is built but never scanned for OS-package CVEs.

**Fix:** add three CI jobs (Go vulncheck, npm audit, Trivy on the built image) following the slice-069 stub-job pattern. Initially informational, not in required-checks (would flake the merge queue on every new published CVE). Filed as slice **089**.

### LOW — AI-assist boundary not yet schema-enforced

CLAUDE.md constitutional invariant says:

> Schema-level enforcement: `ai_assisted=true` records cannot have `human_approved=true` without `human_approver` set.

**State today:** no migration on `main` carries `ai_assisted`, `human_approved`, or `human_approver` columns. The slices that came closest (031 + 032 board packs/briefs) are explicitly templated-only with no LLM imports — they include comments documenting "there is no `ai_assisted` flag because no LLM is invoked."

**Conclusion:** there is nothing to enforce yet because no AI-assist surface exists in code. The constitutional rule is documented and waiting. The first feature slice that touches a table with AI-assisted content (canvas open question #14 — board narrative LLM boundary) MUST land the CHECK constraint as part of its migration. This is documented but NOT enforced today by any pre-commit hook or CI gate; if a future engineer adds `ai_assisted` without `human_approver`, no automatic signal will fire.

**No slice filed.** This is a constitutional-compliance reminder rather than a current vulnerability. If the maintainer wants a stronger guarantee, file a slice for a `pre-commit` hook that greps for `ai_assisted` in new migrations and verifies the CHECK constraint is present.

### LOW — No login brute-force rate-limiting

The bearer-paste login flow (`web/app/login/actions.ts` + `internal/api/authmw`) does not rate-limit failed attempts. Bearer tokens issued by `atlas-cli credentials issue` are 32 bytes of cryptographic randomness, so online brute-force is computationally infeasible. The OIDC RP flow (slice 034) delegates credential checking to the IdP, which is the IdP's responsibility.

**No slice filed.** The current threat model accepts this. If the maintainer later integrates an OIDC IdP that lacks its own rate-limiting (e.g., a self-hosted Keycloak with default config), this re-becomes a finding.

## Strong points (verified, no action needed)

- **Argon2id password hashing with `subtle.ConstantTimeCompare`** — `internal/auth/password/password.go`. Best-practice cryptography.
- **API key storage: HMAC-SHA256 + BEARER_HASH_KEY** — `internal/auth/apikeystore/apikeystore.go` + ADR 0002. Plaintext tokens never persist; lookup is by hash. Timing-attack-resistant by design.
- **Cookies: `HttpOnly` + `Secure` (in prod) + `SameSite=Lax`** — `internal/auth/sessions/sessions.go`, `internal/auth/oidc/oidc.go`. All three flags consistently set across cookie-issuing call sites.
- **Tenant GUC set on every request via `tenancymw`** — `internal/tenancy/context.go` + four-policy RLS pattern across every tenant-scoped table. Constitutional invariant 6 enforced at the DB layer.
- **All URL params parsed as UUID or via `parseID` wrapper** — `internal/api/auditperiods/`, `internal/api/vendors/`, `internal/api/board/`, etc. No string interpolation surface.
- **Only one raw SQL via `fmt.Sprintf` exists** — `internal/api/adminauditlog/handler_integration_test.go:138`. Test-only context (DELETE FROM table-name interpolation in a test cleanup). Acceptable.
- **No `exec.Command` in production code** — shell-injection surface absent.
- **Bootstrap-token file: mode 0600, atomically deleted on first sign-in** (slice 073). P0-A1 safety property test-verified.
- **Artifact upload size-capped via `http.MaxBytesReader`** — slice 036 + `internal/api/artifacts/handlers.go:108`.
- **GitHub Actions all version-pinned** — `@vN` discipline maintained. No `@main` / `@master` floating refs.
- **OIDC SSRF-hardened** — slice 062's preflight + `Transport.DialContext` IP re-check + redirect-disabled.
- **Append-only audit trails** — `decision_audit_log`, `evidence_records`, `metric_inputs` (slice 076). Four-policy RLS without update/delete = append-only by construction.
- **Hash inputs ADR 0003** — AuditPeriod freezing inputs are content-only-hashed, not the wall-clock-affected envelope.
- **Constitutional AI-assist boundary HONORED in code** — slices 031 / 032 templated-only with explicit comments documenting no LLM dependency.

## Notes for the maintainer

- This is a first-pass audit by a generalist orchestrator with deep familiarity with the codebase. It should not replace a paid third-party security review before any commercial launch.
- The four MEDIUM findings (086 + 087 + 088 + 089) are all small + uncorrelated — they make a clean N=2 or N=4 follow-on batch for the continuous-batch loop. None overlap on file surfaces.
- The HIGH finding (086) should land **before** any external production deployment of the v1 binary, since the open-redirect is exploitable today on the existing login page.
- Findings here that are NOT slices (LOW × 2) are documented for completeness; they're accepted-risk under current threat model.
- Re-run this audit cadence quarterly OR after any major auth/middleware change. Track findings in `docs/audits/YYYY-QN-security-audit.md`.
