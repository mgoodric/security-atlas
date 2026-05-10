# 047 — osquery / Fleet endpoint connector

**Cluster:** Connectors
**Estimate:** 1d
**Type:** AFK

## Narrative

Implement the osquery/Fleet endpoint posture connector at `connectors/osquery/`. Ingests host evidence from a Fleet (open-source endpoint platform) deployment: disk encryption, screen-lock, EDR coverage, OS patch level, browser/extension policy. One evidence_kind: `osquery.host_posture.v1`. osquery itself is the agent — we do NOT ship a proprietary agent. Pull from Fleet's API. The slice delivers value because endpoint controls (disk encryption, screen lock, EDR coverage) get real evidence — and the platform demonstrates the "use open agents, never proprietary" anti-pattern rejection.

## Acceptance criteria

- [ ] AC-1: Connector binary registers; `GET /v1/connectors` lists `osquery`
- [ ] AC-2: `osquery.host_posture.v1` per host: produces evidence with disk_encryption, screen_lock_enabled, edr_running, os_patch_level
- [ ] AC-3: Pull-based from Fleet API; auth via Fleet token
- [ ] AC-4: Scope tagging: `cloud_account=workforce`, `data_classification` inferred per device type
- [ ] AC-5: Idempotency key derived from `host_uuid + observation_window`

## Constitutional invariants honored

- **Anti-pattern rejected (no proprietary agents):** uses osquery (open source); we are read-only consumer of Fleet
- **Invariant 3 (two SDK profiles):** pull-based

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.2 (osquery/Fleet in v1 roster)
- `Plans/canvas/01-vision.md` §1.6 (anti-pattern "collector agent on every laptop")

## Dependencies

- #003, #013

## Anti-criteria (P0)

- Does NOT ship our own endpoint agent (Fleet/osquery is the substrate)
- Does NOT collect more host data than declared schema

## Skill mix (3–5)

- Go + Fleet REST API
- osquery result parsing
- gRPC connector contract
- Per-host scope inference
- Idempotency key derivation
