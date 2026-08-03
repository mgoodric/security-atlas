# Slice 757 — Questionnaire Batch Review Queue Decisions

## Decision Summary

Slice 757 is the operator half of the batch answer flow: a "Draft all answers"
control that starts a slice-756 run, and a review queue that renders every
unapproved draft with its citations and model provenance for per-answer
approve / edit / reject. The one new write surface is the `ai-reject`
endpoint, which discards an unapproved AI draft under an append-only audit
record. Approval granularity is untouched — one click, one answer, no bulk
affordance anywhere (P0-757-1).

## D1 — Queue Layout: Focus-Mode Stepper

**Decision:** the drafts queue is a focus-mode stepper (one draft in focus,
prev/next), not a single-pane table.

**Rationale:** the workload is "work through 200 drafts", and reviewing a
draft is a read-the-narrative + check-the-citations act that needs vertical
space per item. A table collapses each draft to a truncated row and invites
scanning over reading — the wrong pressure on a surface whose entire purpose
is considered per-answer judgment. The slice doc biased this way; nothing
during implementation argued against it. The non-draft outcomes ARE flat
lists (tabs), because those rows are routing decisions (answer manually / map
question), not review acts.

**Rejected:** single-pane table with expandable rows — more visible state,
but the expand-collapse churn at 200 rows costs more than the stepper's
position indicator solves.

## D2 — Keyboard Flow: Navigation Only

**Decision:** `j`/`k` and arrow keys step between drafts. Approve and reject
are deliberately NOT keyboard-bound.

**Rationale:** the constitutional boundary makes approval a considered
per-answer act. A keyboard approve chord turns 200 approvals into 200
reflexive keystrokes — mechanically per-answer but ergonomically a bulk
approve. Navigation speed is where the efficiency win lives; the approval
click stays deliberate. This is the same reasoning that bans the bulk
control, applied to input ergonomics.

## D3 — Reject Semantics: Delete + Append-Only Audit

**Decision:** reject DELETES the draft row inside one transaction that first
writes a `questionnaire_answer_reject_audit` row carrying actor, action, and
snapshot-at-rejection model provenance (prompt/model/version/provider).

**Rationale:** the questionnaire data model treats "unanswered" as
row-absence, so a rejected draft must leave no `questionnaire_answers` row
(otherwise re-suggest and manual authoring both need a tombstone special
case). The audit table restores the forensic record the delete would erase.
`answer_id` on the audit row deliberately carries NO foreign key: the row it
references is deleted in the same transaction, and any FK — even
`ON DELETE SET NULL` — would erase the id the audit exists to preserve.

**Rejected:** a `rejected` status column on `questionnaire_answers` — keeps
history in-table but poisons every "does this question have an answer?"
predicate across suggest, batch-run enumeration, export, and rendering.

Reject refuses approved (409), manual (409), and absent/cross-tenant (404,
RLS-invisible) targets. Deleting approved content is a different, deliberate
operation that does not belong to this surface (P0-757-4).

## D4 — Draft State Rides the Detail Projection

**Decision:** the review queue derives its work list from the existing
questionnaire detail GET, which now projects the slice-441 AI-assist columns
(`ai_assisted`, `human_approved`, `human_approver`, prompt/model provenance)
on every answer. No new "list pending drafts" endpoint.

**Rationale:** the queue's membership rule is one predicate
(`ai_assisted AND NOT human_approved`) over data the detail call already
loads. A second endpoint would duplicate the RLS path and drift. This also
makes AC-7 resumability structural: reload → refetch detail → identical
queue. The run record (URL-bound `?view=review&run=<id>`) adds only the
outcome lists and progress counts on top.

## D5 — Cloud-Routing Banner: Config-Driven AND Per-Draft

**Decision:** the queue renders the slice-499 config-driven banner (tenant is
currently cloud-routed) and ADDITIONALLY a per-draft banner derived from the
draft's recorded `model_provider` (FE mirror of the Go provider
classification).

**Rationale:** a queue can contain drafts generated before a tenant switched
back to local — the config banner would be absent while cloud-generated
drafts are on screen. The recorded provider is the truth for "did tenant data
leave the deployment for THIS draft" (AC-4, P0-757-3). Unknown providers
classify as cloud, so a future provider fails toward showing the banner.

## D6 — E2E Tier: Hermetic Route-Mocks (Slice-441 Precedent)

**Decision:** the AC-9 Playwright spec
(`web/e2e/questionnaire-review-queue.spec.ts`) mocks every BFF call with
deterministic stub-LLM-shaped payloads, following the questionnaire AI
convention set by the slice-441 spec.

**Rationale:** the docker-compose bring-up ships no questionnaire seed
fixture, and the atlas binary routes inference to a real Ollama — there is no
server-side stub-LLM env mode for the e2e stack. The established tier split
is: Playwright proves the operator flow and constitutional surface shape
(citations rendered, exactly one approve control, single-answer wire bodies,
fixed suppressed copy); the Go integration tier proves the reject/approve
contract against the real store, RLS, and `llm.StubClient`
(`handlers_ai_reject_integration_test.go`). No seed-harness spillover is
needed because nothing in the spec requires server-side preconditions.

## D7 — "Disabled While Active" Reading

**Decision:** the "Draft all answers" button disables while its own request
is in flight; a run started elsewhere surfaces as the platform's 409 in the
error alert.

**Rationale:** slice-756 execution is request-scoped and synchronous, so "a
run is active" is coextensive with "a start request is outstanding". The
platform's one-active-run guard stays the enforcement; the button state is
ergonomics, not the gate.

## Detection-Tier Classification

- `detection_tier_actual`: `unit` — the slice-436 route-table golden test
  caught the unregistered-golden drift for the new ai-reject route on the
  first full `go test ./...` run.
- `detection_tier_target`: `unit` — exactly where it should be caught; no
  gap.

## Revisit List

- Batch-run execution beyond the request-scoped model (756 D1/D2) will need
  live progress polling in this queue; the run fetch is already URL-bound so
  a poll interval is a local change.
- An evidence detail route does not exist yet; evidence citations link to the
  list surface. Revisit when an evidence detail page ships.
- Keyboard flow could grow `e` (focus edit) once operators ask; approval
  stays unbound per D2.
- The skipped_needs_mapping affordance routes to the question editor (where
  the slice-755 mapping panel lives); a dedicated deep-link into the mapping
  proposal UI would tighten the loop.
