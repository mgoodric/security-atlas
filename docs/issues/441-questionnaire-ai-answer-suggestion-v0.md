# 441 — Questionnaire AI-answer suggestion v0 (cited drafts, one-click approve)

**Cluster:** AI-assist
**Estimate:** L (3d)
**Type:** JUDGMENT (retrieval relevance + draft shape + citation strictness)
**Status:** `ready`

## Narrative

Slice 155 shipped questionnaire CRUD + Excel import but **locked AI-assist OUT**
(deferred to v2). Roadmap §10.2 names the "security-questionnaire response
engine — answer customer questionnaires from existing evidence with one-click
human approval per answer." CLAUDE.md's AI-assist boundary explicitly **permits**
"suggesting draft questionnaire answers with mandatory citations to specific
evidence IDs and/or policy IDs."

A solo security leader fielding a customer's security questionnaire today
re-types the same answers they have already documented as evidence and policy —
the platform has the source material but does not help. This slice is the
**first tracer bullet** of the response engine: for **ONE questionnaire row**,
retrieve candidate evidence/policy IDs, draft a cited answer via local Ollama,
and render it in the existing slice-155 review UI with mandatory-citation
enforcement and per-answer approve/edit/reject.

**Scope discipline.** **ONE questionnaire row, end-to-end.** Retrieval is a
**keyword first-pass** (pgvector semantic retrieval is an explicit follow-on —
do not pull pgvector into v0). The suggestion is a **draft only**: nothing is
published or returned to the customer without one-click human approval per
answer. This slice does **not** ship batch "answer all rows," does **not** ship
cloud-LLM routing (local Ollama only), and does **not** ship similarity matching
against prior questionnaires (a separate boundary-permitted follow-on).
**Follow-on slices:** pgvector semantic retrieval; batch suggest-all;
prior-response similarity matching; cloud-LLM opt-in.

## Threat model (STRIDE) — HEAVY (AI-assist family)

Same threat family as slice 440 (board narrative): the LLM consumes
tenant-confidential evidence + policy data and drafts text that — once approved —
goes to an external customer. Citation integrity, tenant isolation, and
no-fabricated-coverage dominate.

**S — Spoofing.** The suggest + approve endpoints are authenticated +
role-gated (questionnaire-response is a grc_engineer/admin capability).
**Mitigation:** reuse slice 155's questionnaire auth + the OAuth-AS JWT
validation; approval records the human approver.

**T — Tampering / hallucination (PRIMARY).** The model may draft an answer
claiming a control/coverage the tenant does not actually have, or cite an
evidence/policy ID that does not exist — and this answer goes to a customer.
**Mitigation:** **mandatory-citation enforcement** — every factual claim cites a
real evidence/policy ID resolved against the tenant's rows BEFORE the operator
sees the draft; unresolved citation rejects the draft. The model is instructed
to answer ONLY from retrieved cited material; an answer with no resolvable
citation backing is flagged "insufficient evidence — answer manually" rather
than fabricated.

**R — Repudiation.** Which evidence/policy backed an answer the customer
received must be reconstructable.
**Mitigation:** the suggestion record stores the prompt, the retrieved candidate
IDs, the raw draft, operator edits, and the final approved answer with its
citations; `ai_assisted=true ↔ human_approver` invariant applies.

**I — Information disclosure / cross-tenant bleed (PRIMARY).** Retrieval +
prompt assembly must use ONLY the requesting tenant's evidence/policy; a leak
would put Tenant A's confidential evidence into Tenant B's customer-facing
answer — a severe breach.
**Mitigation:** retrieval queries run under `app.current_tenant` (RLS); every
candidate ID is asserted tenant-owned before inclusion in the prompt; Ollama is
local (no egress). An integration test proves a Tenant-B suggestion cannot cite
or quote a Tenant-A record. The CLAUDE.md boundary "never use Tenant A's
confidential data to seed Tenant B's draft" is the explicit invariant.

**D — Denial of service.** Unbounded retrieval or generation spam.
**Mitigation:** retrieval returns a capped candidate set; generation is
rate-limited per tenant + timed out; the prompt carries bounded excerpts, not
the full corpus.

**E — Elevation of privilege.** AI must not publish a customer-facing answer
without one-click human approval; must not auto-approve.
**Mitigation:** suggestions persist as draft state; approval is a separate
human action recording the approver; no path auto-approves.

## Acceptance criteria

**Backend — retrieve + draft (one row)**

- [ ] **AC-1.** For one questionnaire row, a keyword first-pass retrieves a
      capped set of candidate evidence/policy IDs owned by the requesting tenant
      (under RLS).
- [ ] **AC-2.** A prompt is built from the question text + bounded excerpts of
      the candidate evidence/policy; the model is instructed to answer ONLY from
      cited material.
- [ ] **AC-3.** The answer is drafted against local Ollama (default model per
      CLAUDE.md).
- [ ] **AC-4.** **Mandatory citation enforcement:** every claim's cited
      evidence/policy ID is resolved to a real tenant-owned row BEFORE the
      operator sees the draft; an unresolved citation rejects the draft.
- [ ] **AC-5.** When no candidate evidence/policy resolves the question, the
      suggestion returns "insufficient evidence — answer manually" rather than a
      fabricated answer (no-fabricated-coverage).

**Backend — approval + audit**

- [ ] **AC-6.** The suggestion persists in a **draft** state per row; approve /
      edit / reject is a separate human action (one-click approval per answer).
- [ ] **AC-7.** The suggestion record carries the model metadata + enforces
      `ai_assisted=true ⇒ human_approver` for an approved answer.
- [ ] **AC-8.** The audit record stores prompt + candidate IDs + raw draft +
      operator edits + final approved answer.
- [ ] **AC-9.** Suggest + approve endpoints require the questionnaire-response
      role.

**Frontend**

- [ ] **AC-10.** The suggested answer renders in the existing slice-155 review
      UI with its citations shown; the operator can edit inline.
- [ ] **AC-11.** The operator **cannot approve** an answer with an unresolved
      citation.
- [ ] **AC-12.** Approve / edit / reject are per-answer; approved text is what
      the questionnaire stores.

**Tests**

- [ ] **AC-13.** Integration test: a valid suggestion with resolvable citations
      reaches draft state.
- [ ] **AC-14.** Integration test: a draft citing a non-existent evidence ID is
      rejected (threat-model T).
- [ ] **AC-15.** Integration test: a question with no backing evidence returns
      "insufficient evidence," not a fabricated answer (AC-5).
- [ ] **AC-16.** **Cross-tenant isolation test:** a Tenant-B suggestion cannot
      cite or quote a Tenant-A evidence/policy record (threat-model I).
- [ ] **AC-17.** Integration test: an approved answer requires `human_approver`.
- [ ] **AC-18.** Frontend test: approve disabled while a citation is unresolved.

**Docs / JUDGMENT artifact**

- [ ] **AC-19.** A decisions log
      (`docs/audit-log/441-questionnaire-ai-v0-decisions.md`) records the
      retrieval strategy, the citation-strictness call, the "insufficient
      evidence" threshold, and the "Revisit once in use" list.
- [ ] **AC-20.** A changelog entry; questionnaire-module docs note the AI-assist
      surface is draft-only + cited.

## Constitutional invariants honored

- **AI-assist boundary (hard).** Suggesting draft answers with mandatory
  citations to real evidence/policy IDs; human approves per answer; never
  fabricates coverage; never seeds Tenant B with Tenant A data; never
  auto-approves.
- **#6 — Tenant isolation via RLS.** Retrieval + prompt are per-tenant;
  cross-tenant bleed proven absent.
- **#9 — Manual evidence is first-class.** The "insufficient evidence — answer
  manually" path keeps the manual surface equal.
- **Inference backend.** Local Ollama default; no data leaves the deployment.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.5–4.6 — questionnaires + AI-assist
  boundary.
- `Plans/canvas/10-roadmap.md` §10.2 — "security-questionnaire response
  engine … one-click human approval per answer."
- `CLAUDE.md` "AI-assist boundary (hard)" — the permitted-suggestions list.

## Dependencies

- **#155** (questionnaire CRUD + Excel import + review UI) — `merged`. This
  slice extends its review surface; slice 155 locked AI OUT, which this slice
  now opens under the boundary.
- **#190** (OAuth-AS JWT validation) — `merged`. The role gate.
- pgvector — explicitly NOT a dependency in v0 (keyword first-pass).

## Anti-criteria (P0 — block merge)

- **P0-441-1.** Does NOT return/publish any customer-facing answer without
  one-click human approval per answer (AI-assist boundary).
- **P0-441-2.** Does NOT fabricate coverage — no answer claims a control/evidence
  with no resolvable citation backing (AC-5).
- **P0-441-3.** Does NOT include any Tenant-A evidence/policy in a Tenant-B
  suggestion (threat-model I — proven by AC-16).
- **P0-441-4.** Does NOT show the operator a draft with an unresolved citation.
- **P0-441-5.** Does NOT pull in pgvector — keyword first-pass only (follow-on).
- **P0-441-6.** Does NOT route to a cloud LLM — local Ollama only in v0.
- **P0-441-7.** Does NOT implement batch "answer all rows" — one row only.
- **P0-441-8.** Does NOT let `human_approved=true` exist without
  `human_approver`.

## Skill mix (3-5)

`grill-with-docs` · `tdd` (integration-first; citation-rejection + cross-tenant
tests load-bearing) · `security-review` (AI-assist boundary + tenant isolation) ·
`database-designer` (suggestion-record + approval-state invariant) · `simplify`.

## Notes for the implementing agent

- **Phase-2 grill output:** the keyword first-pass is deliberate — do NOT reach
  for pgvector to "do it properly." The retrieval quality follow-on is its own
  slice; v0 proves the cited-draft + approval machinery, which is the
  boundary-critical part.
- **JUDGMENT calls you own:** the keyword retrieval scoring, the citation
  strictness, and the "insufficient evidence" threshold. Record in the
  decisions log.
- The "insufficient evidence — answer manually" path (AC-5) is the
  no-fabricated-coverage guardrail in action — it is a feature, not a fallback;
  test it explicitly.
- Detection-tier: a fabrication or cross-tenant bug caught in integration is the
  desired `target=integration, actual=integration`.
