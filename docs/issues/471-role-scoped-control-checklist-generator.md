# 471 — Role-scoped control-implementation checklist generator v0 (cited, non-binding)

**Cluster:** AI-assist
**Estimate:** L (3d)
**Type:** JUDGMENT (team-assignment heuristic + task-breakdown shape + citation strictness)

**Status:** `ready`

> Filed 2026-06-05 via /idea-to-slice at maintainer request. A 5th AI-assist v0
> surface alongside slice 440 (board narrative), 441 (questionnaire answers),
> 444 (gap explanation). Governed by the CLAUDE.md "AI-assist boundary (hard)"
> and the slice-182 foundation pre-commitments.

## Narrative

**Why (the gap today).** A security leader knows _which_ controls are in scope
— the UCF graph, `applicability_expr`, and FrameworkScope intersection tell
them that deterministically. What the platform does **not** do is translate
"SCF:IAC-06 applies to your production AWS environment" into "here is what the
**infra team** actually has to _do_ this quarter to satisfy it." That
translation — control text → concrete, role-scoped operational to-do items — is
today a manual, repetitive exercise the operator does in a spreadsheet, even
though the platform already holds every input (the control set, each control's
`owner_role`, its `applicability_expr` dimensions, and linked policy/evidence).

**What (the deliverable shape).** For a selected control set (optionally
narrowed by a framework / FrameworkScope), generate a **role-scoped
implementation checklist**: one section per team role, each enumerating the
concrete to-do items that role must execute to satisfy the controls assigned to
it. The split of _which control → which role_ is **deterministic** (derived from
the existing `owner_role` + `applicability_expr` data, via a normalization map),
not LLM-guessed. The LLM's job is narrow and bounded: turn each in-scope
control's text into 1–N actionable, role-appropriate task statements. Every
generated checklist item carries a **mandatory citation** to the specific
control / SCF anchor (and policy ID where the control links one) it derives
from, validated to resolve to a real ID in the current tenant **before** the
operator sees the draft. The checklist is a **draft**: nothing is exported,
assigned, or marked authoritative without one-click human approval per section.

**Scope discipline (v0 tracer bullet).** **ONE control set → role-scoped
checklist, end-to-end, for a FIXED 3-role taxonomy** (`infra`, `engineering`,
`security`). Team assignment is deterministic-first (owner_role normalization +
applicability dimensions); the LLM is used **only** for per-control task-text
breakdown. Local Ollama only (Llama 3.1 8B per slice-182 D5) — **no cloud-LLM
routing in v0** (that is the per-tenant-banner follow-on). In-app view + markdown
export only — **no assignable-task integration** (Jira/Linear) and **no
configurable role taxonomy** in v0 (both explicit follow-ons). A control with no
evidence backing is shown as a checklist item with a "no evidence yet" marker —
the generator **does not fabricate coverage** (AI-assist boundary). This slice
shares the **local-inference client foundation** with slices 440/441/444:
whichever AI-assist v0 slice builds first establishes the `internal/llm` Ollama
client + the `ai_generations` audit record; the rest reuse it (see
Dependencies).

## Threat model

STRIDE pass (design-time). Verdict: **has-mitigations** — the AI-assist boundary
is the dominant control surface and is enforced by construction.

**S — Spoofing.** Adds one new authenticated endpoint
(`POST /v1/controls:generate-checklist` or similar) gated by the existing
operator/admin role — no unauthenticated surface, no `.ics`-style token path.
_Mitigation/AC:_ the endpoint requires the same RBAC role as other control-write
operations; no new role introduced.

**T — Tampering.** The control-set selection is user input that selects which
tenant controls feed the prompt. _Threat:_ a caller requesting controls outside
their tenant. _Mitigation/AC:_ the selection is resolved through the RLS-scoped
control read path (`app.current_tenant` set); an ID that does not resolve in the
tenant is rejected, never silently included. The LLM output is **draft text
only** — never executed, never used to construct a query/path/command.

**R — Repudiation.** Generation is an `ai_assisted` action. _Mitigation/AC:_ it
writes an `ai_generations`-style audit record capturing `model_name`,
`model_version`, `model_provider`, `prompt_version`, the full prompt, the full
draft, the operator's edits, and the final approved text — the slice-182 audit
discipline. `ai_assisted=true` rows cannot be `human_approved=true` without
`human_approver` set (schema invariant from slice 182).

**I — Information disclosure (the load-bearing one).** The prompt is built from
tenant-internal control/policy/evidence text. _Threats + mitigations/ACs:_
(a) **Cross-tenant seeding is forbidden** — generation reads ONLY the current
tenant's controls via RLS; no other tenant's data may enter the prompt (CLAUDE.md
AI-assist boundary). (b) **Local-first** — default inference is local Ollama, so
no tenant data leaves the deployment; cloud routing is out of v0 scope, and when
it lands it carries the visible-banner discipline. (c) The generated checklist
exposes only control identifiers + derived task text + citations — no internal
debug strings, no raw evidence blobs beyond the cited excerpt.

**D — Denial of service.** The LLM call is the expensive surface. _Mitigations/
ACs:_ cap the number of controls per generation request (bounded fan-out), cap
the items-per-control the model may emit, and cap total generation time /
token budget; reject an over-cap control-set with a clear message rather than
launching an unbounded job.

**E — Elevation of privilege.** _Threats + mitigations/ACs:_ the generator
**cannot fabricate control coverage that has no evidence backing** (AI-assist
boundary) — controls without evidence are surfaced as explicit gaps, not as
"done" items. No checklist is exported / marked authoritative without the
one-click human-approval gate, so an AI draft can never become a binding
artifact on its own. Generation does not cross the `atlas_app` /
`atlas_migrate` / `atlas_service_account` role boundary.

## Acceptance criteria

### Backend — team assignment (deterministic)

- [ ] **AC-1.** A normalization map resolves each control's free-text
      `owner_role` (+ `applicability_expr` dimensions where owner_role is absent
      or ambiguous) to exactly one of the fixed v0 roles {`infra`,
      `engineering`, `security`}; the mapping is deterministic and unit-tested,
      with an explicit `unassigned` bucket for controls that match none (surfaced
      to the operator, never silently dropped).
- [ ] **AC-2.** The control set fed to generation is resolved through the
      RLS-scoped control read path; an ID not resolvable in the current tenant is
      rejected (T-mitigation).
- [ ] **AC-3.** A control-set-size cap is enforced; an over-cap request returns a
      clear error, not an unbounded job (D-mitigation).

### Backend — generation (LLM, bounded)

- [ ] **AC-4.** Per in-scope control, the local Ollama client produces 1–N
      role-appropriate task statements; items-per-control and total token budget
      are capped (D-mitigation).
- [ ] **AC-5.** Every generated item carries a citation to a control / SCF
      anchor ID (and policy ID when the control links one); a draft with any
      unresolved citation is **auto-rejected before the operator sees it**
      (mirrors slice-182 D4a + slice-441 citation enforcement).
- [ ] **AC-6.** A control with no evidence backing yields an item flagged "no
      evidence yet" — the generator never emits it as satisfied (E-mitigation;
      AI-assist boundary "no fabricated coverage").
- [ ] **AC-7.** The generation writes the slice-182 audit record (model name +
      version + provider + prompt version + full prompt + full draft); the
      `ai_assisted=true ↔ human_approver` schema invariant holds.
- [ ] **AC-8.** Cross-tenant isolation: an integration test proves a generation
      run under tenant A reads zero tenant-B control/evidence rows (I-mitigation;
      RLS-enforced).

### Frontend — review + approval

- [ ] **AC-9.** The checklist renders grouped by role, each item showing its
      citation as a resolvable link to the control/policy.
- [ ] **AC-10.** Per-section (per-role) approve / edit / reject; the operator
      cannot approve a section containing an unresolved-citation item (editor-mode
      gate, slice-182 D2/D4d).
- [ ] **AC-11.** Markdown export is available **only** for approved sections; an
      unapproved/draft checklist cannot be exported (human-approval gate).
- [ ] **AC-12.** A visible "AI-assisted draft — review before use" affordance is
      present until approval (label honesty, slice-225 lineage).

### Tests + docs

- [ ] **AC-13.** Unit tests: the owner_role→role normalization map (incl.
      ambiguous + unassigned), the citation validator (resolves real IDs;
      rejects fabricated), the control-set-size cap.
- [ ] **AC-14.** Integration test: end-to-end generate → audit-record-written →
      cross-tenant-isolation (AC-8) against real Postgres.
- [ ] **AC-15.** Playwright e2e: generate (against a seeded control set + a
      stubbed/local model in CI) → review → approve one section → export markdown;
      cannot-export-before-approve asserted.
- [ ] **AC-16.** Operator docs page: what the generator does, the local-model
      quality caveat (slice-182 D5), the citation/approval discipline, and the
      "no fabricated coverage" guarantee.
- [ ] **AC-17.** Decisions log records: the team-assignment heuristic, the
      task-breakdown prompt shape, the citation-strictness call, and the CI
      model-stubbing approach.

## Constitutional invariants honored

- **AI-assist boundary (hard).** Mandatory citations to real evidence/control/
  policy IDs; no audit-binding artifact published without one-click human
  approval; `ai_assisted=true` requires `human_approver`; no Tenant A data seeds
  Tenant B's generation; audit log captures model name+version + prompt + draft↔
  final diff; local Ollama default. (CLAUDE.md AI-assist boundary; canvas §4.6.5)
- **#6 RLS tenant isolation** — generation reads only the current tenant's
  controls/evidence via row-level security (AC-2, AC-8).
- **#4/#5 multidimensional scope + FrameworkScope** — team assignment derives
  from `applicability_expr` dimensions; optional framework narrowing respects the
  FrameworkScope intersection (canvas §5).
- **#9 manual evidence is first-class** — a control's evidence backing (manual or
  automated) is treated uniformly when flagging "no evidence yet" (AC-6).

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.6 — AI-assist surfaces + the hard
  boundary + the `ai_assisted`/`human_approver` schema enforcement (§4.6.5).
- `Plans/canvas/02-primitives.md` — Control primitive (`owner_role`,
  `applicability_expr`, policy/evidence links).
- `Plans/canvas/05-scopes.md` §5.1–5.5 — applicability + FrameworkScope
  intersection (the deterministic team-assignment inputs).
- `Plans/canvas/10-roadmap.md` §10.2 — phase-2 AI-assist deliverables.

## Dependencies

- **#182 (merged)** — AI-assist foundation: the `ai_assisted`/`human_approver`
  schema invariant, the board-narrative schema extensions
  (`prompt_version`/`model_name`/`model_version`/`model_provider`), and the tone
  discipline this generator's prompt inherits.
- **Shared local-inference foundation with #440 / #441 / #444 (all `ready`,
  unbuilt).** No `internal/llm` Ollama client exists on `main` yet. Whichever of
  these AI-assist v0 slices builds first establishes the local-inference client +
  the `ai_generations` audit record; the others (including this one) reuse it. If
  471 is built first, it owns that foundation; if not, it consumes it. This is a
  build-ordering coupling, not a hard blocker — note it at pickup.
- **Control `owner_role` (#019/#020) + `applicability_expr` (#002)** — both
  merged; the deterministic team-assignment inputs exist (owner_role is
  free-text, hence the normalization map in AC-1).

## Anti-criteria (P0 — block merge)

- **P0-471-1.** Does NOT publish, export, or mark authoritative any checklist
  without one-click human approval per section. (AI-assist boundary)
- **P0-471-2.** Does NOT emit any checklist item without a citation that resolves
  to a real control/SCF-anchor/policy ID in the current tenant — unresolved
  citation auto-rejects the draft before the operator sees it.
- **P0-471-3.** Does NOT let any tenant's data enter another tenant's generation
  prompt (cross-tenant seeding forbidden); generation is RLS-scoped.
- **P0-471-4.** Does NOT fabricate control coverage — a control with no evidence
  is flagged as a gap, never rendered as satisfied.
- **P0-471-5.** Does NOT route to a cloud LLM in v0 (local Ollama only); does NOT
  ship a configurable role taxonomy or assignable-task (Jira/Linear) integration.
- **P0-471-6.** Does NOT write an `ai_assisted=true` record with
  `human_approved=true` and no `human_approver` (schema invariant from slice 182).
- **P0-471-7.** Does NOT launch an unbounded generation job — control-set size,
  items-per-control, and token budget are all capped.
- **P0-471-8.** Does NOT use vendor-prefixed test fixture tokens; neutral
  `test-*` only.

## Skill mix (3-5)

- `grill-with-docs` — align team/role + control terminology with the domain model
- `database-designer` — the `ai_generations` / checklist-draft record (+ the
  slice-182 schema invariant) under four-policy RLS
- `tdd` — citation validator + normalization map + cross-tenant isolation tests
- `security-review` — the AI-assist boundary surface (the dominant risk)
- `ship-gate` — verify the human-approval gate is non-bypassable before export

## Notes for the implementing agent

- **Build-ordering with 440/441/444.** Before writing an Ollama client, check
  whether one already landed (a sibling AI-assist v0 slice may have built it).
  Reuse `internal/llm` (or whatever the established package is) + the
  `ai_generations` audit record rather than forking a second client. If you are
  first, design the client + audit record so the siblings can reuse it (small,
  provider-agnostic interface; local Ollama impl; the slice-182 columns).
- **Deterministic-first is load-bearing.** The "which control → which team" split
  must NOT be an LLM guess — it is derived from `owner_role` (free-text →
  normalize) + `applicability_expr`. The LLM only writes the task text for an
  already-assigned control. This keeps the assignment auditable and the LLM
  surface minimal. Surface the `unassigned` bucket honestly.
- **CI model stubbing.** The e2e/integration tests must not depend on a live
  Ollama in CI — stub the local-inference client behind the interface (return a
  fixed cited draft) so AC-15 runs deterministically; the real Ollama path is
  exercised manually / documented. Record the stubbing approach in the decisions
  log (AC-17).
- **owner_role is free-text** (confirmed via slice 448: `controls.owner_role` is
  a read-only TEXT role string). The normalization map (AC-1) is where the
  mapping judgment lives; seed it from the roles present in the demo dataset +
  common GRC role names, and make the `unassigned` fallback explicit.
- **Registration note (slice-382).** This slice's `_STATUS.md` row is NOT
  registered on this `docs/471` branch — the slice-382 CI guard rejects
  `_STATUS.md` edits from non-`chore/status-batch` branches. The orchestrator
  registers the row (`ready`) via a `chore/status` action once this spec PR is
  approved/merged.
