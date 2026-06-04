# 443 — Slack connector

**Cluster:** Connectors
**Estimate:** M (1-2d)
**Type:** JUDGMENT (evidence-kind shape + scope-minimum + stable-field choices)
**Status:** `ready`

## Narrative

Roadmap §10.2 names Slack in the connector-roster expansion. For the platform's
persona — a SaaS startup security leader — Slack is high-leverage evidence: a
SaaS startup runs its operations in Slack, so "who has access to the workspace,"
"what the admin audit log shows," and "what the message-retention setting is"
are recurring SOC 2 / ISO access-control + data-retention evidence demands that
today require manual upload.

This slice ships **one vertical Slack connector** following the slice-004 / GCP
(slice 442) connector template: collect Slack **workspace member list** (access
evidence), **admin audit-log** entries (admin-action evidence), and
**message-retention settings** (data-retention evidence) via the Slack admin /
audit APIs, register `profiles_supported` per run, and `Push` each record to the
platform's single `IngestEvidence` API.

**Scope discipline.** **One connector, three evidence surfaces** (members,
admin audit-log, retention settings) — the minimum that proves Slack is a real
first-class peer connector. It does **not** ship message-content collection
(deliberately — see threat model; only membership/admin/retention metadata),
does **not** ship a real-time event subscription (pull-profile only; honest
interval), and does **not** change the platform-side wire (push-only —
invariant #3). **Follow-on slices:** Slack channel-membership granularity; SCIM
provisioning-event evidence; event-driven profile via Slack audit-log streaming.

## Threat model (STRIDE) — connector family (source-credential heavy)

A connector holds **source-side credentials** — here a Slack admin/audit OAuth
token scoped to the customer's workspace. The dominant risks are credential
handling, over-collection (Slack holds extremely sensitive message content the
connector must NOT touch), and keeping the platform wire push-only.

**S — Spoofing.** The connector authenticates TO the platform via its push
credential and TO Slack via a workspace OAuth token. Risk: a stolen push
credential, or a Slack token with broad scopes.
**Mitigation:** push reuses the existing connector credential boundary; Slack auth
uses a least-privilege token scoped to `admin.users:read` /
`auditlogs:read` / `admin.conversations:read`-class scopes only, documented as
the minimum. The Slack token stays source-side (invariant #3).

**T — Tampering.** Each pushed record carries a sha256 content-hash.
**Mitigation:** content-hash per record validated at ingest; the connector only
reads Slack + pushes (no inbound surface).

**R — Repudiation.** Which run produced which evidence must be traceable.
**Mitigation:** register-per-run + stable `actor_id` + documented `observed_at`
granularity (slice 004 pattern).

**I — Information disclosure (PRIMARY for Slack).** Slack contains highly
sensitive message content; the connector must collect ONLY
membership/admin/retention metadata, never message bodies.
**Mitigation:** the connector reads workspace member lists, admin audit-log
entries, and retention **settings** — explicitly NOT message content, NOT DMs,
NOT channel message history. The required Slack scopes deliberately exclude
message-read scopes. A test asserts no message body ever enters an evidence
record. The Slack token is never logged.

**D — Denial of service.** A large workspace (thousands of members, large audit
log) could make a run unbounded.
**Mitigation:** paginated Slack API reads with bounded page sizes + per-run cap;
pull on a named interval; run timeout.

**E — Elevation of privilege.** Risk: the Slack token granted broad admin
scopes "to be safe."
**Mitigation:** read-only least-privilege scopes only; docs name the exact
minimal scopes and warn against broad grants; no platform-side privilege beyond
push.

## Acceptance criteria

**Connector — collection**

- [ ] **AC-1.** A `connectors/slack/` connector lands following the
      slice-004/442 template (register-per-run, stable `actor_id`, `observed_at`
      granularity, scope minimums).
- [ ] **AC-2.** It collects Slack **workspace member list** (access evidence)
      via the admin users API.
- [ ] **AC-3.** It collects **admin audit-log** entries (admin-action evidence)
      via the audit-logs API.
- [ ] **AC-4.** It collects **message-retention settings** (data-retention
      evidence).
- [ ] **AC-5.** It authenticates via a least-privilege Slack OAuth token
      (read-only admin/audit scopes), documented as the minimum.

**Connector — push**

- [ ] **AC-6.** Each record is pushed to the single `IngestEvidence` (`Push`)
      API — no platform-side wire change (invariant #3).
- [ ] **AC-7.** Each record carries a sha256 content-hash + stable optional
      fields.
- [ ] **AC-8.** The connector registers `profiles_supported` (`pull` in v0)
      per run; the pull interval is named honestly.

**Evidence schema**

- [ ] **AC-9.** The Slack member-list / admin-audit / retention evidence_kind
      schemas land with `x-default-scf-anchors` set (OQ #9 governance).

**Tests**

- [ ] **AC-10.** Connector unit/integration tests cover collect → push against a
      mocked Slack API (no live Slack in CI).
- [ ] **AC-11.** A test asserts the connector emits NO message content / DM /
      channel-history data — metadata only (threat-model I).
- [ ] **AC-12.** A test asserts the connector never logs the Slack token.

**Docs / JUDGMENT artifact**

- [ ] **AC-13.** A connector README documents the minimal Slack scopes, the pull
      interval, and the evidence kinds.
- [ ] **AC-14.** A decisions log
      (`docs/audit-log/443-slack-connector-decisions.md`) records the
      evidence-kind + scope-minimum + stable-field JUDGMENT calls.
- [ ] **AC-15.** A changelog entry.

## Constitutional invariants honored

- **#3 — Single canonical inbound API (`IngestEvidence` / `Push`).** First-class
  peer connector; source-side credential; push-only platform wire.
- **Licensing — no closed proprietary connectors.** OSS, in-tree, read-only API.
- **Evidence integrity.** sha256 content-hash per record.
- **Anti-pattern: honest intervals.** Pull profile names its interval.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.1 — connectors + push wire +
  `profiles_supported`.
- `Plans/canvas/10-roadmap.md` §10.2 — connector roster (Slack named).
- `Plans/EVIDENCE_SDK.md` — SDK contract.

## Dependencies

- **#003** (Evidence SDK proto + push + CLI) — `merged`.
- **#004** (AWS connector exemplar) — `merged`. Pattern template.
- **#442** (GCP connector) — sibling connector; NOT a hard dep (both follow the
  same slice-004 template independently).
- **#191** (SDK OAuth client_credentials) — `merged`. Push credential.

## Anti-criteria (P0 — block merge)

- **P0-443-1.** Does NOT collect message content / DMs / channel history —
  membership/admin/retention metadata only (threat-model I).
- **P0-443-2.** Does NOT widen the platform-side wire — push only (invariant #3).
- **P0-443-3.** Does NOT require or document broad/write Slack scopes —
  read-only least-privilege only (threat-model E).
- **P0-443-4.** Does NOT log or transmit the Slack token into the platform.
- **P0-443-5.** Does NOT ship a closed/proprietary collector (licensing).
- **P0-443-6.** Does NOT label the pull profile "continuous monitoring."
- **P0-443-7.** Does NOT implement channel-level / SCIM evidence — follow-ons.

## Skill mix (3-5)

`grill-with-docs` · `tdd` (collect→push round-trip; mocked Slack API) ·
`security-review` (source-credential + over-collection risk) · `simplify` ·
`changelog-generator`.

## Notes for the implementing agent

- **Phase-2 grill output:** Slack's defining risk vs other connectors is
  over-collection — Slack holds message content the connector must never touch.
  The scope-minimum discipline (no message-read scopes) is the load-bearing
  guard; test it explicitly (AC-11).
- **JUDGMENT calls you own:** evidence-kind field shapes, `x-default-scf-anchors`
  per kind, scope minimum. Record in the decisions log.
- Mirror slice 442's connector structure for consistency; both are the
  slice-004 pattern.
- Detection-tier: `none` unless a bug surfaces.
