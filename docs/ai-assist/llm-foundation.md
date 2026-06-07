# `internal/llm` — shared local-inference foundation (slice 498)

`internal/llm` is the shared substrate every AI-assist surface
(440 questionnaire-answer suggestion, 441 SCF-mapping suggestion,
444 gap explanation, 471 board-narrative draft) consumes. It owns the
inference transport, the audit record, and the runtime enforcement of the
AI-assist boundary — so each surface is a thin caller, not a re-implementation.

This document is the operator + contributor reference. The trade-off rationale
lives in the decisions log (`docs/audit-log/498-llm-foundation-decisions.md`);
the runtime boundary it enforces is constitutional (`CLAUDE.md` "AI-assist
boundary (hard)", canvas §4.6.5).

## What this substrate is — and is not

| Ships (slice 498)                                          | Does NOT ship (downstream)                            |
| ---------------------------------------------------------- | ----------------------------------------------------- |
| `Client` interface + local **Ollama** implementation       | Cloud-LLM routing + per-tenant banner (**slice 499**) |
| `StubClient` (the CI seam)                                 | pgvector / semantic retrieval (**slice 500**)         |
| `ai_generations` append-only audit record                  | Any user-facing surface (**440 / 441 / 444 / 471**)   |
| Reusable `ai_assisted ↔ human_approver` DB CHECK template | Surface-specific prompts, citation/numeric checks     |
| The `AuditWriter` + `Service` smoke path                   | Approval UI / approval columns on `ai_generations`    |

The substrate is **local Ollama only** (P0-498-1). No tenant data leaves the
deployment.

## The `Client` interface

```go
type Client interface {
    Generate(ctx context.Context, req GenerateRequest) (GenerateResult, error)
}
```

One method, provider-agnostic. Slice 499 adds a cloud implementation behind the
same interface (callers do not change); slice 500 assembles richer context
**in front of** the client (the client takes pre-assembled context, it never
retrieves).

- **`GenerateRequest`** carries: `Surface`, optional `ModelID` (empty → the
  configured default), `PromptVersion`, `SystemPrompt`, pre-assembled
  `Context`, and the **mandatory** caps `MaxTokens` (≤ `MaxTokenBudget`, 4096)
  and `Timeout` (> 0). A request that omits or over-shoots a cap is rejected
  **before** any inference (P0-498-6) — never an unbounded job.
- **`GenerateResult`** carries the opaque `Text` draft plus the **resolved**
  `ModelName` / `ModelVersion` / `ModelProvider` (recorded on the audit row, so
  history reconstructs even if config later changes).

Model output is **opaque text in / opaque text out** (P0-498-7): the substrate
never executes it, eval's it, or interpolates it into SQL / shell / a path. The
`ai_generations` row binds it as a parameterized value via sqlc.

## Configuration (local Ollama)

| Env var                     | Default                   | Meaning                                                    |
| --------------------------- | ------------------------- | ---------------------------------------------------------- |
| `ATLAS_LLM_OLLAMA_ENDPOINT` | `http://127.0.0.1:11434`  | Local Ollama base URL (never egressed past)                |
| `ATLAS_LLM_DEFAULT_MODEL`   | `llama3.1:8b-instruct-q5` | Model when a request leaves `ModelID` empty                |
| `ATLAS_LLM_REQUEST_TIMEOUT` | `120s`                    | Transport backstop (per-request `Timeout` is the real cap) |

`ConfigFromEnv()` never errors — an empty environment yields the all-defaults
local-Ollama posture, which is the intended self-host shape.

### Local-model quality caveat (slice-182 D5)

The default is **Llama 3.1 8B Instruct (q5)** — it runs on commodity 8–12 GB
GPU hardware, which is the point: a self-hoster gets working AI-assist with no
cloud dependency and no data egress. **Quality is correspondingly modest.** The
8B local model produces serviceable drafts for low-stakes surfaces
(summaries, gap explanations) but is weaker on the high-stakes board-narrative
surface, where hallucination cost is asymmetric. Operators who want higher
quality opt into a cloud LLM **per tenant** (slice 499) with a visible routing
banner. The default-model recommendation is reviewed every 6–12 months as local
models improve (a documented maintenance task, not a slice).

## The CI-stubbing pattern (the unblock for 440 / 441 / 444 / 471)

CI has **no live Ollama** (same constraint as the oscal-bridge: no Python /
trestle in CI). Every downstream consumer wires `StubClient` instead of
`OllamaClient` in its integration + e2e tests:

```go
svc := llm.NewService(llm.NewStubClient(), llm.NewAuditWriter(pool))
res, row, err := svc.GenerateAndRecord(ctx, req, subject)
```

`StubClient` still runs the **shared request validation** (mandatory caps,
surface, prompt), so a consumer's over-cap or malformed-request test gets the
same rejection it would in production. Only the inference is stubbed — the
`ai_generations` write + the enforcement run for real against Postgres. Set
`StubClient.Result` to assert against a known draft, or `StubClient.Err` to
exercise backend-failure handling.

## The `ai_generations` audit record

One tenant-scoped, **append-only** row per generation, across every surface
(`migrations/sql/20260607000000_ai_generations.sql`). Columns: `surface`,
`prompt_version`, `model_name` / `model_version` / `model_provider`, the full
`system_prompt`, the assembled `context_inputs` (JSONB), the `raw_draft`, the
`surface_subject` linkage, and `created_at`.

- **Append-only by construction** (P0-498-5): SELECT + INSERT RLS policies
  only, under `FORCE ROW LEVEL SECURITY`. The deliberate absence of
  UPDATE/DELETE policies makes captured fields immutable — there is no
  UPDATE/DELETE query, and `atlas_app` has no UPDATE/DELETE grant. Same pattern
  as `evidence_audit_log` (013), `decisions_audit` (055), `artifact_access_log`
  (036).
- **Tenant-isolated** (P0-498-8): four-policy-shaped RLS on `app.current_tenant`
  (append-only → two policies). A tenant-B reader sees zero of tenant A's rows
  (proven by `TestCrossTenantIsolation`).
- **Draft ledger only.** `ai_generations` carries **no** approval columns — it
  never holds an audit-binding artifact. Approval state lives on the
  surface-specific consumer record, which adopts the reusable CHECK below.

## The reusable `ai_assisted ↔ human_approver` enforcement

The constitution requires **schema-level** enforcement that an AI-assisted
record cannot be marked `human_approved=true` without a `human_approver`
(P0-498-4). This slice ships that as a **reusable** SQL function so every
approvable AI-assist consumer adopts the identical invariant in one line:

```sql
CONSTRAINT <table>_ai_assist_invariant
    CHECK (ai_assist_human_approver_guard(ai_assisted, human_approved, human_approver))
```

The Go mirror (`llm.EnforceApproval`) gives a friendly early rejection before
the DB round-trip; the **DB CHECK is the authoritative gate** (proven at the DB
layer by `TestReusableCheckTemplate_RejectsAtDBLayer`). Consumers use both —
never one without the other.

`mcp_write_proposals` (slice 173) is the pre-existing adopter; it keeps its own
inlined `mcp_wp_ai_assist_invariant` CHECK (no behavior change — the predicate
is byte-identical in meaning). New adopters use the shared function so the
predicate is authored exactly once going forward.

## Forward note

- **Slice 499** adds a cloud `Client` implementation + the per-tenant opt-in +
  the visible routing banner. It extends, it does not change, this interface.
- **Slice 500** adds pgvector grounding — it assembles `GenerateRequest.Context`
  upstream; the client still takes pre-assembled context.
