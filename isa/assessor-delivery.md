---
phase: specified
progress: 0
---

# Assessor delivery

Epic ISA. Inherits the Constraints in `isa/ISA.md` and cannot violate them.

## Problem

`internal/oscal` builds a bundle and `internal/oscal/sign*.go` cosigns it. Then
it becomes a file. The operator downloads it, uploads it into the audit firm's
platform, and the record of what was sent lives in their sent-mail.

That leaves initiative ISC-3 true of the wrong scope. The evidence ledger can
reconstruct any past date of the platform's _internal_ state, and cannot answer
"which evidence records did the auditor receive on June 3rd, and were they the
versions we think they were." The gap opens at the exact moment an artifact
becomes audit-binding, which is the one moment the record has to hold.

It is also the only capability gap a competitive read found. A practitioner's
internally-built single-tenant GRC tool (four screens: freshness, validation,
delivery) delivers evidence into A-LIGN's API directly. It has no control
graph, no crosswalk, no ledger, no tenancy. It has the last mile.

## Vision

Approved, in-period evidence leaves over a typed adapter to a registered
destination, and every departure is a row. "What did the auditor get" is a
query. The operator never opens a portal to upload a file the platform already
produced.

## Out of Scope

- Any firm-named adapter. Every audit firm's intake API is partner-gated: no
  public schema, no sandbox, credentials issued per commercial relationship.
  Shipping code no self-hoster can exercise is the closed-connector
  anti-pattern wearing a different hat.
- Scheduled or automatic delivery. Every departure in this epic is an explicit
  human action. Recurrence is a separate approval question.
- Replacing the OSCAL bundle. Delivery transports what already exists.

## Constraints

- **Delivery is not Push.** `EvidenceIngestService.Push` is the one inbound
  wire surface (C3). Outbound movement to an assessor is _delivery_. No symbol,
  table, column, route or CLI verb in this epic is named `push`. The two
  directions never share a noun, because the direction is the whole point.
- The egress path reuses `internal/notify` — `SSRFPolicy.ValidateWebhookURL`,
  the redacting `Secret` type, `ScrubSecret`. A second SSRF implementation in
  this tree is a defect regardless of whether it is correct.

## Goal

An audit period is delivered to a real assessor endpoint, and afterwards the
question "which records, which versions, sent by whom, when" is answered from
`assessor_deliveries` alone without opening a browser.

## Claims

### D1 · The seam

- [ ] ISC-1: Evidence leaves only through a registered destination. Falsifier:
      a delivery whose endpoint was supplied by the request rather than read
      from an `assessor_destinations` row.
- [ ] ISC-2: Every departure is reconstructible from the ledger. Falsifier: a
      delivered bundle whose evidence-record set and content hashes cannot be
      recovered from `assessor_deliveries` and shown to equal what was sent.

### D2 · The gate

- [ ] ISC-3: No artifact reaches an assessor without a recorded human
      approver. This is initiative ISC-4 at the surface where it is hardest to
      hold, because delivery is the moment the artifact leaves. Falsifier: a
      delivered record carrying `ai_assisted=true` whose approver is null, or
      an assembly path that skips the guard.
- [ ] ISC-4: A frozen period delivers nothing observed after its freeze.
      Falsifier: a delivered record whose `observed_at` is later than the
      period's `frozen_at`.

### D3 · The egress boundary

- [ ] ISC-5: No delivery request reaches a private, loopback,
      link-local or metadata address. Falsifier: an outbound request from the
      delivery path to any such address, at registration time or at delivery
      time. Both are required: a registration-time check alone loses to DNS
      rebinding.
- [ ] ISC-6: No destination credential is readable back. Falsifier: a
      plaintext credential in an API response, CLI output, log line, error
      string or ledger row.

### D4 · Tenancy

- [ ] ISC-7: A delivery carries exactly one tenant's evidence. Falsifier: a
      delivered bundle containing a record whose tenant differs from the
      destination's.

### D5 · Openness

- [ ] ISC-8: A self-hoster can deliver without a commercial relationship.
      Falsifier: every shipped adapter requiring vendor-issued credentials.
      This is initiative ISC-5 applied to the one surface most likely to erode
      it, since the convenient path here runs through a partner agreement.

## Test Strategy

No probe listed below exists yet. That is deliberate and follows `isa/ISA.md`:
the engine treats an unrunnable probe as a third value, so these claims report
`unverifiable` rather than closing on silence. Leaving them untabled would
report them as `manual`, which would be false — seven of the eight are
machine-checkable and are simply unchecked today.

| isc   | tier    | type | check                                                                              | threshold | tool                                           |
| ----- | ------- | ---- | ---------------------------------------------------------------------------------- | --------- | ---------------------------------------------- |
| ISC-1 | service | sql  | no delivery row whose endpoint is absent from `assessor_destinations`              | 0         | `bash isa/probes/delivery-registered-only.sh`  |
| ISC-2 | service | bash | bundle rebuilt from the ledger equals the delivered digest                         | 0         | `bash isa/probes/delivery-replay.sh`           |
| ISC-3 | service | sql  | no delivered record is ai_assisted without an approver                             | 0         | `bash isa/probes/delivery-approver-guard.sh`   |
| ISC-4 | service | sql  | no delivered record has observed_at later than frozen_at                           | 0         | `bash isa/probes/delivery-freeze-horizon.sh`   |
| ISC-5 | service | bash | delivery to metadata, loopback and RFC1918 targets is refused with no request made | 0         | `bash isa/probes/delivery-ssrf.sh`             |
| ISC-6 | service | bash | credential absent from every response, log and ledger row                          | 0         | `bash isa/probes/delivery-secret-redaction.sh` |
| ISC-7 | service | sql  | no delivered bundle mixes tenants                                                  | 0         | `bash isa/probes/delivery-tenant-isolation.sh` |
| ISC-8 | service | bash | at least one shipped adapter completes with no vendor credential                   | 1         | `bash isa/probes/delivery-open-adapter.sh`     |

ISC-5's probe answers from an attempted delivery, not by reading config. ISC-2
is the load-bearing one: if the replay does not reproduce what was sent, every
other claim in D1 is unfalsifiable.

## Anti-claims

- A1: The platform does not claim the assessor accepted the evidence. It
  claims what left, when, and what receipt came back. Acceptance is the
  auditor's conclusion.
- A2: Delivery does not make the audit faster. It makes the record of the
  handoff exist.

## Not yet specified

- Whether a vendor adapter ever lives in this tree. Three shapes are stated in
  `Plans/canvas/11-open-questions.md` #22 — in-tree vendor adapters, open
  adapter only, or an out-of-tree registry. ISC-8 holds under all three, so the
  question blocks the first firm-named adapter and not this epic.
- Redelivery semantics. Whether a second delivery of the same period appends a
  new row (favored) or is an idempotent no-op. ISC-2's falsifier is written to
  hold either way.
- Partial acceptance. If an adapter reports some records accepted and some
  rejected, whether the row terminates `failed` or `partial`, and what ISC-2's
  replay compares against in that case.
- What the delivery surface looks like in `web/`. This epic is the seam and the
  CLI; the queue UI is unwritten.

## Decisions

Ratified decisions live in `docs/adr/`. The ones that bound these claims:

| ADR  | Bears on                                                                                        |
| ---- | ----------------------------------------------------------------------------------------------- |
| 0012 | append-only evidence ledger — the delivery ledger is a sibling with the same discipline (ISC-2) |
| 0003 | audit-period freeze hash inputs (ISC-4)                                                         |
| 0011 | RLS tenant isolation (ISC-7)                                                                    |
| 0006 | board-narrative AI assist, the approval guard shape (ISC-3)                                     |

Canvas §8.6 (`Plans/canvas/08-audit-workflow.md`) carries the design
commitment these claims are the falsifiers for.

## Changelog

### 2026-08-29 · specified

Filed from a competitive read of a practitioner's internally-built GRC tool
that delivers evidence to A-LIGN over their API. Assessor delivery was the only
capability it had that this platform lacked; every other axis (crosswalk,
ledger, tenancy, freezing, connector count) already favored this one.

Canvas §8.6 written the same day, plus OQ #22 stating the vendor-adapter
coupling fork so that the first firm-named adapter is blocked and the seam is
not.

The naming constraint in Constraints is the finding worth keeping. The obvious
name for this feature is "push", which is already the name of the inbound wire
surface under C3. Two directions sharing one noun would make the codebase's
most important directional distinction ambiguous at exactly the point where
getting the direction wrong sends tenant evidence outward.
