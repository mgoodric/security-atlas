// Package admindemo serves the slice-278 demo-seed UI button.
//
// Two action routes wrap slice 205's `internal/demoseed/` package
// with a triple-gated HTTP surface:
//
//	POST   /v1/admin/demo/seed         -- reseed demo dataset
//	POST   /v1/admin/demo/teardown     -- delete demo tenant
//	GET    /v1/admin/demo/status       -- {enabled: boolean} probe
//
// TRIPLE-GATE (defense in depth — order matters):
//
//  1. Env-var gate: handlers read `ATLAS_ENABLE_DEMO_SEED`. Unset
//     -> 503 {error: "demo seed not enabled on this deployment"}.
//     This is the load-bearing edge-deployment-only gate. The
//     env var is set on the `atlas-edge` docker-compose profile
//     only; production deployments never set it. 503 (NOT 404)
//     is intentional: the path exists, the feature is disabled.
//     Non-admin callers never see 503 because the admin gate
//     short-circuits first (admin OPA -> 403 from authzmw).
//
//  2. Admin role gate: slice-035 OPA at authzmw.Middleware. Resolves
//     the request to resource.type='admin', resource.id='demo'.
//     The admin.rego rule admits any action for a caller carrying
//     RoleAdmin (DB-side user_roles grant). Every other canonical
//     role denies. Super_admin alone does NOT admit (anti-criterion
//     P0-278-7: admin is the only gate). The handler additionally
//     does a defense-in-depth admin check via
//     authctx.CredentialFromContext so a misconfigured authz mount
//     fails closed.
//
//  3. Meta-audit row: every Seed/Teardown invocation writes ONE
//     `me_audit_log` row with action='demo_seed' (or 'demo_teardown')
//     BEFORE invoking the seeder. The seeder package writes its own
//     'demo_seed_apply' / 'demo_seed_teardown' rows separately —
//     two-row forensic separation distinguishes WHO CLICKED THE
//     BUTTON from WHAT THE SEEDER DID.
//
// RATE LIMIT: per-IP token bucket, 1 invocation per 60 seconds for
// Seed + Teardown (shared bucket per IP). Excess returns 429 with
// `Retry-After: 60`. The Status probe is NOT rate-limited (operator
// hits it on every page load). The 60s window matches operator
// pace ("reset between demo runs is 5+ minutes apart") and is
// generous enough that a single mis-click doesn't lock the
// operator out. The IP comes from RemoteAddr by default;
// X-Forwarded-For is honored only when TRUST_FORWARDED_HEADERS=1
// is set (mirrors the slice-162 auth-package posture). Without that
// opt-in, an attacker on the admin network could spoof XFF to evade
// the limiter.
//
// SCOPE DISCIPLINE (P0-278-*):
//
//   - P0-278-1: no auto-enable — env-var must be explicitly set.
//   - P0-278-2: no user-supplied tenant slug or scale — buttons
//     hard-code defaults (slug="demo", scale=1.0).
//   - P0-278-3: no admin-role-bypass.
//   - P0-278-4: no skip of audit-row write.
//   - P0-278-5: no modification of slice 205's demoseed package.
//   - P0-278-6: no bypass of slice 205's safeguards (10-row guard,
//     forensic-mark teardown).
//   - P0-278-7: no new authz role — admin role only.
//   - P0-278-8: no logging of seeded row contents — only counts.
//
// CONSTITUTIONAL INVARIANTS:
//
//   - Invariant #6 (tenant isolation via RLS). The seed handler
//     uses the BYPASSRLS auth pool only because that is the pool
//     the seeder package itself requires (slice 205 D3 pattern);
//     the actor's session tenant_id flows through to the audit-log
//     rows so RLS still scopes downstream reads.
//
//   - Append-only audit invariant. The handler always writes the
//     audit-log row BEFORE invoking the seeder. If the audit-log
//     write fails, the seeder does NOT run (fail closed).
//
//   - AI-assist boundary. No LLM in the loop.
package admindemo
