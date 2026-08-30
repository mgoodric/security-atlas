**security-atlas canvas** · [← index](../ARCHITECTURE_CANVAS.md)

---

# 8. Audit Workflow

## 8.1 Auditor role

A dedicated `auditor` role with:

- Read-only access to evidence, controls, scopes, exceptions, policies.
- Sample-pull tools (random N from population, deterministic seed for reproducibility).
- Walkthrough recording (annotated screen captures + transcript stored alongside evidence).
- Their own workspace for organizing testing notes — not visible to the auditee.
- A time-window scope (auditor sees state as of `audit_period_end`, not live).

This role is **first-class**, not an afterthought. Auditors who can do their work in our tool become advocates; auditors who can't insist on Vanta or spreadsheets.

## 8.2 OSCAL SSP / POA&M export

| Artifact                     | Generated from                                                                                        |
| ---------------------------- | ----------------------------------------------------------------------------------------------------- |
| SSP (`system-security-plan`) | Org profile + scope cells + applicable controls + control implementation narratives + linked policies |
| Assessment Plan              | Auditor's selected sample population + planned procedures                                             |
| Assessment Results           | Sampled evidence records + auditor pass/fail/finding annotations                                      |
| POA&M                        | Open findings with milestones, owners, due dates                                                      |

We commit to OSCAL JSON v1.1.x compatibility and ship an `oscal-export` CLI alongside the UI export.

> **OSCAL covers security primitives only.** The OSCAL schema family (catalog / profile / component-definition / SSP / AP / AR / POA&M) is purpose-built for security control programs (NIST 800-53 / FedRAMP / etc.). It does NOT carry data-subject, processing-activity, DPIA, or other privacy primitives. When the privacy sibling module ships (privacy v0, v2+ per [canvas OQ #7 resolution](./11-open-questions.md)), privacy-side exports use **W3C Data Privacy Vocabulary (DPV) as JSON-LD** — NOT OSCAL. The two export wire formats live side-by-side: an audit-period export bundle for a privacy-aware tenant will contain BOTH an OSCAL `assessment-results` document (security) AND a DPV-JSON-LD `Activity` graph (privacy). Audit periods are shared; their export bundles are split by module. Foundation pre-commitment landed via slice 180 (audit-log `subject_module` column).

## 8.3 Walkthrough and sample-pull primitives

- `Population(control, scope_predicate, time_window)` — defines what a sample is drawn from.
- `Sample(population, n, seed)` — deterministic, reproducible.
- `Walkthrough(control, narrative, attachments[])` — auditor or owner recorded explanation, hashed and signed.
- `Finding(control, severity, description, linked_evidence[])` — drives POA&M.
- `AuditPeriod(audit_id, period_start, period_end, frozen_at)` — see [§8.4 Audit-period freezing](#84-audit-period-freezing-the-snapshot-primitive).
- `AuditNote(scope: control | finding | sample, author, body, visibility)` — auditor↔auditee threaded comments inside the tool.

These primitives compose. An audit cycle is a graph of populations, samples, walkthroughs, findings, frozen periods, and notes against the control set.

## 8.4 Audit-period freezing (the snapshot primitive)

A recurring practitioner complaint about Vanta/Drata is **post-window evidence pollution** — a control is failing on the day of the auditor walkthrough but passes the next morning, and the sample population shifts under the auditor's feet. We solve this with explicit freezing.

When an `AuditPeriod` is created, the user (or auditor) calls `freeze(period_id, frozen_at)`. From that moment:

- Sample populations for that period draw only from evidence with `observed_at ≤ frozen_at`.
- Control state for the period is computed against frozen evidence; live state continues independently.
- New evidence after `frozen_at` does not retroactively change the auditor's view.
- Frozen state is hashed and signed; tampering is detectable.

The append-only evidence ledger makes this cheap — we don't need separate snapshots, we just shift the read horizon. This is one of the practical wins of the event-driven evidence architecture (see [§4 Evidence Engine](./04-evidence-engine.md)).

## 8.5 Auditor collaboration (the "Audit Hub" pattern)

Practitioners cite Drata's in-product auditor↔auditee comment thread as the single most valuable feature when migrating between tools. We replicate it as a first-class workflow:

- Auditor leaves a comment on a control / sample / finding.
- Auditee receives a notification, replies in-product, attaches additional evidence.
- Comment thread is retained as an audit artifact, exported to OSCAL `assessment-results` `observation` annotations.
- No email back-and-forth. No "I'll send you the screenshot in Drive" loop.

This is not a separate "messaging" feature — it's threaded annotations on first-class objects.

## 8.6 Assessor delivery (the outbound seam)

Everything in 8.1-8.5 assumes the auditor comes to the platform. Real engagements
run the other way too: the audit firm has its own audit-management platform
(A-LIGN's A-SCEND, Johanson's portal, Prescient's), and the operator's last mile
is still a zip file, a shared drive, or a portal upload done by hand. That last
mile is where evidence goes stale, where version mismatches enter the record, and
where the "which file did we actually send?" question becomes unanswerable.

security-atlas closes it with a **delivery seam**: approved, in-period evidence
leaves the platform over a typed adapter to a registered assessor destination,
and every departure is written to an append-only delivery ledger.

**Delivery is not Push.** Invariant #3 reserves "push" for the single inbound
`EvidenceIngestService.Push` wire surface — connectors pushing evidence _into_ the
platform. Outbound movement to an assessor is **delivery**, never push. The two
directions never share a noun.

Four commitments:

- **Destinations are registered, tenant-scoped objects.** An `AssessorDestination`
  binds an adapter kind, an endpoint, a credential reference, and the
  framework/audit-period it may receive. A destination is not an ad-hoc URL typed
  at delivery time.
- **The delivery ledger is append-only**, sibling to the evidence ledger and
  governed by the same rule: a delivery record is written, never mutated. It
  captures actor, destination, audit period, the exact evidence record IDs and
  their content hashes, the payload digest, and the remote receipt. "What did we
  send the auditor, and when" is a query, not an archaeology exercise.
- **Delivery is gated on approval and on the period horizon.** Nothing leaves
  without an explicit human action per delivery, and a frozen `AuditPeriod`
  delivers only evidence with `observed_at <= frozen_at` (invariant #10). An
  AI-drafted artifact that has not been human-approved is structurally
  undeliverable (the CLAUDE.md AI-assist boundary applies at the seam, not per
  renderer).
- **The payload is the signed OSCAL bundle.** Delivery does not invent a wire
  format; it transports what `internal/oscal` already produces and cosign already
  signs. A vendor adapter may reshape that bundle into the firm's API schema, but
  the signed bundle is the artifact of record on our side.

Adapters are ordinary connectors in reverse: a `Deliverer` interface with one
open, credential-free implementation shipped in-tree (signed-bundle HTTP POST to
any endpoint the operator controls), and vendor adapters added as partner API
access allows. Closed vendor adapters that cannot be run by a self-hoster are the
anti-pattern here, same as closed inbound connectors.

---

[← Canvas index](../ARCHITECTURE_CANVAS.md) · [← 7. Metrics](./07-metrics.md) · **Next:** [9. Architecture and Tech Stack →](./09-tech-stack.md)
