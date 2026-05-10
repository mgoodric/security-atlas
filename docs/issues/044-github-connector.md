# 044 — GitHub connector

**Cluster:** Connectors
**Estimate:** 1d
**Type:** AFK

## Narrative

Implement the GitHub connector at `connectors/github/`. Reuses the connector contract from slice 003 + the AWS reference from slice 004. Three evidence_kinds: `github.repo_protection.v1` (branch protection state per repo), `github.audit_event.v1` (org audit log events streamed), `github.scim_user.v1` (SCIM-provisioned user state). Pull-based with webhook subscription for audit events. The slice delivers value because GitHub-anchored controls (e.g., code-review enforcement, signed commits, branch protection on `main`) get real evidence.

## Acceptance criteria

- [ ] AC-1: Connector binary registers; `GET /v1/connectors` lists `github`
- [ ] AC-2: Three evidence_kinds emit records with full provenance + scope tags
- [ ] AC-3: `github.repo_protection.v1` per repo: produces pass/fail based on branch protection rules
- [ ] AC-4: `github.audit_event.v1` ingests events via webhook subscription; idempotency_key derived from event id
- [ ] AC-5: `github.scim_user.v1` reconciles user lifecycle; tied to slice 035 RBAC
- [ ] AC-6: GitHub auth via PAT (with documented least-privilege scopes) or GitHub App

## Constitutional invariants honored

- **Invariant 3 (two SDK profiles):** both pull (schedule) and webhook-subscribe
- **Anti-pattern rejected (no proprietary agents):** uses GitHub's standard API

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.2 (v1 connector roster — GitHub)

## Dependencies

- #003, #013

## Anti-criteria (P0)

- Does NOT require admin PAT — least-privilege scopes documented
- Does NOT skip webhook signature verification

## Skill mix (3–5)

- Go + GitHub API client
- Webhook signature verification
- Idempotency key from event id
- gRPC connector contract
- SCIM reconciliation
