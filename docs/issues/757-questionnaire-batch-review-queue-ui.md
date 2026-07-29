# 757 — Questionnaire batch review + per-answer approval queue UI

**Cluster:** Frontend / AI-assist
**Estimate:** M (1.5d)
**Type:** JUDGMENT (queue UX shape + copy)
**Status:** `ready`

## Narrative

Filed from the OE-595 end-to-end questionnaire-answering design (2026-07-28
repo review). Slice 3 of 4, **parented on #756** (the run + drafts it
reviews); see also #755 (mapping suggest-approve), #758 (approval-gated
export).

Slice 441 wired per-row AI suggest + approve into the questionnaire detail
page; slice 756 makes a whole run of drafts land at once. Reviewing 200 drafts
through the one-row-at-a-time surface is where the workflow would die. This
slice is the operator's half of the batch flow: start a run, watch it
progress, then work a **review queue** — every drafted answer with its
citations and model provenance, approved / edited / rejected one answer at a
time, with the non-draft outcomes (insufficient evidence, suppressed, skipped
unmapped) surfaced as their own work lists instead of disappearing.

Approve wires to the existing slice-441 `ai-approve` endpoint (recorded
approver, DB-guarded). Reject needs a new endpoint — discard the draft,
audit-logged, question returns to unanswered — and per the
contract-in-one-slice rule the endpoint lands HERE with its first caller, not
in #756 where nothing would call it.

The queue is deliberately per-answer. The constitutional boundary requires
one-click human approval **per answer** — so there is NO "approve all"
button. The efficiency win is layout (everything reviewable in one place, keyboard
flow), never approval granularity.

## Threat model — LIGHT (UI over guarded endpoints)

The write paths this UI drives are the slice-441 approve (DB
`ai_assist_human_approver_guard`) and the new reject. The reject endpoint is
the one new attack surface: it must be tenant-scoped (RLS path), role-gated
like approve, restricted to unapproved AI drafts (rejecting an approved or
manual answer is a 409/404 — deletion of approved content is a different,
deliberate operation that does not belong to this surface), and audit-logged.
The UI must render citations and provenance faithfully — hiding a citation or
the cloud-routing banner would erode the transparency half of the boundary —
but rendering is honesty, not enforcement; enforcement stays server-side.

## Acceptance criteria

**Backend (one new endpoint)**

- [ ] **AC-1.** `POST /v1/questionnaires/{id}/answers/{qid}/ai-reject`
      discards an unapproved AI draft (question returns to unanswered),
      audit-logged with provenance; 409/404 for approved, manual, or absent
      answers. Role-gated like ai-approve. RLS-scoped.

**Frontend — run + queue**

- [ ] **AC-2.** Questionnaire detail offers "Draft all answers" (starts a #756
      run; disabled while one is active) and shows run progress from the run
      status endpoint (counts by outcome).
- [ ] **AC-3.** A review queue lists every `drafted` item: question, draft
      narrative, its citations (linked to the evidence/policy records), and
      model provenance (name / version / provider + prompt version).
- [ ] **AC-4.** Cloud-routing banner renders on any draft whose generation was
      cloud-routed (CloudRouted flag honored end-to-end).
- [ ] **AC-5.** Per answer: Approve (one click, wires to ai-approve), Edit
      (inline; edited text is what approval stores), Reject (wires to AC-1).
      There is NO bulk-approve control anywhere on the surface.
- [ ] **AC-6.** Non-draft outcomes are surfaced as filterable lists:
      insufficient-evidence → "answer manually" affordance;
      skipped_needs_mapping → link to the #755 mapping affordance; suppressed →
      fixed reason code only (no model/backend detail — slice-367 leak
      discipline).
- [ ] **AC-7.** Approved answers leave the queue; queue count reflects
      remaining unreviewed drafts; the surface is resumable (reload
      mid-review loses nothing).

**Tests**

- [ ] **AC-8.** Integration: ai-reject discards a draft + writes the audit row;
      rejects approved/manual targets with 409/404; Tenant B cannot reject
      Tenant A's draft.
- [ ] **AC-9.** Playwright e2e (stub LLM): import → draft-all → queue renders
      drafts with citations → approve one (approver recorded), edit-approve
      one, reject one → outcomes reflected in the questionnaire.
- [ ] **AC-10.** Frontend: no control exists that submits more than one
      approval; approve always sends exactly one answer id.

**Docs / JUDGMENT artifact**

- [ ] **AC-11.** Decisions log at
      `docs/audit-log/757-questionnaire-review-queue-decisions.md` (queue
      layout call, keyboard flow, reject semantics, detection-tier fields,
      revisit list) + changelog entry.

## Constitutional invariants honored

- **AI-assist boundary (hard).** One-click human approval **per answer** is the
  spine of this UI; bulk approval is structurally absent (AC-5/AC-10). Nothing
  is published from this surface — export is #758.
- **#6 — Tenant isolation via RLS.** The new reject endpoint follows the
  RLS-bound store path (AC-8).
- **#9 — Manual evidence is first-class.** Insufficient-evidence rows route to
  the manual-authoring surface as peers, not leftovers.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.6.4 (inbound flow: "SME approves /
  edits / rejects per answer"), §4.6.5 (boundary).
- `CLAUDE.md` "AI-assist boundary (hard)" — per-answer approval + banner.

## Dependencies

- **#756** (batch run + status API + outcome vocabulary) — **parent**; this
  slice renders that contract.
- **#441** (ai-approve endpoint + citation rendering patterns) — `merged`.
- Composes with #755's mapping affordance (AC-6 link) but does not depend on
  it — the link renders whenever #755's surface exists.

## Anti-criteria (P0 — block merge)

- **P0-757-1.** NO bulk/select-all approval control, keyboard shortcut, or API
  affordance. Approval stays per answer.
- **P0-757-2.** Does NOT render a draft without its citations, or with citation
  links that do not resolve.
- **P0-757-3.** Does NOT suppress the cloud-routing banner.
- **P0-757-4.** Does NOT allow reject to touch approved or manual answers.
- **P0-757-5.** Does NOT expose model/backend error detail in suppressed-row
  copy (fixed reason codes only).

## Skill mix (3-5)

`grill-with-docs` · `tdd` (reject-endpoint integration tests) ·
`security-review` (new write endpoint) · `simplify` · Playwright e2e per the
testing discipline.

## Notes for the implementing agent

- Reuse the slice-441 approval components/BFF wiring wherever they fit; the
  queue is a new arrangement of existing pieces plus the run-progress view.
- The vitest tier is node-only (no JSX) — component behavior lands in the
  Playwright spec per the repo's test-tier conventions; BFF route logic gets
  vitest.
- Queue UX (single-pane list vs focus-mode stepper) is the JUDGMENT call; bias
  toward the focus-mode stepper with next/prev — it matches "work through 200
  drafts" better than a giant table. Record it.
