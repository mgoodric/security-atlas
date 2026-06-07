# 498 — Shared local-inference (`internal/llm`) client foundation + `ai_generations` audit record + runtime schema enforcement

**Cluster:** AI-assist
**Estimate:** L (3d)
**Type:** JUDGMENT (client interface shape + audit-record schema + runtime-enforcement placement)
**Status:** `ready`

> Filed 2026-06-06 via the AI-assist/reporting gap analysis. This is the
> **foundation the four ready AI-assist v0 slices (440, 441, 444, 471) all
> depend on but none owns.** Slice 182 shipped the board-narrative AI
> _documentation_ foundation (tone list, ADR, schema-extension _contract_) — it
> ships **no code**. Each of 440/441/444/471 carries a "shared local-inference
> foundation … whichever builds first establishes the `internal/llm` Ollama
> client + the `ai_generations` audit record; the rest reuse it" note. That
> "whoever's first owns it" coupling is a latent design hole: the foundation has
> no spec, no owner, and no tests of its own, so the first v0 slice would have
> to design the shared client _and_ the audit record _and_ the runtime schema
> enforcement inline — three load-bearing, security-critical decisions buried
> inside a feature slice. This slice extracts them into a dedicated foundation so
> the v0 surfaces become thin consumers.

## Narrative

**Why (the gap today).** `internal/llm` does not exist on `main`. No Ollama
client exists. No `ai_generations` audit table exists. The `ai_assisted` /
`human_approver` invariant is documented in canvas §4.6.5 and CLAUDE.md as a
**schema-level** guarantee, and the `QuestionnaireAnswer` columns (`ai_assisted`,
`ai_model`, `human_approved`, `human_approver`) exist on that one entity — but
there is no **shared, reusable** runtime mechanism that every AI-assist surface
plugs into: no common client, no common audit record, no common
`ai_assisted=true ⇒ human_approver` enforcement helper. Without this, the first
v0 slice (440/441/444/471) silently becomes the de-facto owner of the entire
AI-assist substrate, and the other three either fork a second client or take a
hard dependency on a feature branch — both bad outcomes for a security boundary.

**What (the deliverable shape).** A small, provider-agnostic local-inference
substrate that the v0 surfaces consume:

1. **`internal/llm` client package** — a narrow `Client` interface
   (`Generate(ctx, GenerateRequest) → (GenerateResult, error)`) with a
   **local Ollama implementation** as the default (Llama 3.1 8B per slice-182
   D5). Provider-agnostic by design so the cloud-LLM-opt-in slice (499) adds a
   second implementation behind the same interface — **no cloud routing in this
   slice** (local only). The request carries the system prompt, the assembled
   context, a model identifier, and generation caps (token budget + timeout);
   the result carries the raw text + the resolved model metadata
   (`model_name`, `model_version`, `model_provider`).
2. **`ai_generations` audit record** — a single tenant-scoped, append-only table
   capturing one row per generation across **all** AI-assist surfaces:
   `surface` (questionnaire / board_narrative / gap_explanation / checklist /
   summary), `prompt_version`, `model_name`, `model_version`, `model_provider`,
   the full system prompt, the assembled context inputs, the raw draft, and the
   surface-specific linkage (e.g., the answer/section/control it pertains to).
   This is the slice-182 audit discipline made concrete and **shared** — old
   rows are immutable (snapshot-at-generation).
3. **Runtime `ai_assisted ↔ human_approver` enforcement** — a reusable
   DB-layer constraint pattern + a Go guard helper so every approvable
   AI-assist record (not just `QuestionnaireAnswer`) gets the invariant for
   free: an `ai_assisted=true` row cannot become `human_approved=true` without
   `human_approver` set. The canonical example today is `QuestionnaireAnswer`;
   this slice generalizes the check into a reusable helper + a CHECK-constraint
   template so 440/441/471 wire it identically.
4. **A CI-stubbable seam** — the `Client` interface is the seam that lets every
   downstream slice's integration + e2e tests run without a live Ollama in CI
   (return a fixed cited draft). This slice ships the stub implementation +
   documents the stubbing pattern so 440/441/444/471 all stub identically.

**Scope discipline (foundation only).** This slice ships the **client +
audit record + enforcement helper + stub** and **one thin internal smoke
consumer** to prove the substrate end-to-end — it does **not** ship any of the
four user-facing v0 surfaces (those stay 440/441/444/471). **Local Ollama only**
— cloud routing + the visible banner are slice 499. **No pgvector / semantic
retrieval** — that grounding layer is slice 500; this slice's client takes
already-assembled context as input, it does not retrieve. **No prompt templates
for any specific surface** — each surface owns its own prompt; this slice owns
the transport + audit + enforcement. **Follow-on slices:** 499 (cloud-LLM
opt-in + banner), 500 (pgvector grounding), and the consumers 440/441/444/471.

## Threat model

STRIDE pass (design-time). Verdict: **has-mitigations** — this slice IS the
AI-assist boundary's runtime substrate, so the boundary controls are the
dominant surface and are enforced by construction.

**S — Spoofing.** The substrate exposes no new ingress of its own (it is an
internal package + a table). The smoke consumer, if it exposes any endpoint, is
internal-only / test-only and authenticated; no public surface.
_Mitigation/AC:_ no new unauthenticated endpoint ships; the substrate is library
code consumed behind the existing role gates of the eventual surfaces.

**T — Tampering / hallucination.** The client returns model output that
downstream surfaces treat as untrusted draft text. This slice does **not** itself
validate citations (that is surface-specific), but it MUST NOT execute, eval, or
use model output to build a query/path/command — it is opaque text in, opaque
text out.
_Mitigation/AC:_ the `GenerateResult.Text` is never interpolated into SQL, a
shell command, or a file path by the substrate; the `ai_generations` row is
written via parameterized sqlc, model text bound as a value only.

**R — Repudiation.** Every generation must be reconstructable: which model, which
prompt, which inputs, what came back.
_Mitigation/AC:_ the `ai_generations` append-only record captures
`model_name` / `model_version` / `model_provider` / `prompt_version` + full
prompt + full context inputs + raw draft. Rows are immutable
(snapshot-at-generation); the table has no UPDATE path for the captured fields.

**I — Information disclosure / cross-tenant bleed (load-bearing).** The audit
record and any context the client receives are tenant-confidential. A leak would
put one tenant's prompt/draft into another tenant's row, or let the local model
retain cross-call state.
_Mitigation/AC:_ `ai_generations` is RLS-scoped (four-policy, `app.current_tenant`)
like every tenant table; the client holds **no cross-call state** (each
`Generate` is stateless); local Ollama means no tenant data leaves the
deployment (cloud egress is explicitly out of scope here — slice 499 adds the
banner discipline). An integration test proves a tenant-B reader cannot see a
tenant-A `ai_generations` row.

**D — Denial of service.** LLM generation is the expensive surface; an unbounded
prompt or runaway inference exhausts resources.
_Mitigation/AC:_ the `GenerateRequest` carries a **mandatory** token budget +
timeout; the client enforces both (context deadline + max-tokens) and rejects a
request exceeding the configured cap rather than launching an unbounded job. The
caps are the shared primitive every surface inherits.

**E — Elevation of privilege.** The defining boundary: AI must not publish an
audit-binding artifact without one-click human approval and must not
auto-approve.
_Mitigation/AC:_ the shared `ai_assisted ↔ human_approver` enforcement helper +
CHECK-constraint template make `human_approved=true` impossible without
`human_approver` set, **at the DB layer**, for every approvable AI-assist
record that adopts it. The substrate writes only **draft / non-approved**
generations; it has no self-approve code path. Generation does not cross the
`atlas_app` / `atlas_migrate` / `atlas_service_account` role boundary.

## Acceptance criteria

### Client substrate

- [ ] **AC-1.** `internal/llm` defines a provider-agnostic `Client` interface
      (`Generate(ctx, GenerateRequest) → (GenerateResult, error)`); the request
      carries system prompt + assembled context + model id + a **mandatory**
      token budget + timeout; the result carries text + resolved
      `model_name` / `model_version` / `model_provider`.
- [ ] **AC-2.** A local **Ollama** implementation is the default (Llama 3.1 8B
      per slice-182 D5); it reads the model + endpoint from config and never
      egresses outside the configured local endpoint.
- [ ] **AC-3.** The client enforces the token budget + timeout (context deadline)
      and rejects an over-cap request with a clear error rather than launching an
      unbounded job (D-mitigation).
- [ ] **AC-4.** A **stub** implementation (returns a fixed result) ships behind
      the same interface; the stubbing pattern is documented so 440/441/444/471
      reuse it for CI (no live Ollama in CI).

### `ai_generations` audit record

- [ ] **AC-5.** An idempotent + reversible migration creates `ai_generations`:
      tenant-scoped, append-only, with `surface`, `prompt_version`,
      `model_name`, `model_version`, `model_provider`, full system prompt, full
      context inputs (JSONB), raw draft, surface linkage, and `created_at`.
- [ ] **AC-6.** `ai_generations` has four-policy RLS on `app.current_tenant`;
      captured fields have no UPDATE path (immutability via policy + no update
      query).
- [ ] **AC-7.** A reusable writer (sqlc) persists one row per generation; model
      output is bound as a parameterized value (never interpolated).

### Runtime `ai_assisted ↔ human_approver` enforcement

- [ ] **AC-8.** A reusable CHECK-constraint template + a Go guard helper enforce
      `ai_assisted=true ⇒ (human_approved=true ⇒ human_approver IS NOT NULL)`;
      `QuestionnaireAnswer`'s existing columns are the canonical adopter and are
      brought under the shared helper (no behavior change, just consolidation).
- [ ] **AC-9.** The enforcement is proven at the **DB layer** — an integration
      test shows a direct `human_approved=true` write with NULL `human_approver`
      is rejected by the constraint, not merely by application code.

### Smoke consumer (prove the substrate)

- [ ] **AC-10.** One thin internal smoke path exercises client → `ai_generations`
      write → enforcement helper end-to-end against real Postgres + the stub
      client; it is NOT a user-facing surface.

### Tests + docs

- [ ] **AC-11.** Unit tests: token-budget/timeout cap rejection; the stub client;
      the enforcement guard helper (positive + negative).
- [ ] **AC-12.** Integration test: cross-tenant isolation on `ai_generations`
      (tenant B cannot read tenant A's rows); the DB-layer enforcement (AC-9);
      the append-only/no-update guarantee.
- [ ] **AC-13.** Operator/dev docs: the `internal/llm` interface contract, the
      CI-stubbing pattern, the local-model quality caveat (slice-182 D5), and the
      `ai_generations` audit-record shape. A note that cloud routing (499) and
      pgvector grounding (500) extend this substrate.
- [ ] **AC-14.** Decisions log (`docs/audit-log/498-llm-foundation-decisions.md`):
      the `Client` interface shape, the `ai_generations` column set, the
      enforcement placement (DB CHECK vs trigger), and the CI-stubbing approach.

## Constitutional invariants honored

- **AI-assist boundary (hard).** This slice is the **runtime substrate** for the
  boundary: the `ai_assisted ↔ human_approver` enforcement, the model-metadata
  audit record (name + version + provider + prompt), and the local-Ollama-default
  posture all land here as **shared** mechanisms the surfaces inherit. No
  audit-binding artifact is published by the substrate; it writes drafts only.
  (CLAUDE.md AI-assist boundary; canvas §4.6.5)
- **#6 RLS tenant isolation** — `ai_generations` is four-policy RLS-scoped;
  cross-tenant bleed proven absent (AC-12).
- **#2 ingestion/evaluation separation** — the substrate writes only to its own
  audit table; it never writes to the evidence ledger.
- **Inference backend** — local Ollama default; no data leaves the deployment;
  cloud is a slice-499 follow-on with the visible-banner discipline.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.6.5 — AI-assist boundary,
  schema-level `ai_assisted`/`human_approver` enforcement, pluggable inference
  backend (local Ollama default; cloud opt-in with banner).
- `CLAUDE.md` "AI-assist boundary (hard)" — schema-level enforcement; audit log
  shows model name + version + diff; local-Ollama default.
- `Plans/canvas/10-roadmap.md` §10.2 — the phase-2 AI-assist deliverables that
  consume this substrate.

## Dependencies

- **#182 (merged)** — the AI-assist documentation foundation: the
  `ai_assisted`/`human_approver` contract + the board-narrative schema-extension
  columns (`prompt_version`/`model_name`/`model_version`/`model_provider`). This
  slice turns that contract into shared runtime code.
- **#190 (merged)** — OAuth-AS JWT validation; the role gate the eventual
  surfaces sit behind (not exercised directly here beyond the smoke path).
- **Blocks / is depended-on by #440, #441, #444, #471** — those four `ready`
  AI-assist v0 surfaces consume `internal/llm` + `ai_generations` + the
  enforcement helper. This slice resolves their shared-foundation coupling: build
  this first and they become thin consumers.

## Anti-criteria (P0 — block merge)

- **P0-498-1.** Does NOT route to a cloud LLM — local Ollama only; cloud opt-in +
  banner is slice 499.
- **P0-498-2.** Does NOT implement pgvector / semantic retrieval — the client
  takes pre-assembled context; grounding is slice 500.
- **P0-498-3.** Does NOT ship any user-facing AI-assist surface (440/441/444/471
  stay their own slices); only the internal substrate + a smoke consumer.
- **P0-498-4.** Does NOT allow a `human_approved=true` AI-assist record without
  `human_approver` — enforced at the **DB layer**, proven by AC-9.
- **P0-498-5.** Does NOT let `ai_generations` rows be UPDATE-mutated on the
  captured fields — append-only, snapshot-at-generation.
- **P0-498-6.** Does NOT launch an unbounded generation job — token budget +
  timeout are mandatory on every request.
- **P0-498-7.** Does NOT interpolate model output into SQL/shell/path — opaque
  text in/out; bound as a parameterized value only.
- **P0-498-8.** Does NOT make `ai_generations` non-tenant-scoped — four-policy
  RLS, proven by the cross-tenant isolation test.
- **P0-498-9.** Does NOT use vendor-prefixed test fixture tokens; neutral
  `test-*` only.

## Skill mix (3-5)

- `grill-with-docs` — align the substrate's terms with canvas §4.6.5 + the
  slice-182 schema contract.
- `database-designer` — the `ai_generations` append-only table under four-policy
  RLS + the reusable enforcement CHECK template.
- `tdd` — the cap-rejection, enforcement-guard, and cross-tenant isolation tests
  are load-bearing (integration > unit).
- `security-review` — this slice IS the AI-assist boundary substrate; the review
  is the dominant gate.
- `simplify` — the `Client` interface must stay narrow so 499/500 extend it
  cleanly.

## Notes for the implementing agent

- **You are building the thing 440/441/444/471 each assumed someone else would
  build.** Keep the `Client` interface deliberately small and provider-agnostic
  so slice 499 adds a cloud implementation behind it without touching callers,
  and so slice 500 layers retrieval _in front of_ it (the client takes assembled
  context, not raw evidence). Resist the urge to bake any surface-specific prompt
  or citation logic in — that belongs to the consumers.
- **The enforcement is the security crux.** Prove `ai_assisted ↔ human_approver`
  at the DB layer (CHECK constraint or trigger), not just in Go — the constitution
  says "schema-level enforcement." `QuestionnaireAnswer` already has the columns;
  bring it under the shared helper without changing its behavior.
- **CI stubbing is the unblock for all four consumers.** Ship the stub +
  document the pattern so the consumer slices' integration/e2e tests never need a
  live Ollama in CI. Record the approach in the decisions log.
- **Registration note (slice-382).** This slice's `_STATUS.md` row is NOT
  registered on this `docs/498` branch — the slice-382 CI guard rejects
  `_STATUS.md` edits from non-`chore/status-batch` branches. The orchestrator
  registers the row via a `chore/status` action after the spec PR merges.
