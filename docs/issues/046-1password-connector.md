# 046 — 1Password connector

**Cluster:** Connectors
**Estimate:** 0.5d
**Type:** AFK

## Narrative

Implement the 1Password connector at `connectors/1password/`. One evidence_kind: `1password.org_policy.v1` (org password policy state — length, complexity, rotation, 2FA enforcement on the 1Password org itself). Pull-only via 1Password Connect / Business API. The slice delivers value because password-policy controls and 1Password's own org-level posture become observable.

## Acceptance criteria

- [ ] AC-1: Connector binary registers; `GET /v1/connectors` lists `1password`
- [ ] AC-2: `1password.org_policy.v1` produces pass/fail with policy fields
- [ ] AC-3: Auth via 1Password service account token
- [ ] AC-4: Records carry provenance + scope tags

## Constitutional invariants honored

- **Anti-pattern rejected (no proprietary agents):** uses 1Password's standard API

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.2 (1Password in v1 roster)

## Dependencies

- #003, #013

## Anti-criteria (P0)

- Does NOT request more 1Password scopes than necessary

## Skill mix (3–5)

- Go + 1Password API
- Service-account auth
- gRPC connector contract
- Idempotency key derivation
