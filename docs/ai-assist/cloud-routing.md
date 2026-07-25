# Cloud-LLM opt-in per-tenant routing (slice 499)

By default, security-atlas runs **all** AI-assist drafts on a **local Ollama**
model — no tenant data leaves your deployment. This is the safe default and
requires no configuration.

A tenant may **opt in** to a cloud LLM (Anthropic, OpenAI, or AWS Bedrock) for
higher-quality drafts. Cloud routing is:

- **Off by default** — every tenant is local-ollama until an admin opts in.
- **Per tenant** — in a multi-tenant deployment, one tenant can be on cloud
  while the rest stay local.
- **Disclosed by a visible banner** — wherever an AI-assist draft appears, a
  banner reads _"AI assist routes to {provider} — your data leaves this
  deployment."_ when the tenant is on a cloud provider. There is no banner on
  local-ollama.

> **Data-egress implication.** When a tenant is on a cloud provider, the
> assembled prompt for each AI-assist generation — which can include
> tenant-confidential evidence excerpts, policy text, and control descriptions —
> is sent to that third-party provider's API. This is the exact thing the
> local-default posture avoids. Opt in only for tenants whose data-handling
> agreements permit it. The `ai_generations.model_provider` audit column records
> the actual provider for every generation, so you can always prove which
> generations used a cloud backend and which stayed local.

## Enabling cloud routing on a deployment

Cloud routing requires a **deployment-level master key** that encrypts the
per-tenant provider API keys at rest. Without it, the platform refuses to store a
cloud key (a cloud opt-in returns _"cloud routing is not enabled on this
deployment"_). Provide a 32-byte (AES-256) key, base64-encoded:

- `ATLAS_LLM_CLOUD_KEY_FILE` — path to a `0600` file holding the base64 key
  (preferred for self-host), **or**
- `ATLAS_LLM_CLOUD_KEY` — the base64 key directly (e.g. from a secret manager).

Generate one with, for example: `openssl rand -base64 32`. Keep it out of
version control and rotate it per your secret-management policy. The master key
is never stored in the database and never logged.

## Opting a tenant into a cloud provider

The routing config is **tenant-admin gated** — a tenant admin (or a super
admin) can set it; lower-privilege users get a 403. The endpoint is
`/v1/admin/llm-routing` (BFF: `/api/admin/llm-routing`).

| Verb     | Effect                                                             |
| -------- | ------------------------------------------------------------------ |
| `GET`    | Read the tenant's current routing (masked — never returns the key) |
| `PUT`    | Set/replace the provider + API key                                 |
| `DELETE` | Clear the config — revert the tenant to local-ollama               |

`PUT` body:

```json
{ "provider": "anthropic", "api_key": "<your provider API key>" }
```

- `provider` is a **closed set**: `local-ollama`, `anthropic`, `openai`,
  `bedrock`. There is no custom-URL option (this is deliberate — an
  operator-supplied endpoint would be an exfiltration vector).
- `api_key` is **write-only**: it is encrypted at rest and is **never** returned
  by `GET` or `PUT` (the response carries only `has_api_key: true` and a
  `<redacted>` mask) and never written to logs.
- For `provider: "local-ollama"`, omit `api_key` (a local provider takes no
  key).

Example responses (note the key is never present):

```json
// after PUT { "provider": "anthropic", "api_key": "..." }
{ "provider": "anthropic", "is_cloud": true, "has_api_key": true, "api_key": "<redacted>" }

// after DELETE (reverted to default)
{ "provider": "local-ollama", "is_cloud": false, "has_api_key": false }
```

## Banner behavior

When a tenant is on a cloud provider, the routing banner renders on every
AI-assist surface that shows a draft — at both draft-generation and
draft-review time. On local-ollama, no banner renders. The banner is driven by
the tenant's routing config, so any AI-assist surface inherits it automatically.

## What does NOT change

- **The human-approval gate is unchanged.** A cloud-generated draft is still a
  draft: it still requires one-click human approval (with a recorded approver)
  before it can become an audit-binding artifact. Cloud routing changes _where_
  a draft is generated, never _whether_ it can be published without approval.
- **Tenant isolation is unchanged.** A tenant's provider + key are resolved
  under that tenant's row-level-security context; one tenant's generation can
  never use another tenant's provider or key.

## Provider notes

- **Anthropic** and **OpenAI** are fully supported (Messages API / Chat
  Completions API).
- **AWS Bedrock** routing, the banner, the audit provenance, and key handling
  are in place, but Bedrock's request signing (AWS SigV4) lands in a follow-on;
  treat Bedrock as preview until then.

## Follow-ons (not yet shipped)

- Provider-key rotation reminders / cadence UX (today: set / replace / clear).
- Per-surface cloud-eligibility policy (keep some surfaces local-only even when
  a tenant opts cloud).
- Bedrock SigV4 request signer.

See `docs/audit-log/499-cloud-llm-opt-in-decisions.md` for the design rationale,
and `docs/ai-assist/llm-foundation.md` for the local-inference substrate this
builds on.
