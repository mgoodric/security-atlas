# 755 — Questionnaire SCF-mapping suggest-and-approve (AI mapping surface v0)

**Cluster:** AI-assist / Questionnaires
**Estimate:** M (1.5d)
**Type:** JUDGMENT (candidate retrieval scoring + proposal UX copy)
**Status:** `ready`

## Narrative

Filed from the OE-595 end-to-end questionnaire-answering design (2026-07-28
repo review). This is slice 1 of 4; see also #756 (batch answer run), #757
(review queue UI), #758 (approval-gated export).

Slice 155 imports a customer questionnaire and marks every row whose
`scf_anchor_id` could not be pre-resolved as `needs_mapping`. Today those rows
just sit: the operator hand-edits nothing, and — worse for the end-to-end flow —
the slice-441 answer-suggestion surface and the slice-155 AnswerLibrary both key
on the SCF anchor, so an unmapped question is locked out of every downstream
assist. The canvas has always named the fix: "user maps each to SCF
(AI-suggested) once; future receipts auto-map" (§4.6.2), and the AI-assist
boundary explicitly permits "**Suggesting** SCF mappings for unmapped
questionnaire questions (human approves once; mapping is canonical
thereafter)".

This slice builds that surface: for one `needs_mapping` question, retrieve a
capped candidate set of SCF anchors from the seeded catalog (keyword
first-pass, same discipline as slice 441 — NO pgvector), ask local Ollama to
pick the best anchor with a one-line rationale, validate the pick against the
grounding set AND the real catalog before the operator sees it, persist it as
an unapproved proposal, and let the operator approve (one click, recorded
approver) or reject. Approval writes `questionnaire_questions.scf_anchor_id`
and clears `needs_mapping` — canonical thereafter, exactly like a hand-entered
mapping.

The proposal record follows the `mcp_write_proposals` pattern (slice 173) and
adopts the shared `ai_assist_human_approver_guard` CHECK (slice 498) rather
than re-authoring the predicate — the CLAUDE.md-designated path for new
AI-assist surfaces.

## Threat model (STRIDE) — HEAVY (AI-assist family)

**S — Spoofing.** Suggest/approve/reject endpoints reuse the slice-155
questionnaire auth + OAuth-AS JWT role gate. No new principal classes.

**T — Tampering / hallucination (PRIMARY).** The model may invent an SCF anchor
ID (e.g. `IAC-99`) or pick one outside the candidate set.
**Mitigation:** the suggested anchor is accepted ONLY if it is (a) in the
candidate grounding set the prompt presented AND (b) resolvable to a real row
in the seeded SCF catalog — validated BEFORE persistence; a failed validation
suppresses the proposal (nothing persisted, fixed reason code, slice-367 leak
discipline). This mirrors the qaisuggest citation gate applied to anchor IDs.

**R — Repudiation.** Which model/prompt proposed a mapping later found wrong.
**Mitigation:** the generation rides `internal/llm` (ai_generations audit row:
prompt version, model name/version/provider, candidate anchor IDs); the
proposal row carries the same provenance columns; approval records the
approver.

**I — Information disclosure.** The SCF catalog is platform-canonical (not
tenant-confidential), but the QUESTION TEXT is tenant data and the proposal
rows are tenant-scoped. **Mitigation:** proposal table under RLS
(`app.current_tenant`); prompts contain only the one tenant's question text +
catalog excerpts; no cross-tenant retrieval surface exists in this slice.

**D — Denial of service.** Suggestion spam. **Mitigation:** capped candidate
set; single bounded generation per request (reuse qaisuggest's timeout/token
constants or siblings).

**E — Elevation of privilege.** AI must not make its own mapping canonical.
**Mitigation:** the proposal persists unapproved; ONLY the approve endpoint —
a separate human action with a recorded approver — writes
`questionnaire_questions.scf_anchor_id`. The DB CHECK makes
`human_approved=true` without `human_approver` impossible. No code path
auto-approves (constitutional: "Auto-approve its own mappings" is on the
does-NOT list).

## Acceptance criteria

**Backend — suggest**

- [ ] **AC-1.** For one `needs_mapping` question, a keyword first-pass over the
      seeded SCF catalog retrieves a capped candidate set of anchors (id, title,
      bounded description excerpt).
- [ ] **AC-2.** Local Ollama picks one candidate + a one-line rationale; the
      pick is validated to be in the candidate set AND to resolve to a real
      catalog anchor BEFORE anything persists or renders. A failed validation
      suppresses the proposal with a fixed reason code.
- [ ] **AC-3.** A valid pick persists as an UNAPPROVED proposal row
      (`ai_assisted=true`, `human_approved=false`, `human_approver=NULL`)
      carrying prompt-version + model provenance, guarded by the shared
      `ai_assist_human_approver_guard` CHECK.
- [ ] **AC-4.** When the catalog retrieval yields no candidates, the surface
      returns "no mapping suggestion — map manually" and persists nothing
      (no-fabricated-coverage path).

**Backend — approve / reject**

- [ ] **AC-5.** Approve is a separate endpoint: records the approver, flips the
      proposal to approved, writes `questionnaire_questions.scf_anchor_id`, and
      clears `needs_mapping`. Blank approver is rejected (Go mirror of the DB
      CHECK).
- [ ] **AC-6.** The approved mapping is canonical thereafter: the question
      behaves identically to a hand-mapped one (AnswerLibrary suggestions +
      slice-441 answer drafting both work against it).
- [ ] **AC-7.** Reject discards the proposal (audit-logged); the question stays
      `needs_mapping` and can be re-suggested.
- [ ] **AC-8.** Approve/reject/suggest are audit-logged with model + prompt
      provenance.

**Frontend**

- [ ] **AC-9.** In the questionnaire detail, a `needs_mapping` row offers
      "Suggest mapping"; the proposal renders anchor + rationale + provenance
      with one-click Approve / Reject.
- [ ] **AC-10.** Cloud-routing banner renders when the generation was routed to
      a cloud provider (structurally false today; the flag is honored, not
      dropped).

**Tests**

- [ ] **AC-11.** Integration: a fabricated / out-of-grounding anchor ID from the
      model suppresses the proposal (nothing persisted).
- [ ] **AC-12.** Integration: approval without an approver fails at BOTH the Go
      guard and the DB CHECK.
- [ ] **AC-13.** Integration: approval flips `needs_mapping` and the mapping
      survives as canonical (a subsequent library-suggestion query for that
      anchor finds the question's answers eligible).
- [ ] **AC-14.** Integration: Tenant B cannot read or approve Tenant A's
      proposal (RLS).

**Docs / JUDGMENT artifact**

- [ ] **AC-15.** Decisions log at
      `docs/audit-log/755-questionnaire-scf-mapping-decisions.md` (retrieval
      scoring, single-pick vs ranked-list call, re-suggest semantics,
      detection-tier fields, revisit list). Changelog entry.

## Constitutional invariants honored

- **AI-assist boundary (hard).** "Suggesting SCF mappings for unmapped
  questionnaire questions (human approves once; mapping is canonical
  thereafter)" — this slice is that sentence, verbatim. Never auto-approves its
  own mappings.
- **#6 — Tenant isolation via RLS.** Proposal rows tenant-scoped; proven by
  AC-14.
- **#7 — SCF is the canonical control catalog.** Mappings go question → SCF
  anchor; the suggestion can only name a real catalog anchor.
- **Inference backend.** Local Ollama default; cloud opt-in banner honored.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.6.2 ("user maps each to SCF
  (AI-suggested) once; future receipts auto-map"), §4.6.5 (AI boundary).
- `CLAUDE.md` "AI-assist boundary (hard)" — permitted-suggestions list +
  `ai_assist_human_approver_guard` adoption path.

## Dependencies

- **#155** (questionnaire CRUD + import; `needs_mapping` flag) — `merged`.
- **#441** (qaisuggest; the validation-gate + provenance patterns to mirror) —
  `merged`.
- **#498** (shared `ai_assist_human_approver_guard`) — `merged`.
- NOT dependent on #756/#757/#758 — independently shippable.

## Anti-criteria (P0 — block merge)

- **P0-755-1.** Does NOT write `scf_anchor_id` from any code path other than
  human approval with a recorded approver.
- **P0-755-2.** Does NOT persist or render a proposal whose anchor is outside
  the grounding set or absent from the catalog.
- **P0-755-3.** Does NOT pull in pgvector — keyword first-pass only.
- **P0-755-4.** Does NOT route to a cloud LLM by default.
- **P0-755-5.** Does NOT bundle SIG/CAIQ content — the surface operates on
  already-ingested customer-supplied questionnaires only.

## Skill mix (3-5)

`grill-with-docs` · `tdd` (fabricated-anchor + approval-guard tests
load-bearing) · `database-designer` (proposal table + shared guard) ·
`security-review` (AI boundary + RLS) · `simplify`.

## Notes for the implementing agent

- Mirror qaisuggest's shape: retrieve → generate → validate → persist-unapproved
  → separate approve. Reuse `internal/llm` (SurfaceQuestionnaire is acceptable;
  a distinct prompt version like `qmapsuggest-v0` is required either way —
  record the surface-enum call in the decisions log).
- The catalog side of retrieval is platform-canonical data; the RLS-critical
  rows are the question text and the proposal itself.
- Single-pick vs ranked-list-of-3 is a JUDGMENT call; v0 bias is single pick
  (one-click approve stays one click).
