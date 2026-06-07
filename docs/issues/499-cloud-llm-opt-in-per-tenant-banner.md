# 499 — Cloud-LLM opt-in per-tenant routing + visible "routes to {provider}" banner

**Cluster:** AI-assist
**Estimate:** M (1-2d)
**Type:** JUDGMENT (per-tenant opt-in storage shape + banner placement + provider-key handling)
**Status:** `blocked` (needs #498 — the `internal/llm` client interface + `ai_generations` audit record)

> Filed 2026-06-06 via the AI-assist/reporting gap analysis. Canvas §4.6.5
> commits to a pluggable inference backend: **"Optional: cloud — Anthropic,
> OpenAI, or Bedrock via API key. Off by default. When enabled, the deployment
> owner explicitly opts in per-tenant; a banner indicates 'AI assist routes to
> {provider}' wherever drafts appear."** CLAUDE.md repeats this:
> "Cloud LLMs … are opt-in per-tenant with a visible banner indicating routing."
> Every AI-assist v0 slice (440/441/444/471) explicitly defers cloud routing as
> a follow-on and ships local Ollama only. **No slice owns the cloud-routing +
> banner discipline.** This is the slice that does.

## Narrative

**Why (the gap today).** The local-Ollama-default posture (slice 498) keeps all
tenant data inside the deployment — the safe default. But canvas §4.6.5 commits
to a cloud opt-in for tenants who want higher-quality drafts (e.g., Llama 3.1 8B
is documented as a quality caveat at v0; a tenant may prefer Anthropic/OpenAI/
Bedrock). The constitution puts a hard discipline on this: it is **off by
default**, **opt-in per tenant** (not per deployment — a multi-tenant deployment
may have one tenant on cloud and the rest local), and **every surface where a
draft appears must show a banner** naming the provider data routes to. That
banner is the honesty affordance: the operator can never be unaware that their
confidential evidence is leaving the deployment for a third party.

**What (the deliverable shape).**

1. **Per-tenant inference-routing config** — a tenant-scoped setting recording
   the active provider (`local-ollama` default, or `anthropic` / `openai` /
   `bedrock`) and the (encrypted-at-rest) provider API key. Default is
   `local-ollama`; switching to a cloud provider is an explicit admin action.
2. **A cloud `Client` implementation behind the slice-498 interface** — one
   adapter per provider, selected at generation time by the requesting tenant's
   routing config. The slice-498 `Client` interface is the seam; this slice adds
   implementations, it does **not** change callers (440/441/444/471 keep calling
   `Generate` and get the tenant's configured backend transparently).
3. **The visible banner** — every UI surface that renders an AI-assist draft
   (board-narrative editor, questionnaire-answer review, gap-explanation,
   checklist) shows "AI assist routes to {provider} — your data leaves this
   deployment" when the tenant is on a cloud provider; no banner when local.
   The banner is driven by the tenant routing config, not hardcoded per surface.
4. **`model_provider` carries the truth into the audit record** — the
   slice-498 `ai_generations.model_provider` records the actual provider used
   for each generation, so the audit trail proves which data went to which
   backend. A cloud-routed generation is forensically distinguishable from a
   local one.

**Scope discipline.** This slice ships **per-tenant routing config + the cloud
adapters behind the 498 interface + the banner + the audit-provenance**. It does
**not** change any surface's prompt or citation logic (those stay with the
surface slices). It does **not** add a key-rotation UX beyond set/replace/clear
(rotation cadence is a follow-on tracked under the existing push-credential OQ
shape). It does **not** route by default — local stays default; cloud is an
explicit per-tenant opt-in. **Follow-on slices:** provider-key rotation UX;
per-surface cloud-eligibility policy (some surfaces may stay local-only even when
the tenant opts cloud — a future governance refinement).

## Threat model

STRIDE pass (design-time). Verdict: **has-mitigations** — the dominant surface is
**information disclosure (data egress to a third party)**; the banner +
explicit-opt-in + audit-provenance are the constitutional mitigations.

**S — Spoofing.** Setting a tenant's routing config / provider key is a
privileged admin action.
_Mitigation/AC:_ the routing-config endpoint requires tenant-admin role (reuse
the OAuth-AS JWT validation, slice 190); a lower-privilege user cannot switch the
tenant to a cloud provider or read the key.

**T — Tampering.** A tampered routing config could silently redirect a tenant's
generations to an attacker-controlled endpoint, exfiltrating evidence.
_Mitigation/AC:_ the provider set is a closed enum (`local-ollama` / `anthropic`
/ `openai` / `bedrock`) — no arbitrary URL injection; the provider endpoint is
the provider's official API, not operator-supplied free text. The routing config
is tenant-RLS-scoped so one tenant cannot rewrite another's routing.

**R — Repudiation.** "Which provider did my evidence go to?" must be answerable.
_Mitigation/AC:_ the slice-498 `ai_generations.model_provider` records the actual
provider per generation; combined with the immutable audit row, a tenant can
prove exactly which generations used a cloud backend and which stayed local.

**I — Information disclosure / third-party egress (PRIMARY).** Cloud routing
sends tenant-confidential evidence/policy text to a third party — the exact thing
the local-default posture avoids. The risks: (a) a tenant unaware their data is
leaving; (b) cross-tenant config bleed routing tenant A's data using tenant B's
provider/key; (c) the API key leaking.
_Mitigation/AC:_ (a) the **visible banner** on every draft surface names the
provider — opt-in is explicit and unmissable (the constitutional control);
(b) routing config is tenant-RLS-scoped + the provider/key are resolved under
`app.current_tenant` at generation time, proven by a cross-tenant test; (c) the
provider API key is encrypted at rest (reuse the existing secret-handling
pattern), never returned in API responses (write-only / masked), and never logged.

**D — Denial of service.** Cloud calls add latency + cost + an external
dependency; a cloud provider outage or rate-limit could stall AI-assist.
_Mitigation/AC:_ cloud calls inherit the slice-498 mandatory timeout + token
budget; a cloud failure surfaces a clear error (and the surface can fall back to
"generate locally" per its own UX) — it never hangs unbounded. Per-tenant rate
limiting still applies.

**E — Elevation of privilege.** No change to the approval gate: cloud routing
affects only _where_ a draft is generated, not _whether_ it can be published
without human approval.
_Mitigation/AC:_ the slice-498 `ai_assisted ↔ human_approver` enforcement is
backend-agnostic; a cloud-generated draft is still draft-only and still requires
one-click human approval. No path lets cloud routing bypass approval.

## Acceptance criteria

### Per-tenant routing config

- [ ] **AC-1.** A tenant-scoped routing config records the active provider
      (enum: `local-ollama` default / `anthropic` / `openai` / `bedrock`) + an
      encrypted-at-rest provider API key; the config is four-policy RLS-scoped.
- [ ] **AC-2.** Default for every tenant is `local-ollama` (no migration leaves a
      tenant on cloud by default); switching to cloud is an explicit
      tenant-admin action.
- [ ] **AC-3.** Setting/replacing/clearing the provider config + key requires
      tenant-admin role; the key is write-only/masked in responses and never
      logged.

### Cloud adapters behind the slice-498 interface

- [ ] **AC-4.** One cloud `Client` implementation per provider, selected at
      generation time by the requesting tenant's routing config, behind the
      **unchanged** slice-498 `Client` interface (callers in 440/441/444/471 are
      not modified).
- [ ] **AC-5.** A cloud generation records `model_provider` = the actual provider
      in the slice-498 `ai_generations` row (audit provenance).
- [ ] **AC-6.** Cloud calls inherit the slice-498 timeout + token budget; a cloud
      failure returns a clear error, never an unbounded hang.

### The visible banner

- [ ] **AC-7.** Every UI surface that renders an AI-assist draft shows
      "AI assist routes to {provider} — your data leaves this deployment" when the
      tenant is on a cloud provider; no banner when on `local-ollama`. The banner
      is driven by the tenant routing config, not hardcoded per surface.
- [ ] **AC-8.** The banner is present at draft-generation time and at
      draft-review time (wherever a draft appears), per canvas §4.6.5.

### Tests

- [ ] **AC-9.** Integration test: a tenant on `local-ollama` routes to the local
      client and shows no banner; a tenant switched to a cloud provider routes to
      the cloud adapter and records `model_provider` accordingly.
- [ ] **AC-10.** **Cross-tenant isolation test:** tenant A's generation uses
      tenant A's provider/key; it cannot be routed using tenant B's config/key
      (threat-model I-b).
- [ ] **AC-11.** Integration test: the provider key is never returned in an API
      response and never appears in logs (threat-model I-c).
- [ ] **AC-12.** Frontend test: the banner renders for a cloud-routed tenant and
      is absent for a local tenant (AC-7).
- [ ] **AC-13.** Integration test: a cloud-generated draft still requires
      `human_approver` for approval (approval gate is backend-agnostic).

### Docs / JUDGMENT artifact

- [ ] **AC-14.** Decisions log (`docs/audit-log/499-cloud-llm-opt-in-decisions.md`):
      the routing-config storage shape, the key-encryption approach, the banner
      placement decision, and the "Revisit once in use" list.
- [ ] **AC-15.** Operator docs: how to opt a tenant into a cloud provider, the
      data-egress implication, and the banner behavior.

## Constitutional invariants honored

- **AI-assist boundary (hard) / inference backend.** Cloud is **off by default**,
  **opt-in per tenant**, with a **visible banner** wherever drafts appear — the
  exact canvas §4.6.5 + CLAUDE.md commitment. The approval gate is unchanged
  (cloud routing does not bypass human approval).
- **#6 RLS tenant isolation** — routing config + provider key are tenant-scoped;
  cross-tenant routing/key bleed proven absent (AC-10).
- **No data leaves the deployment by default** — local Ollama remains default;
  egress is an explicit, banner-disclosed, per-tenant choice.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.6.5 — pluggable inference backend;
  cloud opt-in per-tenant with the "AI assist routes to {provider}" banner.
- `CLAUDE.md` "AI-assist boundary (hard)" → "Inference backend" — cloud opt-in
  per-tenant with a visible routing banner.

## Dependencies

- **#498 (this batch — `ready`/unbuilt)** — the `internal/llm` `Client`
  interface + the `ai_generations.model_provider` audit column. This slice adds
  cloud implementations behind that interface. **Hard dependency — `blocked`
  until 498 lands.**
- **#190 (merged)** — OAuth-AS JWT validation; the tenant-admin role gate.
- The secret-handling / encryption-at-rest pattern used for OAuth keys
  (`internal/auth/keystore`) informs the provider-key storage.

## Anti-criteria (P0 — block merge)

- **P0-499-1.** Does NOT route to a cloud provider by default — local Ollama is
  the default for every tenant; cloud is explicit opt-in.
- **P0-499-2.** Does NOT render any AI-assist draft from a cloud provider WITHOUT
  the "routes to {provider}" banner (canvas §4.6.5).
- **P0-499-3.** Does NOT allow an arbitrary/operator-supplied inference URL — the
  provider set is a closed enum (no SSRF / exfil-endpoint injection).
- **P0-499-4.** Does NOT return or log the provider API key (write-only/masked).
- **P0-499-5.** Does NOT let one tenant's generation use another tenant's
  provider/key (cross-tenant isolation — proven by AC-10).
- **P0-499-6.** Does NOT modify the slice-498 `Client` interface or any
  consumer's call site — adapters slot in behind the existing seam.
- **P0-499-7.** Does NOT bypass the human-approval gate — a cloud draft is still
  draft-only requiring `human_approver` (AC-13).

## Skill mix (3-5)

- `grill-with-docs` — align the routing + banner shape with canvas §4.6.5.
- `secrets-vault-manager` — the encrypted-at-rest provider key (write-only,
  masked, never logged).
- `tdd` — cross-tenant routing isolation + key-non-leakage + banner-presence
  tests are load-bearing.
- `security-review` — third-party data egress is the dominant risk surface.
- `database-designer` — the tenant-scoped routing-config table under four-policy
  RLS.

## Notes for the implementing agent

- **The banner is the constitutional control, not a UI nicety.** Drive it from
  the tenant routing config so a new AI-assist surface inherits it for free — do
  NOT hardcode the banner per surface. The data-egress honesty is the whole point
  of the opt-in being per-tenant + visible.
- **The closed provider enum prevents SSRF.** Resist any "custom endpoint" field
  — that turns the opt-in into an exfiltration primitive. Anthropic / OpenAI /
  Bedrock official endpoints only.
- **Build-ordering.** This slice is meaningless without slice 498's `Client`
  interface and `ai_generations.model_provider` column. Confirm 498 has landed at
  pickup; if not, this stays `blocked`.
- **Registration note (slice-382).** This slice's `_STATUS.md` row is NOT
  registered on this `docs/499` branch; the orchestrator registers it via a
  `chore/status` action after the spec PR merges.
