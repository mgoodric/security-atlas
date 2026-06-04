# 428 — ADRs for the four load-bearing canvas invariants without a decision record

**Cluster:** Docs
**Estimate:** M
**Type:** JUDGMENT
**Status:** `ready`

## Narrative

**Why.** The repo carries ten ADRs (`docs/adr/0001`–`0010`), but every one of them records a **narrow tactical** decision — framework-scope workflow, bearer-token storage, audit-period-freeze hash inputs, OAuth AS, control-detail empty state, branch-protection PAT, board-narrative AI-assist, contract-test tier, demoseed convergence, OSCAL cosign signing. None of the four **pillar** architecture invariants — the ones CLAUDE.md calls non-negotiable and that bound every other decision — has its own decision record. They live only as canvas prose + a one-line CLAUDE.md invariant. A diligence reviewer (or a new contributor) who asks "where is the decision record for tenant isolation?" finds an invariant assertion but no ADR capturing the trade-off and the rejected alternative. The four pillars without an ADR:

| Invariant                                                        | Canvas                   | Today's record                     |
| ---------------------------------------------------------------- | ------------------------ | ---------------------------------- |
| #6 — RLS tenancy at the DB layer                                 | §5.4                     | CLAUDE.md invariant + canvas prose |
| #2 — append-only evidence ledger, ingestion/evaluation separated | §4.3                     | CLAUDE.md invariant + canvas prose |
| #1 — UCF graph / one-control-N-satisfactions                     | §3, `UCF_GRAPH_MODEL.md` | CLAUDE.md invariant + canvas prose |
| #4/#5 — multidimensional scope + FrameworkScope intersection     | §5.1-5.5                 | CLAUDE.md invariant + canvas prose |

**What.** One short ADR (~1 page each) per pillar — four new ADRs at the next four ADR slots (0011–0014, allocated in order at write time). Each ADR follows the existing ADR shape (Context · Decision · Consequences / trade-offs · Alternatives considered / why-not). The canvas keeps the **resolved invariant** as the daily reference; the ADR captures the **trade-off context and the rejected alternatives** — the "why this and not that" a reviewer needs. This is documentation discipline catch-up, not a new decision: the decisions were made long ago (these are the project's founding commitments); the ADRs retroactively record the reasoning.

**Scope discipline.** Four ADRs, one slice, four deliverables. The ADRs are **retrospective** — they record decisions already made and shipped; they do NOT re-open or re-litigate any invariant. They do NOT modify the canvas invariant text (the canvas is already the resolved record; the ADR is additive context). If, mid-write, the four ADRs prove to be more than one slice's worth of substantive work, the implementer may split the two thinnest into a follow-on slice and note it — but the default is one slice with all four ADR deliverables, matching the brief.

## Threat model

Docs slice STRIDE pass. An ADR that **misstates** a security-load-bearing invariant (e.g. describes RLS as application-enforced, or describes the evidence ledger as mutable) is an integrity/trust threat: it would mislead a diligence reviewer and could seed a future contributor's incorrect mental model that weakens the actual control.

**S — Spoofing.** N/A (no auth surface). The RLS ADR (#6) DOES describe the tenant-context spoofing threat that RLS defends against — that is content, not a slice threat.

**T — Tampering (load-bearing for the ledger ADR).** The append-only-ledger ADR (#2) MUST accurately state the integrity property: evaluation never writes to source-of-truth evidence, the ledger is append-only, point-in-time replay holds. _Threat:_ an ADR that softens this (e.g. "the ledger may be compacted") would document a weaker guarantee than the code enforces. _Mitigation:_ each ADR's "Decision" section is grilled against the cited canvas section and the relevant code package before merge.

**R — Repudiation.** The ledger ADR (#2) should note the audit-trail property it underpins. No new audit surface is created.

**I — Information disclosure (load-bearing for the RLS ADR).** The RLS ADR (#6) MUST state the **deny-on-missing-context** behavior correctly — RLS denies (not silently passes) when tenant context is absent. _Threat:_ an ADR that describes RLS as fail-open would be a dangerously wrong record. _Mitigation:_ grill against `internal/db/` integration tests and canvas §5.4.

**D — Denial of service.** N/A (static docs).

**E — Elevation of privilege.** The RLS ADR's accuracy is the safeguard: misdescribing the role model (`atlas_app` / `atlas_migrate`) could mislead a contributor. The ADR cites the canonical role model rather than restating it loosely.

## Acceptance criteria

- [ ] **AC-1.** Four new ADR files exist at consecutive slots (next four after 0010 at write time), one per pillar: RLS tenancy (#6), append-only evidence ledger (#2), UCF graph / one-control-N-satisfactions (#1), multidimensional scope + FrameworkScope intersection (#4/#5).
- [ ] **AC-2.** Each ADR follows the existing ADR section shape: a status/date header, **Context**, **Decision**, **Consequences** (trade-offs), and **Alternatives considered** (the rejected option + why-not).
- [ ] **AC-3.** The RLS ADR (#6) states deny-on-missing-context correctly and cites the canonical role model (`internal/db/` integration test) rather than restating it loosely.
- [ ] **AC-4.** The evidence-ledger ADR (#2) states the append-only + ingestion/evaluation-separation + point-in-time-replay properties accurately and cites canvas §4.3.
- [ ] **AC-5.** The UCF-graph ADR (#1) states one-control-N-satisfactions / STRM-typed edges through SCF anchors / no per-framework duplication, and cites canvas §3 + `UCF_GRAPH_MODEL.md`.
- [ ] **AC-6.** The scope ADR (#4/#5) states multidimensional-not-a-tree + `effective_scope = applicability_expr ∩ framework_scope.predicate`, and cites canvas §5.1-5.5.
- [ ] **AC-7.** Each ADR's **Alternatives considered** names the concrete rejected alternative (e.g. per-framework duplicated controls for #1; scope-as-tree for #4/#5; application-layer tenant filtering for #6; mutable evidence table for #2) and why it was rejected.
- [ ] **AC-8.** Each ADR marks itself **retrospective** (records a decision already shipped) so a reader does not mistake it for a re-opened question.
- [ ] **AC-9.** No canvas invariant text is modified (the ADRs are additive); the slice touches only `docs/adr/`.
- [ ] **AC-10.** If the project maintains an ADR index/README, the four new ADRs are listed there in order.
- [ ] **AC-11.** Each ADR cross-links to its canvas section and to the CLAUDE.md invariant number it records.
- [ ] **AC-12.** `pre-commit run --files` passes on the four new ADR files (prettier/markdownlint clean).
- [ ] **AC-13.** Each ADR is grilled against the cited code package / canvas section for accuracy and the grill outcome is noted in the decisions log (the load-bearing correctness gate per the threat model).

## Constitutional invariants honored

This slice **documents** invariants #1, #2, #4, #5, #6 — it records their rationale without altering them. It honors the documentation discipline ("new architectural decisions land as ADRs; the canvas captures the resolved invariant, the ADR captures the trade-off context").

## Canvas references

- `Plans/canvas/03-ucf.md` + `Plans/UCF_GRAPH_MODEL.md` — UCF graph (#1).
- `Plans/canvas/04-evidence-engine.md` §4.3 — append-only ledger / ingestion-evaluation separation (#2).
- `Plans/canvas/05-scopes.md` §5.1-5.5 — multidimensional scope + FrameworkScope intersection (#4/#5) + RLS tenancy §5.4 (#6).

## Dependencies

- None (retrospective documentation of already-shipped invariants). The cited code packages (`internal/db/`, `internal/evidence/`, `internal/ucf/`, `internal/scope/`) all exist on `main`.

## Anti-criteria (P0 — block merge)

- **P0-428-1.** Does NOT re-open, re-litigate, or alter any pillar invariant — the ADRs are retrospective records, not new decisions.
- **P0-428-2.** Does NOT modify canvas invariant text or CLAUDE.md invariant text — additive `docs/adr/` content only.
- **P0-428-3.** Does NOT misstate a security-load-bearing property: RLS deny-on-missing-context, evidence-ledger append-only, must be stated accurately (threat-model T/I; AC-13 is the guard).
- **P0-428-4.** Does NOT collapse the four pillars into fewer than four ADRs (one decision record per pillar — that is the diligence-answer shape).
- **P0-428-5.** Does NOT claim an alternative was "considered at the time" if it was not — frame retrospective ADRs honestly ("the rejected alternative, recorded retrospectively").

## Skill mix (3-5)

- `grill-with-docs` — align each ADR against its canvas section + code package (AC-13).
- `Security` — verify the RLS and ledger ADRs state their security properties correctly.
- `simplify` — keep each ADR to ~1 page; resist canvas-section duplication.
- `changelog-generator` — note the four ADRs in the changelog.

## Notes for the implementing agent

- The ADR slot numbering has a known historical collision: two files share `0003-` (`0003-audit-period-freeze-hash-inputs.md` and `0003-oauth-authorization-server.md`). Do NOT replicate that — allocate the next four **distinct** slots after the highest existing number (`0010`), i.e. `0011`–`0014`, and verify no other in-flight PR is claiming them before writing.
- Existing ADRs to pattern-match for shape: `docs/adr/0010-oscal-cosign-signing.md` (full Context/Decision/Consequences/Alternatives) and `docs/adr/0007-contract-test-tier.md` (concise tactical record). Match the fuller shape — these are pillar records.
- These are retrospective. The honest framing is "this ADR records, after the fact, the decision and the alternative the project rejected" — do not fabricate a contemporaneous deliberation that did not happen. Pattern-match the rejected alternatives from the canvas anti-patterns list (per-framework duplicated controls, scope-as-tree, application-layer tenancy) which already names them as explicit rejections.
- Detection-tier: `none` expected (pure docs; no bug surface). If grilling surfaces a canvas/code drift, that is a finding to record (and possibly spillover), not to silently paper over.
- The brief explicitly permits a follow-on split if four ADRs overflow one slice — but default to one slice, four deliverables. If splitting, file the follow-on via `/idea-to-slice` and reference this slice.
