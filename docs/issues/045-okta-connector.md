# 045 — Okta connector

**Cluster:** Connectors
**Estimate:** 1d
**Type:** AFK

## Narrative

Implement the Okta connector at `connectors/okta/`. Two evidence_kinds: `okta.mfa_policy.v1` (MFA enforcement policy state across the org) and `okta.system_log.v1` (System Log events for password changes, MFA enrollment, suspicious activity). Pull-based with webhook (System Log event hooks). SCIM-based user reconciliation feeds RBAC role assignments (slice 035). The slice delivers value because workforce-IAM controls (MFA enforcement, account lifecycle) get real evidence — directly feeds the MFA control from slice 010.

## Acceptance criteria

- [ ] AC-1: Connector binary registers; `GET /v1/connectors` lists `okta`
- [ ] AC-2: `okta.mfa_policy.v1` per workspace: produces pass/fail with `webauthn_pct`, `totp_pct`, `unenrolled_count`
- [ ] AC-3: `okta.system_log.v1` ingests events via event hook; idempotency keyed by event uuid
- [ ] AC-4: Okta auth via API token (least-privilege)
- [ ] AC-5: Auth flow documented; recovery from rate limits respected

## Constitutional invariants honored

- **Invariant 3 (two SDK profiles):** pull + subscribe both
- **Anti-pattern rejected (no proprietary agents):** Okta's published APIs only

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.2 (Okta in v1 roster)

## Dependencies

- #003, #013

## Anti-criteria (P0)

- Does NOT require Super Admin token — least-privilege documented
- Does NOT skip event hook signature verification

## Skill mix (3–5)

- Go + Okta API client
- Event hook signature verification
- Idempotency from event uuid
- gRPC connector contract
- Okta policy modeling
