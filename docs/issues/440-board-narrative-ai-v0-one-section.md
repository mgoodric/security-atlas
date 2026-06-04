# 440 — Board-narrative AI v0 — one numbered section, end-to-end

**Cluster:** Board / AI-assist
**Estimate:** L (3d)
**Type:** JUDGMENT (prompt shape, citation-resolution rules, tone enforcement)
**Status:** `ready`

## Narrative

Slice 182 landed the board-narrative AI **foundation only** — the tone
anti-pattern reference list, ADR-0002, and the documented schema extensions —
**no implementation**. OQ #14 (resolved 2026-05-20) and CLAUDE.md's
board-narrative section lock **all seven guardrail decisions**. Today the v1
board narrative is still a pure `text/template` renderer
(`internal/board/narrative.go`) with **no LLM call site** anywhere.

This slice is the **first tracer bullet** of board-narrative AI v0: implement
**exactly ONE numbered section** end-to-end against the default local Ollama
backend (Llama 3.1 8B), wiring in all seven guardrails for that one section.
Board narratives are the highest-risk AI-assist surface in the product — board
members are non-technical and take outputs at face value, so the hallucination
cost is asymmetric. Every guardrail is non-negotiable.

The seven guardrails, instantiated for one section:

1. **Hybrid input** — the LLM sees a deterministic pre-computation rollup PLUS
   cited evidence excerpts; never raw evidence records, never pure rollup.
2. **Per-section approval** — this one section is approve/edit/reject on its own.
3. **Full prompt+response audit** — system prompt + evidence inputs + draft +
   operator edits + final approved text, stored every time.
4. **Mandatory citations** — every factual claim cites a real evidence/control/
   risk ID, validated to resolve **before** the operator sees the draft;
   unresolved citation rejects the draft.
5. **Numeric-claim verification** — every number is auto-checked against the
   pre-computation; the draft auto-rejects on mismatch.
6. **Section-shape enforcement** — the LLM is constrained to the numbered
   template; freestyle output is rejected.
7. **Editor-mode UX** — inline edit; cannot approve with unresolved citations.

Plus the **banned-phrase tone enforcement** (slice 182's list wired into the
system prompt for this section) and the **schema columns** the boundary
requires: `prompt_version`, `model_name`, `model_version`, `model_provider`,
plus the `ai_assisted=true ↔ human_approver` invariant.

**Scope discipline.** **ONE section only** — pick the most rollup-grounded
section (proposed: the control-coverage-summary section, whose claims are all
numeric and citable). The remaining narrative sections are **explicit
follow-ons**. This slice does **not** ship cloud-LLM routing (local Ollama
only; cloud opt-in is a follow-on), does **not** ship the regenerate-with-
instruction / write-from-scratch affordances (inline-edit default only — 6A),
and does **not** publish any audit-binding artifact without one-click human
approval. **Follow-on slices:** remaining sections; cloud-LLM opt-in routing +
visible banner; regenerate-with-instruction affordance.

## Threat model (STRIDE) — HEAVY

This is the highest-risk AI-assist surface. The LLM consumes tenant-confidential
evidence + control + risk data and produces text a non-technical board consumes
at face value. Three threats dominate: **hallucination / citation forgery**,
**cross-tenant data bleed into the prompt**, and **tone/persuasion drift**.

**S — Spoofing.** The generate endpoint is authenticated + role-gated (board
narratives are an admin/grc_engineer capability). Risk: a lower-privilege user
triggering generation or approving a section.
**Mitigation:** the generate + approve endpoints require the board-report role;
approval records `human_approver` (the boundary's schema invariant); a forged
cookie/bearer hits the existing OAuth-AS JWT validation (slice 190).

**T — Tampering / hallucination (PRIMARY).** The LLM may fabricate a claim, a
number, or a citation that points at nothing.
**Mitigation:** guardrails 4 + 5 + 6 are the mitigation, enforced server-side
**before** the operator sees the draft: (a) every citation ID is resolved
against real evidence/control/risk rows — unresolved ⇒ draft rejected; (b) every
number is matched against the deterministic pre-computation — mismatch ⇒ draft
auto-rejected; (c) the output must conform to the numbered section template —
freestyle ⇒ rejected. The operator never sees a draft that failed validation.

**R — Repudiation.** Auditors / board must be able to reconstruct exactly what
the model was told and what it produced.
**Mitigation:** guardrail 3 — the full system prompt + evidence inputs + raw
draft + operator edits + final approved text are persisted per section, plus
`prompt_version` / `model_name` / `model_version` / `model_provider`. Old
records are immutable (snapshot-at-generation, not retroactive).

**I — Information disclosure / cross-tenant bleed (PRIMARY).** The prompt is
built from tenant evidence; the model must NEVER receive Tenant A's data when
generating Tenant B's narrative, and the local model must not retain/leak
across calls.
**Mitigation:** the pre-computation rollup + evidence excerpts are assembled under
`app.current_tenant` (RLS) for the requesting tenant only; the prompt builder
asserts every cited evidence/control/risk ID belongs to the requesting tenant
before inclusion. Ollama is local (no data leaves the deployment); no cross-call
state is reused. An integration test proves a Tenant-B generation cannot
include a Tenant-A evidence excerpt or citation. Cloud routing is OUT of scope
(no external egress in v0).

**D — Denial of service.** LLM generation is expensive; an attacker could spam
generate calls, or a huge evidence corpus could build an enormous prompt.
**Mitigation:** the evidence-excerpt set is bounded (top-N cited excerpts, not the
full corpus — guardrail 1's "not raw evidence records"); generation is
rate-limited per tenant; a generation timeout caps runaway inference.

**E — Elevation of privilege.** The defining boundary: AI must NOT publish an
audit-binding artifact without one-click human approval, and must NOT
auto-approve.
**Mitigation:** `ai_assisted=true` records cannot have `human_approved=true`
without `human_approver` set (schema-level, the constitutional invariant); the
generate path produces a **draft** state only; approval is a separate
human-initiated action that records the approver. No code path lets the model
self-approve.

## Acceptance criteria

**Backend — generation pipeline (one section)**

- [ ] **AC-1.** A deterministic pre-computation rollup for the chosen section is
      computed server-side (numbers + cited evidence/control/risk IDs) under the
      requesting tenant's RLS context.
- [ ] **AC-2.** A hybrid prompt is built: the rollup PLUS bounded top-N cited
      evidence excerpts — NOT raw evidence records, NOT pure rollup (guardrail 1).
- [ ] **AC-3.** The section is generated against local Ollama (Llama 3.1 8B
      default per CLAUDE.md); the call site lives where slice 182 noted the v2
      continuation owns it.
- [ ] **AC-4.** **Citation validation:** every claim's cited ID is resolved to a
      real evidence/control/risk row owned by the requesting tenant BEFORE the
      operator sees the draft; an unresolved citation rejects the draft
      (guardrail 4).
- [ ] **AC-5.** **Numeric verification:** every number in the draft is checked
      against the pre-computation; a mismatch auto-rejects the draft (guardrail 5).
- [ ] **AC-6.** **Section-shape enforcement:** output not conforming to the
      numbered section template is rejected (guardrail 6).
- [ ] **AC-7.** **Banned-phrase tone enforcement:** slice 182's banned-phrase
      list is wired into the system prompt for this section; the measured-tone
      discipline is applied.

**Backend — approval + audit + schema**

- [ ] **AC-8.** The generated section persists in a **draft** state; a separate
      human action approves/edits/rejects it (per-section — guardrail 2).
- [ ] **AC-9.** The board-narrative record carries `prompt_version`,
      `model_name`, `model_version`, `model_provider` (NOT NULL), and enforces
      `ai_assisted=true ⇒ human_approver` required for `human_approved=true`.
- [ ] **AC-10.** The full audit row is stored: system prompt + evidence inputs +
      raw draft + operator edits + final approved text (guardrail 3).
- [ ] **AC-11.** Generation + approval endpoints require the board-report role.

**Frontend — editor-mode UX**

- [ ] **AC-12.** The section renders in an editor-mode UI: operator edits inline,
      sees citations, and **cannot approve** while any citation is unresolved
      (guardrail 7).
- [ ] **AC-13.** Approve / edit / reject are per-section actions; the approved
      text is what ships into the board pack.

**Tests**

- [ ] **AC-14.** Integration test: a valid generation passes all guardrails and
      reaches the draft state.
- [ ] **AC-15.** Integration test: a draft with a fabricated number is
      auto-rejected (guardrail 5).
- [ ] **AC-16.** Integration test: a draft with an unresolvable citation is
      rejected (guardrail 4).
- [ ] **AC-17.** Integration test: a draft that breaks section shape is rejected
      (guardrail 6).
- [ ] **AC-18.** **Cross-tenant isolation test:** a Tenant-B generation cannot
      include any Tenant-A evidence excerpt or citation (threat-model I).
- [ ] **AC-19.** Integration test: a record cannot be `human_approved=true`
      without `human_approver` set (schema invariant).
- [ ] **AC-20.** Frontend test (vitest/Playwright): the approve button is
      disabled while a citation is unresolved.
- [ ] **AC-21.** Unit test: the banned-phrase check rejects a draft containing a
      banned phrase.

**Docs / JUDGMENT artifact**

- [ ] **AC-22.** A decisions log
      (`docs/audit-log/440-board-narrative-ai-v0-decisions.md`) records the
      chosen section, the prompt shape, citation-resolution rules, and the
      "Revisit once in use" list.
- [ ] **AC-23.** A changelog entry; the chosen-section + guardrail wiring noted
      in the board module docs.

## Constitutional invariants honored

- **AI-assist boundary (hard).** No audit-binding artifact published without
  one-click human approval; `ai_assisted=true ↔ human_approver`; audit log shows
  model name + version + diff; never auto-approves; never seeds Tenant B with
  Tenant A data.
- **Board-narrative section (load-bearing).** All seven guardrails wired for the
  one section; banned-phrase tone enforcement; the four schema columns.
- **#6 — Tenant isolation via RLS.** Prompt assembly is per-tenant; cross-tenant
  bleed proven absent.
- **Inference backend.** Local Ollama default; no data leaves the deployment.

## Canvas references

- `Plans/canvas/07-metrics.md` — board reporting first-class.
- `CLAUDE.md` "Board-narrative AI-assist (load-bearing — OQ #14 resolved
  2026-05-20)" — the seven decisions + banned-phrase list + schema columns.
- `Plans/canvas/11-open-questions.md` #14 — the resolved board-narrative LLM
  boundary.
- `docs/adr/0002-*` (slice 182) — board-narrative foundation ADR.

## Dependencies

- **#182** (board-narrative AI foundation — tone list, ADR, schema-ext doc) —
  `merged`. This slice implements against that foundation.
- **#190** (OAuth-AS JWT validation middleware) — `merged`. The role gate.
- The pre-computation rollup reuses the existing board pack data path
  (`internal/board`).

## Anti-criteria (P0 — block merge)

- **P0-440-1.** Does NOT publish any audit-binding artifact without one-click
  human approval; does NOT auto-approve (AI-assist boundary).
- **P0-440-2.** Does NOT let `human_approved=true` exist without `human_approver`
  (schema invariant).
- **P0-440-3.** Does NOT include any Tenant-A evidence/citation in a Tenant-B
  generation (threat-model I — proven by AC-18).
- **P0-440-4.** Does NOT show the operator a draft that failed citation /
  numeric / shape validation (guardrails 4/5/6 are pre-operator).
- **P0-440-5.** Does NOT route to a cloud LLM — local Ollama only in v0; cloud
  opt-in is a follow-on.
- **P0-440-6.** Does NOT emit banned-phrase / marketing-tone text (slice 182
  list wired in).
- **P0-440-7.** Does NOT implement more than ONE section — remaining sections
  are follow-ons.
- **P0-440-8.** Does NOT feed raw evidence records to the model (bounded cited
  excerpts only — guardrail 1, threat-model D).

## Skill mix (3-5)

`grill-with-docs` · `tdd` (integration-first; guardrail-rejection + cross-tenant
tests are load-bearing) · `security-review` (AI-assist boundary + tenant
isolation + prompt-injection surface) · `database-designer` (the four schema
columns + approval-state invariant) · `simplify`.

## Notes for the implementing agent

- **Phase-2 grill output:** the seven guardrails are NOT optional and NOT
  negotiable — they are the constitutional boundary. If any AC tempts you to
  ship a guardrail "later," stop: the whole point of v0 is proving the guardrail
  machinery on one section. The follow-ons add _more sections_, not _fewer
  guardrails_.
- **JUDGMENT calls you own:** which section to implement first (pick the most
  numeric/citable — coverage-summary proposed), the exact prompt template, and
  the citation-resolution strictness. Record in the decisions log.
- Prompt-injection note: evidence excerpts are tenant-supplied text that enters
  the prompt — treat them as untrusted; the guardrails (citation + numeric +
  shape validation post-generation) are the defense, not input sanitization
  alone.
- Detection-tier: a guardrail-bypass bug caught in an integration test is
  `target=integration, actual=integration` (the desired outcome).
