# 534 — Grafana SAML/RBAC config evidence: JUDGMENT decisions log

Slice type: JUDGMENT (evidence-kind shape + over-collection/secret+PII boundary +
auth-scope minimum + stable-field choices). This file records the subjective
build-time calls for slice 534 — the sibling-kind decision, the
over-collection / secret + PII drop mechanism (HOW a SAML key / user email can't
leak), the auth-scope minimum + the Admin warning, and the SCF anchor choice. It
does NOT block merge; the maintainer iterates post-deployment from the "Revisit
once in use" notes.

Parent: slice 488 (`docs/issues/488-*` + the base Grafana connector under
`connectors/grafana/**`). Slice 488 shipped the connector emitting
`monitoring.alert_config.v1` (alert-rule + contact-point inventory) and
deliberately deferred Grafana authn/authz configuration (SAML/OAuth SSO settings

- org-role / team RBAC) as a follow-on (P0-488-7). This slice adds that surface.

## D1 — Evidence-kind shape: sibling kind `grafana.access_config.v1`

- **Options considered:** (a) widen the existing `monitoring.alert_config.v1`
  record with access fields; (b) a new sibling kind `grafana.access_config.v1`
  carrying one access-config record per run.
- **Chosen:** (b), the sibling kind — as the slice doc's JUDGMENT line specified.
- **Rationale:**
  1. **Different surface, different altitude.** `monitoring.alert_config.v1` is a
     monitoring-configuration kind shared by Datadog + Grafana (a rule with a
     name/type/enabled/targets shape). SSO enforcement + RBAC posture is an
     identity/access surface — it has no rule/target shape and would force the
     monitoring kind into a lowest-common-denominator blob.
  2. **Independent evaluation + control mapping.** The monitoring kind feeds
     CC7.2 (monitoring); the access kind feeds CC6.1/CC6.2/CC6.3 (SSO enforced +
     access role-based) → SCF IAC-06/IAC-22. A sibling kind lets the evaluator
     query the IAM surface as its own thing.
  3. **Clean append-only ledger (invariant #2).** Distinct idempotency-key prefix
     (`grafana.access_config|<environment>|<hour>`) so the two kinds never
     collide. One access-config record per (environment, hour).
  4. **Repo precedent.** Mirrors the slice-488→533 / 490→555/556 / 489→538
     sibling splits: a follow-on surface whose auditable fields don't fit the
     base shape gets its own kind.
- **Shape:** one record per run. Payload keys: `sso_enabled` (bool — the CC6.1
  headline), `team_count`, `total_team_memberships`, `user_role_assignments`,
  `team_role_assignments`, `builtin_role_assignments` (the six required keys),
  plus optional `sso_providers[]` (`provider_type` + `enabled` + optional
  `role_mappings[]`). `additionalProperties:false`. `Result = INCONCLUSIVE` (the
  connector reports a descriptive posture; the platform evaluator owns the
  pass/fail per (control, scope)).

## D2 — The over-collection / secret + PII boundary (THE load-bearing decision)

This is the central discipline of the slice, and it is sharper than 488's
contact-point boundary: Grafana's `GET /api/v1/sso-settings` payload _embeds_
secrets (SAML private key, OAuth client secret, LDAP bind password, signing
certificate), and the team / access-control endpoints _embed_ user identities
(name / email / login). The question this slice owns: _what config can be
collected without pulling a secret or an identity?_

- **Decided IN:** SSO `enabled` flags, the provider TYPE (saml / oauth / oidc /
  ldap / …), the org-role mapping RULE strings (role names / attribute->role
  expressions — `Editor`, `Admin`, `GrafanaAdmin`, …), aggregate team COUNTS
  (team count + total membership count), and the RBAC role-assignment COUNTS
  rolled up by scope (user / team / builtin).
- **Decided OUT (never collected):** the SAML private key, the OAuth client
  secret, the LDAP bind password, any signing certificate, and ANY individual
  user / team-member / role-assignment identity (name / email / login / user id).
  A team's membership is a COUNT; a role assignment is COUNTED by scope, never by
  who.

**The mechanism (three layers, structural-first):**

1. **The type system is the guard.** The record structs (`RawProvider`,
   `RawTeamStats`, `RawRoleStats`, `Provider`, `AccessConfig`) have NO field that
   can hold a secret, a key, a certificate, or an identity. There is nowhere to
   put one. `TestStructuralOverCollectionGuard` pins the field set by reflection
   and fails the build the moment a banned-substring field
   (`secret`/`token`/`key`/`password`/`certificate`/`private`/`email`/`login`/…)
   is added.
2. **The decode boundary drops it.** In `client.go` the decode structs
   (`apiSSOSetting.Settings`, `apiTeamSearch.Teams[]`, `apiRoleAssignment`)
   declare ONLY the safe keys. Go's `json.Decode` silently discards any JSON key
   with no matching struct field, so the SAML private key / OAuth client secret /
   LDAP bind password / signing cert and the team-member `members[]` rows + the
   assignment's `userLogin`/`userEmail` are never materialized in connector
   memory — even though the fake response carries them.
3. **The drop test proves it end-to-end.** `TestClient_NeverLeaksSecretOrPII`
   stands up an `httptest` server whose `sso-settings` body embeds a SAML
   `private_key` + a `client_secret` + a `bind_password` + a `certificate`, whose
   `teams/search` body embeds member `login`/`email`/`name`, and whose
   `access-control/assignments` body embeds `userLogin`/`userEmail`. It then
   Collects → Builds the record and asserts NONE of the fake secret/identity
   literals appears in the serialized payload, and that no payload key names a
   secret/identity field. A companion (`TestClient_RoleMappingsSafe`) proves the
   role-mapping rule NAMES _are_ captured, so the drop test is not vacuously
   passing by dropping everything.

## D3 — Auth-scope minimum + the Admin warning (this slice owns it)

488's minimum was the read-only **Viewer** role. Reading SSO settings needs MORE
than Viewer; this slice owns documenting the PRECISE additional minimum and
warning against the "grant Admin to be safe" anti-pattern.

- **The precise additional read-only minimum (documented, not Admin):**
  - SSO settings (`GET /api/v1/sso-settings`): `settings:read` scoped to
    `settings:auth.*`, carried by Grafana's built-in fixed role
    `fixed:settings:reader`.
  - RBAC assignments (`GET /api/access-control/assignments`): `roles:read` +
    `users.roles:read` + `teams.roles:read`, carried by the built-in fixed role
    `fixed:roles:reader`.
- **Surfaced in code + docs:** `grafanaauth.RequiredAccessConfigScopes()` returns
  the two documented permissions (with `SSOSettingsReadPermission` +
  `AccessControlReadPermission` constants); the `permissions` subcommand prints a
  per-surface table and a warning that explicitly bans `Editor`/`Admin` "to be
  safe"; the README's auth table documents both fixed roles.
- **Coverage ratchet (b228):** the new `grafanaauth` lines ship fully tested
  (`TestRequiredAccessConfigScopes`), so the package's 98% floor holds — it
  measures **100%**. (Contrast slice 533's `datadogauth.RequiredScopes()`, which
  shipped 0%-covered and dropped the package below its floor → CI block. Not
  repeated here.)
- **Read-only, source-side, never in a record:** the connector has no write code
  path; the token is read from `GRAFANA_TOKEN` (never a flag), never logged, and
  the credential redacts the token on every format path (488's guard, unchanged).

## D4 — SCF anchor choice: IAC-06 + IAC-22 → CC6.x

- **Chosen:** `x-default-scf-anchors = [IAC-06, IAC-22]` — IAC-06 Authenticator
  Management (SSO is enforced) + IAC-22 Least Privilege (access is role-based).
- **Rationale:** the recurring SOC 2 demand is CC6.1 ("logical access controls"),
  CC6.2 ("registration/authorization of access"), and CC6.3 ("role-based
  access"). `sso_enabled` + provider posture maps to IAC-06 (authenticator
  enforcement); the org-role mapping + the RBAC role-assignment counts map to
  IAC-22 (least privilege / role-based). The candidate anchors in the slice doc
  were adopted verbatim.

## D5 — Stable-field + bounding choices (locked connector pattern)

- `actor_id = connector:grafana:ssoconfig@<version>` (cross-connector convention,
  register-per-run).
- `profiles_supported = [pull]`; the interval is named honestly (operator-
  scheduled, recommended 24h — NOT continuous monitoring).
- sha256-derived idempotency key per record; `observed_at` truncated to the UTC
  hour so same-hour re-runs collapse to one ledger row.
- Bounded (DoS guard, threat-model D): per-endpoint decode cap (`maxItems`),
  bounded body read (16 MiB `io.LimitReader`), 20s HTTP client timeout, 10s
  per-push timeout. `TestClient_BoundedCap` proves the provider decode caps.
- Counts are clamped non-negative (`nonNeg`) so a malformed source can't push a
  negative count.
- Migration: none — the kind ships via `DefaultSeed` + the bundled JSON schema;
  the schema-drift bijection test passes with the new kind.

## Detection-tier classification (slice 353 Q-13)

- `detection_tier_actual`: `none` — no defect surfaced during the slice. The
  cmd-layer test failures hit during development were expected fallout from
  wiring the second run-pass + changing the `permissions` table (the seam-test
  harness and the `permissions` assertion both needed updating for the new
  surface), caught at the `unit` tier by the existing grafana cmd suite before
  any push — i.e. the test harness did its job; these were test-fixture updates,
  not product defects.
- `detection_tier_target`: `none`. Had a secret/PII-leak regression been
  introduced, the target tier is `unit` — the structural reflection guard
  (build-time) + the `client_test.go` drop test (unit) are designed to catch a
  leak before integration or production, which is the correct lowest-cost tier
  for an over-collection regression.

## Revisit once in use

- If a Grafana deployment exposes per-provider SSO _enforcement_ posture beyond a
  simple `enabled` flag (e.g. "SSO required for all users" vs "SSO optional"),
  consider adding an enforcement-mode field — but only if it is a config flag,
  never an identity.
- The `/api/teams/search` + `/api/access-control/assignments` shapes are modeled
  from the documented Grafana API; if a deployment paginates teams beyond the cap,
  the team count falls back to `totalCount` (honored when present). Revisit
  pagination if large installs surface a truncated count.
