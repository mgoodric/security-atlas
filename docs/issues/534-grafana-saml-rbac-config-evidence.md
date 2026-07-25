# 534 — Grafana connector: SAML / RBAC config evidence

**Cluster:** Connectors
**Estimate:** M (1-2d)
**Type:** JUDGMENT (evidence-kind shape + scope-minimum + stable-field choices)
**Status:** `blocked` (depends on #488 — base Grafana connector — merged first)

## Narrative

Slice 488 shipped the base Grafana connector with one evidence surface — alert-
rule + notification-policy configuration inventory (`monitoring.alert_config.v1`)
— deliberately scoped to the minimum that proves the monitoring evidence family
is a first-class peer. It explicitly deferred Grafana **authn/authz
configuration** (SAML/OAuth SSO settings + org-role / team RBAC) as a follow-on
(P0-488-7).

This slice adds that surface: read Grafana's SSO + RBAC configuration (whether
SAML/OAuth SSO is enabled, the org-role mapping, team membership counts, and the
RBAC role assignments) via the read-only Grafana API (`GET /api/v1/sso-settings`,
`GET /api/access-control/...`, Admin-or-scoped-read role). The recurring SOC 2
CC6.1/CC6.2/CC6.3 evidence demand is "prove SSO is enforced and access is
role-based"; this is an identity/access surface distinct from the monitoring
surface in 488, so it gets its own evidence kind.

This is the slice-488 pattern: a new `internal/ssoconfig` collector + a new
evidence kind (JUDGMENT: a sibling `grafana.access_config.v1`, NOT the monitoring
kind — this is an IAM surface, not a monitoring surface) + its schema with
`x-default-scf-anchors` (candidate: `IAC-06` Authenticator Management + `IAC-22`
Least Privilege), registered in `DefaultSeed`, faked Grafana API surface in tests.
No platform-side wire change (invariant #3 — push only); `profiles_supported`
stays `[pull]`; the interval stays honestly named.

**Scope discipline + auth caveat.** Reading SSO settings may require more than the
Viewer role (slice 488's minimum). This slice owns documenting the _additional_
minimal read scope precisely and warning against granting Admin "to be safe."
Configuration only — never SAML private keys, OAuth client secrets, or LDAP bind
passwords.

## Threat model (STRIDE)

Source-credential-heavy, with a sharper credential-exposure risk than 488 because
SSO settings _contain_ secrets (SAML private key, OAuth client secret).

- **S — Spoofing.** Platform push via existing credential; Grafana via the
  service-account token. **Mitigation:** the smallest read scope that can read
  SSO settings + access-control, documented as the minimum.
- **T — Tampering.** sha256 content-hash per record; ingest validates.
- **R — Repudiation.** Register-per-run + stable `actor_id`
  (`connector:grafana:ssoconfig@<version>`) + documented `observed_at`.
- **I — Information disclosure (DOMINANT).** SSO settings embed the SAML private
  key / OAuth client secret / LDAP bind password. **Mitigation:** collect only
  the _presence/enabled_ booleans + role mappings + provider type — NEVER the
  private key, client secret, bind password, or any credential field. A test
  asserts no secret field name (`private_key`, `client_secret`, `bind_password`,
  `certificate`) and no secret value enters a record. Source token never logged.
- **D — Denial of service.** Bounded reads + run timeout.
- **E — Elevation of privilege.** Read-only least-privilege; name the exact
  additional scope beyond Viewer and warn against Admin.

## Acceptance criteria (sketch — refine at pickup)

- [ ] `connectors/grafana/internal/ssoconfig` collector lands following the
      slice-488 pattern.
- [ ] Collects SSO-enabled state + provider type + org-role mapping + RBAC
      assignments via the read-only Grafana API.
- [ ] Authenticates via the documented minimal read scope (record the exact
      role/scope beyond Viewer).
- [ ] Pushes to the single `IngestEvidence` (`Push`) API — no wire change.
- [ ] sha256 content-hash + stable optional fields; `profiles_supported=[pull]`,
      honest interval.
- [ ] Evidence kind + schema with `x-default-scf-anchors` (record the JUDGMENT).
- [ ] Tests cover collect → push against a mocked source API (no live Grafana).
- [ ] A test asserts NO SSO secret (private key / client secret / bind password /
      certificate) enters a record; a test asserts the source token is never
      logged.
- [ ] README + decisions log + changelog.

## Anti-criteria (P0)

- **P0.** No platform-side wire change (push only — invariant #3).
- **P0.** No write/admin source role beyond the documented minimal read scope.
- **P0.** No SAML private keys / OAuth client secrets / LDAP bind passwords / any
  credential field in a record.
- **P0.** No source token logged or transmitted into the platform.
- **P0.** Read-only OSS API; honest pull interval.

## Dependencies

- **#488** (base Grafana connector) — provides the connector scaffold + auth.
