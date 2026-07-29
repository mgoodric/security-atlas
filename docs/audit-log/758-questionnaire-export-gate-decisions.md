# 758 — Questionnaire Export Approval Gate Decisions

**Date:** 2026-07-29
**Type:** JUDGMENT
**Slice:** 758 — approval-gated questionnaire export + export audit trail

## Decision

Use **exclude-with-visible-summary** for unapproved AI draft answers at export time.

Manual answers and approved AI answers render unchanged. Answers with
`ai_assisted=true AND human_approved=false` are classified as
`unapproved_draft`, converted to unanswered rows before PDF rendering, and
counted in the response summary: `N drafted answers pending approval were
excluded`.

## Rationale

Canvas §4.6.3/§4.6.4 model questionnaires as a response workflow that can move
through draft/review/approved/sent while operators still need to return a
legitimate partial answer set. Blocking the whole export would protect the
AI-assist boundary, but it would also punish the operator who has manually
answered part of a customer questionnaire and wants to send that subset now.

The constitutional boundary is narrower and harder: unapproved AI text must not
cross the publish boundary. Excluding drafts while surfacing the count satisfies
that boundary and keeps manual evidence first-class.

## Revisit Triggers

- Operators report that partial exports are too easy to misunderstand.
- A future export format cannot visibly surface the exclusion summary before
  and after export.
- Questionnaire response statuses grow a strict `approved`/`sent` transition
  that product wants to use as the only customer-return path.

## Detection Tier

- Unit: shared classification and PDF-input construction prove manual /
  approved-AI / unapproved-draft handling in one spot.
- Integration: mixed manual + approved AI + draft export produces audit counts
  `1/1/1`, returns exclusion headers, and the draft narrative is absent from
  PDF bytes.
- Integration: approving the draft then re-exporting records `1/2/0`, proving
  the gate keys on the approval columns.
- Integration: tenant B cannot export tenant A's questionnaire.

## Assertion Note

The byte-level absence assertion is direct: the unapproved draft narrative is
not present in the PDF byte stream. Positive PDF text inclusion is not asserted
byte-for-byte because Chrome's PDF encoding can compress or subset text; the
approve-then-reexport path is asserted through the shared gated input and the
audit counts.
