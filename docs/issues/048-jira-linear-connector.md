# 048 — Jira / Linear ticket evidence connector

**Cluster:** Connectors
**Estimate:** 1d
**Type:** AFK

## Narrative

Implement the Jira and Linear connectors at `connectors/jira/` and `connectors/linear/`. Both share an evidence_kind: `ticket.change_event.v1` — captures change-management tickets (CR creation, approval, deployment, closure) and incident-response tickets (creation, triage, resolution). Powers SOC 2 controls around change management and incident response. Pull + webhook patterns. The slice delivers value because change-management and IR controls get real evidence — auditors stop demanding screenshots of Jira filters.

## Acceptance criteria

- [ ] AC-1: Both connector binaries register; visible in `GET /v1/connectors`
- [ ] AC-2: `ticket.change_event.v1` ingests CR-tagged tickets with state transitions
- [ ] AC-3: Webhook subscription registered; idempotency key derived from ticket_id + event_timestamp
- [ ] AC-4: Auth via OAuth (Jira) and API token (Linear)
- [ ] AC-5: Records tagged with scope cell based on project / team

## Constitutional invariants honored

- **Anti-pattern rejected (no proprietary agents):** read-only consumers of vendor APIs

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.2 (Jira/Linear in v1 roster)

## Dependencies

- #003, #013

## Anti-criteria (P0)

- Does NOT request workspace-admin scope beyond what's necessary
- Does NOT skip webhook signature verification

## Skill mix (3–5)

- Go + Jira/Linear API clients
- OAuth dance
- Webhook signature verification
- Project-to-scope mapping
- gRPC connector contract
