# 502 — AI evidence-summarization v0 (cited, non-binding, control-detail surface)

**Cluster:** AI-assist
**Estimate:** M (1-2d)
**Type:** JUDGMENT (summary shape + citation strictness + corpus-bounding)
**Status:** `blocked` (needs #498 — the `internal/llm` substrate)

> Filed 2026-06-06 via the AI-assist/reporting gap analysis. Roadmap §10.2 names
> phase-2 **"AI-assisted gap explanation _and_ evidence summarization"** as a
> paired deliverable. Slice 444 ships the **gap-explanation** half and
> explicitly defers the sibling: "It does **not** ship evidence-summarization
> (the sibling §10.2 deliverable — separate slice)." **No slice owns evidence
> summarization.** This is the sibling.

## Narrative

**Why (the gap today).** A control with automated + manual evidence accumulates
many records over time — scan results, access-review completions, config
snapshots, uploaded artifacts. To answer "what does my evidence for SCF:IAC-06
actually show right now?" the operator today scrolls a list and reads records
one by one. The platform holds the records; it does not summarize them. Roadmap
§10.2 pairs this with gap-explanation precisely because both are
**comprehension aids** over data the platform already holds deterministically —
low-risk, non-binding, operator-facing.

**What (the deliverable shape).** For **one control's evidence set**, retrieve
the control's current evidence records (under RLS), feed bounded excerpts to the
slice-498 inference substrate, and render a plain-language summary — "what this
evidence collectively shows" — with **cited evidence IDs**, in the existing
control-detail view, alongside (not replacing) the deterministic evidence list.
Like gap-explanation (slice 444), the summary is **non-binding** — it is a
reading aid, never an audit artifact, never published — but the
**bindingness-independent** AI-assist invariants still apply fully: no fabricated
coverage (the summary may only assert what the cited evidence supports), no
cross-tenant bleed, local-only inference by default.

**Scope discipline.** **One control, one summary,** in the control-detail
surface. Non-binding ⇒ no approve/reject workflow (the operator reads it; they do
not publish it). It does **not** persist the summary as an audit artifact (it is
regenerated on demand). It does **not** ship a cross-control "portfolio summary"
(a follow-on). It does **not** ship cloud routing (inherits whatever backend the
tenant is on per slice 499). It does **not** summarize across audit-period
boundaries that would violate audit-period freezing — the summary reflects
current live evidence only, clearly labeled. **Follow-on slices:** multi-control
/ portfolio evidence summary; period-scoped summary that respects audit-period
freezing for an in-progress audit.

## Threat model

STRIDE pass (design-time). Verdict: **has-mitigations** — lower-risk than
440/441 (non-binding, operator's own view), but the bindingness-independent
invariants (no fabricated coverage, no cross-tenant bleed, local-only default)
dominate, same family as slice 444.

**S — Spoofing.** The summarize path reuses the control-detail read authz; no new
ingress beyond the authenticated control view.
_Mitigation/AC:_ the summarize endpoint sits behind the existing control-detail
authz; no new role; no public surface.

**T — Tampering / hallucination.** The model could summarize the evidence by
asserting coverage the records do not support, or cite an evidence ID that does
not exist — even non-binding, a false summary misleads the operator.
_Mitigation/AC:_ the summary is grounded in the **actual retrieved evidence
records** (the model summarizes only what it was given); cited evidence IDs are
validated to resolve to real tenant-owned rows before display; a summary citing
an unresolvable ID is suppressed (fall back to the deterministic evidence list).
No fabricated coverage even in an informational surface — the
no-fabricated-coverage invariant is bindingness-independent.

**R — Repudiation.** Non-binding ⇒ no audit-trail requirement for the summary
text itself (it is not a record); the underlying evidence is already in the
ledger.
_Mitigation/AC:_ the summary is regenerated on demand, not persisted as an audit
artifact (deliberate — comprehension aid, not evidence). The model metadata
(name/version/provider) is surfaced in the UI for transparency.

**I — Information disclosure / cross-tenant bleed.** The evidence set is
tenant-scoped; the prompt must use only the requesting tenant's records.
_Mitigation/AC:_ the evidence retrieval + bounded excerpts are assembled under
`app.current_tenant` (RLS); every cited ID is asserted tenant-owned; local
inference by default (no egress unless the tenant opted into cloud via slice 499,
with the banner). An integration test proves a tenant-B summary cannot cite or
quote a tenant-A evidence record.

**D — Denial of service.** Summarization over a large evidence set is expensive;
spam or an unbounded corpus could exhaust resources.
_Mitigation/AC:_ the evidence excerpt set is bounded (top-N recent/relevant
records per control, not the full history); generation inherits the slice-498
mandatory timeout + token budget; per-tenant rate limiting applies.

**E — Elevation of privilege.** No new role; the summary reaches only a user
already authorized to see the control. Non-binding ⇒ no publish path to abuse.
_Mitigation/AC:_ reuse control-detail authz; there is no approve/publish surface
to elevate into.

## Acceptance criteria

### Backend

- [ ] **AC-1.** For one control, the current evidence records are retrieved under
      the requesting tenant's RLS context; the excerpt set is bounded (top-N), not
      the full history (D-mitigation).
- [ ] **AC-2.** A prompt is built from the bounded cited evidence excerpts; the
      model is instructed to summarize ONLY the provided records and cite their
      evidence IDs.
- [ ] **AC-3.** The summary is generated against the slice-498 substrate (local
      Ollama default; cloud only under the slice-499 per-tenant opt-in + banner).
- [ ] **AC-4.** **Citation validation:** every cited evidence ID is resolved to a
      real tenant-owned row before display; an unresolvable citation suppresses
      the summary (falls back to the deterministic evidence list).
- [ ] **AC-5.** The summary is **non-binding**: rendered in the control-detail
      view only, with NO approve/publish/export path; reflects current live
      evidence only (no audit-period-frozen mixing), clearly labeled.

### Frontend

- [ ] **AC-6.** The control-detail view renders the summary with its citations + a
      visible "AI-generated summary (model X) — not an audit artifact" disclosure.
- [ ] **AC-7.** When generation is unavailable or the citation check fails, the
      deterministic evidence list still displays (graceful degradation).

### Tests

- [ ] **AC-8.** Integration test: a valid summary with resolvable citations
      renders for a control with evidence.
- [ ] **AC-9.** Integration test: a summary citing a non-existent evidence ID is
      suppressed in favor of the deterministic list (threat-model T).
- [ ] **AC-10.** **Cross-tenant isolation test:** a tenant-B summary cannot cite
      or quote a tenant-A evidence record (threat-model I).
- [ ] **AC-11.** Frontend test: the non-binding disclosure renders; there is no
      approve/export affordance on the summary.

### Docs / JUDGMENT artifact

- [ ] **AC-12.** Decisions log
      (`docs/audit-log/502-evidence-summarization-v0-decisions.md`): the summary
      shape, the citation-strictness call, the corpus-bounding (top-N) choice, and
      the "Revisit once in use" list.
- [ ] **AC-13.** Changelog + control-detail docs note the summary is non-binding +
      cited + live-evidence-only.

## Constitutional invariants honored

- **AI-assist boundary (hard).** Evidence summarization is roadmap §10.2 phase-2;
  never fabricates coverage (bindingness-independent); never seeds tenant B with
  tenant A data; local-only default. No audit-binding artifact (non-binding,
  not persisted).
- **#6 RLS tenant isolation** — retrieval + prompt are per-tenant; cross-tenant
  bleed proven absent (AC-10).
- **#2 ingestion/evaluation separation** — the summary READS evidence records; it
  never writes to the ledger.
- **#9 manual evidence is first-class** — manual + automated evidence are
  summarized uniformly.
- **#10 audit-period freezing** — the summary reflects current live evidence only,
  clearly labeled; it does not mix frozen audit-period populations (period-scoped
  summary is a follow-on).
- **Inference backend** — local Ollama default; cloud only under slice-499 opt-in.

## Canvas references

- `Plans/canvas/10-roadmap.md` §10.2 — "AI-assisted gap explanation _and_
  evidence summarization."
- `CLAUDE.md` "AI-assist boundary (hard)" — "Summarizing prior responses for
  similarity matching" + the no-fabricated-coverage invariant.
- `Plans/canvas/04-evidence-engine.md` §4.6.5 — AI-assist boundary.

## Dependencies

- **#498 (this batch — `ready`/unbuilt)** — the `internal/llm` substrate.
  **Hard dependency — `blocked` until 498 lands.**
- **#444 (`ready`/unbuilt)** — the sibling gap-explanation surface; this slice
  mirrors its non-binding, cited, control-detail pattern. Not a hard dependency,
  but the two should share the non-binding-disclosure UX.
- **#190 (merged)** — control-detail authz.
- Reuses the existing control-detail evidence data path.

## Anti-criteria (P0 — block merge)

- **P0-502-1.** Does NOT fabricate coverage — citations validated even though
  non-binding (AI-assist boundary; threat-model T).
- **P0-502-2.** Does NOT include any tenant-A evidence in a tenant-B summary
  (threat-model I — proven by AC-10).
- **P0-502-3.** Does NOT add any approve/publish/export path — the summary is
  non-binding + read-only (AC-5).
- **P0-502-4.** Does NOT persist the summary as an audit artifact — regenerated on
  demand.
- **P0-502-5.** Does NOT mix audit-period-frozen evidence into a live summary —
  current live evidence only, clearly labeled (#10 audit-period freezing).
- **P0-502-6.** Does NOT route to a cloud LLM by default — local Ollama; cloud
  only under the slice-499 per-tenant opt-in + banner.
- **P0-502-7.** Does NOT block the deterministic evidence list on the LLM —
  graceful degradation (AC-7).
- **P0-502-8.** Does NOT feed the full evidence history to the model — bounded
  top-N excerpts only.

## Skill mix (3-5)

- `grill-with-docs` — align the summary shape with roadmap §10.2 + the
  no-fabricated-coverage invariant.
- `tdd` — citation-suppression + cross-tenant tests are load-bearing.
- `security-review` — the AI-assist boundary + tenant isolation (even
  non-binding).
- `simplify` — keep it a thin sibling of slice 444; share the non-binding-
  disclosure UX.

## Notes for the implementing agent

- **Mirror slice 444's pattern.** Gap-explanation and evidence-summarization are
  the §10.2 paired non-binding surfaces — share the non-binding-disclosure UX, the
  citation-validation-then-suppress fallback, and the graceful-degradation copy.
  The distinction: 444 explains a _gap state_ from the deterministic rollup; this
  summarizes the _evidence content_ from the actual records.
- **No-fabricated-coverage is bindingness-independent.** Even though the summary
  is non-binding, validate citations and suppress on failure — a false summary
  that asserts unsupported coverage misleads the operator just as badly.
- **Respect audit-period freezing.** The summary is over _current live_ evidence,
  labeled as such. A period-scoped summary that draws from a frozen population is
  a follow-on; do not mix the two in v0.
- **Build-ordering.** Needs slice 498's substrate. Confirm it has landed at
  pickup; until then this stays `blocked`.
- **Registration note (slice-382).** This slice's `_STATUS.md` row is NOT
  registered on this `docs/502` branch; the orchestrator registers it via a
  `chore/status` action after the spec PR merges.
