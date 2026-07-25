# Slice 499 — cloud-LLM opt-in per-tenant routing + banner (decisions log)

JUDGMENT slice. Claude made the subjective build-time calls below using
best-reasoned, pattern-matched judgment, recorded them here, and shipped. The
runtime **AI-assist boundary is constitutional and untouched** — this slice
operationalizes it (cloud off-by-default, opt-in per tenant, visible banner, no
approval bypass); this log is a development-process artifact, not a relaxation
of that boundary.

- detection_tier_actual: integration
- detection_tier_target: integration

> One material premise discrepancy surfaced during the slice (caught at pickup,
> manual_review → confirmed via integration): the task brief said the AI-assist
> surfaces 440/441/444/471 are "none built yet". On this branch they ARE built
> (`internal/qaisuggest`, `internal/boardnarrative`, `internal/checklist`,
> `internal/gapexplain` + their API handlers + the `ai-suggest-panel.tsx` web
> surface), each already wired with a hardcoded `llm.NewOllamaClient(...)` and
> each already emitting a per-draft `cloud_routed` flag derived from
> `model_provider`. This made the slice CLEANER, not harder: the per-tenant
> router slots in behind the existing `llm.Client` seam at the 4 registration
> sites with zero call-site change, and the existing `cloud_routed` flag becomes
> truthful the moment a tenant opts in. No production exposure — the change is
> purely additive. See D2/D7.

## Decisions made

### D1 — routing-config storage shape: one row per tenant, absence = local

**Decision.** `tenant_llm_routing` is keyed by `tenant_id` (PRIMARY KEY): a
tenant has exactly zero or one routing row. **Absence of a row means
local-ollama** — the off-by-default posture (P0-499-1 / AC-2). The migration
performs NO backfill and creates NO default cloud row, so no tenant is ever left
on cloud by default.

**Options considered.** (a) A row-per-tenant with an explicit `local-ollama`
default value seeded for every tenant; (b) absence = local, only cloud opt-ins
carry a row (chosen). (b) wins because it makes "off by default" structural — a
tenant with no row is provably local without a migration that must touch every
tenant, and `Clear` (revert to local) is a `DELETE`, not an `UPDATE` to a magic
default. The on-row `provider` default is still `local-ollama` so an explicit
local row (an admin who set then cleared the key) is also valid.

### D2 — the per-tenant Router IS an `llm.Client`; the 4 surfaces are unchanged

**Decision.** Routing is a `cloud.Router` that itself implements the unchanged
slice-498 `llm.Client` interface. At `Generate` time it resolves the tenant's
config under `app.current_tenant` and dispatches to the local Ollama client
(default) or a per-provider cloud `Adapter` (with the tenant's decrypted key).
The only wiring change is swapping `llm.NewOllamaClient(llm.ConfigFromEnv())`
for `s.inferenceClient()` at the 4 surface registration sites
(register_questionnaire ×2, register_board, register_controlstate). **No surface
call site (`s.client.Generate`) and no slice-498 interface byte changes**
(P0-499-6).

**Options considered.** (a) Add a `provider` parameter to `Generate` (changes
the interface + every caller — violates P0-499-6); (b) resolve the backend
inside each surface service (duplicates routing logic 4×); (c) a Router that is
itself an `llm.Client` (chosen). (c) is the only option that honors P0-499-6 and
keeps routing defined once.

### D3 — key encryption: AES-256-GCM crypter, master key from env/file, not DB

**Decision.** The provider API key is encrypted at rest with **AES-256-GCM**
(nonce-prefixed, base64-encoded) by `cloud.Crypter`. The 32-byte master key
comes from `ATLAS_LLM_CLOUD_KEY_FILE` (a 0600 file, preferred) or
`ATLAS_LLM_CLOUD_KEY` (base64) — the deployment, **never the DB**. The key is
write-only: encrypted on the way in, never returned by the API (the read path
returns a `MaskedConfig` carrying only `has_api_key` + a `<redacted>` mask), and
never logged (the `cloud.Secret` type masks `%s/%v/%#v/%q/json.Marshal`).

**Why this rather than literally reusing `internal/auth/keystore`.** The brief
named keystore as the pattern to reuse. keystore stores **asymmetric ES256 JWT
signing keys as 0600 PEM files** — it is not a symmetric value-encryptor for a
DB column. The RIGHT reuse is the _pattern_, not the package: "key material on a
0600 file / env, never alongside the ciphertext it protects" (keystore's
discipline) + the `notify.Secret` masking type (the established
never-log-a-secret pattern). I reimplemented `Secret` package-locally in `cloud`
rather than import `notify` to avoid coupling the inference layer to the
notification layer. A future slice can graduate the crypter's master key to
KMS/HSM behind the same `Crypter` constructor without touching callers.

### D4 — closed provider enum, NO free-text endpoint (the SSRF guard)

**Decision.** `provider` is a closed enum
(`local-ollama`/`anthropic`/`openai`/`bedrock`) at three layers: a Go
`ParseProvider` gate, the DB `tenant_llm_routing_provider_chk` CHECK, and the
API handler (an unknown/URL provider → 400 before the DB). There is **no
endpoint / base-url column and no "custom" member** — the cloud endpoint is the
provider's official API, hard-coded in the adapter (P0-499-3). An
operator-supplied URL would turn the opt-in into an SSRF / exfiltration
primitive. A new provider requires a follow-on migration extending the CHECK +
a new Go adapter — deliberately a code change, not config.

### D5 — banner is config-driven via a reusable hook, not hardcoded per surface

**Decision.** The visible "AI assist routes to {provider} — your data leaves
this deployment" banner is driven by the tenant routing config through a
reusable layer: `lib/llm-routing/routing.ts` (the pure parser + the canonical
banner string), `useRoutingBanner()` (the hook that reads
`GET /api/admin/llm-routing`), and `<CloudRoutingBanner />` (the self-resolving
component that renders nothing on local-ollama). A surface drops in
`<CloudRoutingBanner />` and inherits the banner for free — **the deliverable is
the hook + component, so every future AI surface gets the banner without
re-authoring it** (the slice narrative's explicit requirement).

**Placement.** Wired into the one live AI-assist surface on this branch
(`ai-suggest-panel.tsx`) at the top of the panel, so the banner shows at BOTH
draft-generation time AND draft-review time (AC-8), independent of whether a
draft exists. The pre-existing per-draft `cloud_routed` banner is KEPT as
defense-in-depth (it reflects the actual provider that served THIS draft, which
catches a cloud-routed draft even if the config view is momentarily stale).

### D6 — provider adapters: anthropic + openai fully built; bedrock scaffolded

**Decision.** All three cloud `Client` adapters exist behind the unchanged
slice-498 interface and the same injectable HTTP seam, unit-tested with httptest
(request shape, auth header carries the key, timeout → ErrTimeout, non-200 →
ErrBackend, over-cap pre-IO rejection). **Anthropic and OpenAI are fully built**
(real Messages / Chat-Completions request+response codecs, correct auth headers
`x-api-key` / `Bearer`). **Bedrock is scaffolded**: it shares the Anthropic
message codec (Bedrock serves Anthropic models) and the request shape +
error-mapping + provenance are exercised, but it carries a `Bearer` auth header
as a placeholder for the **SigV4 request signer**, which is the named follow-on
(Bedrock auth is AWS SigV4, not a static key — a non-trivial signer that
warrants its own slice). This is honest scope: the routing/banner/audit/key
machinery is complete for all three; Bedrock's transport auth is the one
deferred piece, documented here and in the adapter.

### D7 — wiring is shared once via `Server.inferenceClient()`

**Decision.** A single `cloud.Store` + a single env-resolved `Crypter` are built
once (`sync.Once`) and shared; each surface gets its own thin `cloud.Router`
over the shared local client + Store. On a deployment with no cloud master key,
the crypter is nil: reads/clears still work, but a cloud opt-in is rejected with
a clear "cloud routing not enabled on this deployment" (409) — you cannot store
a key you cannot protect. This keeps the self-host default (no cloud key) a
local-only deployment with zero config.

### D8 — tenant-admin gate accepts the per-tenant `admin` role (not only super_admin)

**Decision.** The config endpoint gate (`requireTenantAdmin`) accepts a caller
who is `super_admin` OR holds the per-tenant `admin` role (`authz.RoleAdmin`),
mirroring the slice-484 `requireAdminActor` shape but broadened from
super-admin-only to tenant-admin (the spec says "tenant-admin role"). A
lower-privilege user (viewer/owner) is 403. The route is declared `adminBearer`
tier in RouteSpecs to match the `/v1/admin/` convention.

## Revisit once in use (named follow-ons — NOT built here)

- **Provider-key rotation UX.** This slice ships set / replace / clear only.
  Rotation cadence + a rotation-reminder surface track under the existing
  push-credential rotation OQ shape. Filed as a follow-up slice.
- **Per-surface cloud-eligibility policy.** A future governance refinement may
  keep some surfaces local-only even when the tenant opts cloud (e.g. board
  narratives stay local while questionnaire answers may go cloud). The router is
  the natural enforcement point; not built here. Filed as a follow-up slice.
- **Bedrock SigV4 signer.** Graduate the Bedrock adapter from scaffold to
  fully-built by replacing the placeholder bearer with an AWS SigV4 request
  signer. Filed as a follow-up slice.

## Constitutional invariants honored

- **AI-assist boundary (hard) / inference backend** — cloud is off by default
  (no row ⇒ local-ollama), opt-in per tenant (a per-tenant row set by a
  tenant-admin action), visible banner wherever a draft appears, approval gate
  unchanged (proven backend-agnostic by the integration test).
- **#6 tenant isolation** — routing config + key are four-policy-RLS-scoped
  under FORCE; resolved under `app.current_tenant`; cross-tenant bleed proven
  absent (AC-10).
- **No data leaves the deployment by default** — local Ollama remains default;
  egress is an explicit, banner-disclosed, per-tenant choice with a
  closed-enum provider (no SSRF vector).
