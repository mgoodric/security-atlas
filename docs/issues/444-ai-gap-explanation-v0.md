# 444 — AI gap-explanation v0 (plain-language, cited, non-binding)

**Cluster:** AI-assist
**Estimate:** M (1-2d)
**Type:** JUDGMENT (explanation shape + citation strictness)
**Status:** `ready`

## Narrative

Roadmap §10.2 names "AI-assisted _gap explanation_ and _evidence
summarization_" as a phase-2 deliverable, and CLAUDE.md's AI-assist boundary
explicitly **permits** "Explaining gaps ('evidence covers SCF:IAC-06 but
freshness is 95 days')." This is the **lowest-risk** AI-assist surface in the
product: the output is **informational** — it explains why a control is in a
gap state — and is **NOT an audit-binding artifact**. It never goes to an
auditor, a board, or a customer unedited; it is a comprehension aid in the
operator's own control-detail view.

This slice is the first tracer bullet: for **one control's gap state**, feed the
**deterministic gap rollup** (the same freshness/drift/coverage facts the
control-detail page already computes) to local Ollama and render a plain-language
explanation, with cited evidence/control IDs, in the control-detail view.
Because the output is non-binding, there is **no approval gate** — but citations
are **still validated** to resolve to real IDs (no fabricated coverage,
ever — that invariant holds regardless of bindingness).

**Scope discipline.** **One control, one explanation,** in the existing
control-detail surface. Non-binding ⇒ no approve/reject workflow (the operator
reads it; they don't publish it). It does **not** ship evidence-summarization
(the sibling §10.2 deliverable — separate slice), does **not** ship cloud-LLM
routing (local Ollama only), and does **not** cache/persist the explanation as
an audit artifact (it is regenerated on demand; not a record). **Follow-on
slices:** evidence-summarization; multi-control gap-overview explanation;
cloud-LLM opt-in.

## Threat model (STRIDE) — AI-assist family (lower-risk, non-binding)

Lower risk than slices 440/441 because the output is non-binding and stays
inside the operator's own view — but the AI-assist invariants that are
**bindingness-independent** still apply: no fabricated coverage, no cross-tenant
bleed, local-only inference.

**S — Spoofing.** The explain endpoint is authenticated + role-gated (reuses the
control-detail read auth).
**Mitigation:** the explain endpoint sits behind the existing control-detail
authz; no new ingress beyond the authenticated control view.

**T — Tampering / hallucination.** The model could explain a gap by citing a
control/evidence ID that does not exist, or assert a coverage fact that is false
— even though it is non-binding, a false explanation misleads the operator.
**Mitigation:** the explanation is grounded in the **deterministic gap rollup**
(numbers come from the rollup, not the model); cited evidence/control IDs are
validated to resolve to real tenant-owned rows before display; an explanation
referencing an unresolvable ID is suppressed (fall back to the deterministic
rollup display). No fabricated coverage even in an informational surface.

**R — Repudiation.** Non-binding ⇒ no audit-trail requirement for the
explanation text itself (it is not a record). The underlying gap facts are
already in the deterministic read-models.
**Mitigation:** the explanation is regenerated on demand and not persisted as an
audit artifact (deliberate — it is a comprehension aid, not evidence). The
model metadata (name/version) is still surfaced in the UI for transparency.

**I — Information disclosure / cross-tenant bleed.** The gap rollup is
tenant-scoped; the prompt must use only the requesting tenant's data.
**Mitigation:** the gap rollup + cited excerpts are assembled under
`app.current_tenant` (RLS); every cited ID is asserted tenant-owned; Ollama is
local (no egress). An integration test proves a Tenant-B explanation cannot cite
a Tenant-A record.

**D — Denial of service.** Explanation generation is expensive; spam or a huge
rollup could exhaust resources.
**Mitigation:** the rollup is bounded (one control's gap facts); generation is
rate-limited per tenant + timed out.

**E — Elevation of privilege.** No new role; the explanation reaches only a user
already authorized to see the control. Non-binding ⇒ no publish path to abuse.
**Mitigation:** reuse control-detail authz; there is no approve/publish surface to
elevate into.

## Acceptance criteria

**Backend**

- [ ] **AC-1.** For one control in a gap state, the deterministic gap rollup
      (freshness / drift / coverage facts) is computed server-side under the
      requesting tenant's RLS context.
- [ ] **AC-2.** A prompt is built from the rollup + bounded cited excerpts; the
      model is instructed to explain ONLY the rollup facts.
- [ ] **AC-3.** The explanation is generated against local Ollama (default model
      per CLAUDE.md).
- [ ] **AC-4.** **Citation validation:** every cited evidence/control ID is
      resolved to a real tenant-owned row before display; an unresolvable
      citation suppresses the explanation (falls back to the deterministic
      rollup display).
- [ ] **AC-5.** The explanation is **non-binding**: it is rendered in the
      control-detail view only, with NO approve/publish/export path.

**Frontend**

- [ ] **AC-6.** The control-detail view renders the explanation with its
      citations + a visible "AI-generated explanation (model X) — not an
      audit artifact" disclosure.
- [ ] **AC-7.** When generation is unavailable or the citation check fails, the
      deterministic gap rollup still displays (graceful degradation).

**Tests**

- [ ] **AC-8.** Integration test: a valid gap explanation with resolvable
      citations renders for a control in a gap state.
- [ ] **AC-9.** Integration test: an explanation citing a non-existent ID is
      suppressed in favor of the deterministic rollup (threat-model T).
- [ ] **AC-10.** **Cross-tenant isolation test:** a Tenant-B explanation cannot
      cite a Tenant-A record (threat-model I).
- [ ] **AC-11.** Frontend test: the non-binding disclosure renders; there is no
      approve/export affordance on the explanation.

**Docs / JUDGMENT artifact**

- [ ] **AC-12.** A decisions log
      (`docs/audit-log/444-gap-explanation-v0-decisions.md`) records the
      explanation shape, the citation-strictness call, and the "Revisit once in
      use" list.
- [ ] **AC-13.** A changelog entry; control-detail docs note the explanation is
      non-binding + cited.

## Constitutional invariants honored

- **AI-assist boundary (hard).** Explaining gaps is explicitly permitted; never
  fabricates coverage (bindingness-independent); never seeds Tenant B with
  Tenant A data; local-only inference.
- **#6 — Tenant isolation via RLS.** Rollup + prompt are per-tenant; cross-tenant
  bleed proven absent.
- **#2 — Ingestion/evaluation separation.** The explanation READS read-models;
  it never writes to the evidence ledger.
- **Inference backend.** Local Ollama default; no data leaves the deployment.

## Canvas references

- `Plans/canvas/10-roadmap.md` §10.2 — "AI-assisted _gap explanation_."
- `CLAUDE.md` "AI-assist boundary (hard)" — "Explaining gaps" permitted.
- `Plans/canvas/04-evidence-engine.md` §4.6 — AI-assist boundary.

## Dependencies

- **#016** (freshness + drift read-models) — `merged`. The deterministic gap
  facts.
- **#190** (OAuth-AS JWT validation) — `merged`. The control-detail authz.
- Reuses the existing control-detail data path.

## Anti-criteria (P0 — block merge)

- **P0-444-1.** Does NOT fabricate coverage — citations validated even though
  non-binding (AI-assist boundary; threat-model T).
- **P0-444-2.** Does NOT include any Tenant-A data in a Tenant-B explanation
  (threat-model I — proven by AC-10).
- **P0-444-3.** Does NOT add any approve/publish/export path — the explanation
  is non-binding and read-only (AC-5).
- **P0-444-4.** Does NOT persist the explanation as an audit artifact — it is
  regenerated on demand.
- **P0-444-5.** Does NOT route to a cloud LLM — local Ollama only in v0.
- **P0-444-6.** Does NOT ship evidence-summarization — sibling follow-on.
- **P0-444-7.** Does NOT block the deterministic rollup display on the LLM —
  graceful degradation (AC-7).

## Skill mix (3-5)

`grill-with-docs` · `tdd` (integration-first; citation-suppression + cross-tenant
tests) · `security-review` (AI-assist boundary + tenant isolation) · `simplify` ·
`changelog-generator`.

## Notes for the implementing agent

- **Phase-2 grill output:** the key distinction vs slices 440/441 is
  bindingness — gap explanation is informational, so there is NO approval gate,
  but the no-fabricated-coverage + no-cross-tenant invariants are
  bindingness-independent and fully apply. Do not skip citation validation just
  because the output is non-binding.
- **JUDGMENT calls you own:** the explanation shape/length, the citation
  strictness, and the graceful-degradation copy. Record in the decisions log.
- The non-binding disclosure (AC-6) is a trust affordance — make it
  unmistakable that this is a comprehension aid, not evidence.
- Detection-tier: `none` unless a bug surfaces.
