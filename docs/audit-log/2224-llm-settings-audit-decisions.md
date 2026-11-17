# #2224 — audit-log changes to the LLM routing and local-rerank settings — decisions log

**Ticket:** https://github.com/mgoodric/security-atlas/issues/2224
**Date:** 2026-11-15

Follow-up from the review of #2208 (#2079). Two tenant settings decide
whether tenant text is sent to an LLM, and until now only `updated_by` /
`updated_at` recorded a change. Each change now writes one `me_audit_log` row
in the same transaction as the change:

| Endpoint                            | Action                           |
| ----------------------------------- | -------------------------------- |
| `PUT /v1/admin/llm-routing`         | `tenant_llm_routing_update`      |
| `DELETE /v1/admin/llm-routing`      | `tenant_llm_routing_clear`       |
| `PUT /v1/controls/imports/settings` | `control_import_settings_update` |

**Detection-tier classification (slice 353 / Q-13):**

- `detection_tier_actual`: none
- `detection_tier_target`: none

No bug surfaced during the work.

---

## D1 — `me_audit_log`, written by the stores

`me_audit_log` is the tenant audit log the other tenant settings use
(`tenant_self_approval_update`, `tenant_vendor_initial_review_update`). The
row is written inside `cloud.Store.Set` / `Clear` and
`controlimport.Store.SetSettings`, because those own the transaction; the
handlers pass the actor in. The actor is `credstore.CallerUserID(cred)`, which
handles both the JWT path (`user:<uuid>`) and a user-owned credential; a caller
that is not a user (a bootstrap key) is recorded as `uuid.Nil`, as the tenants
handler does.

## D2 — a routing row never holds the key

The before / after values are a dedicated `routingAudit` struct with four
fields: `provider`, `is_cloud`, `has_api_key`, and `api_key_written` (after
only). It has no field that could carry the key or its ciphertext, so a
secret cannot reach the row by a later edit that adds a field to the masked
config. `api_key_written` makes a key rotation under an unchanged provider
visible as a change. The integration test asserts the row's text contains
neither the plaintext key nor the stored ciphertext, and has no field outside
those four.

## D3 — what counts as a change

- Every successful routing `PUT` writes a row, including a re-PUT of the same
  provider: it always replaces the stored key.
- A `DELETE` writes a row only when it removed a config. Clearing a tenant
  already on the default changes nothing and writes nothing (it still answers
  200, unchanged).
- Every successful settings `PUT` writes a row with the before and after
  `llm_rerank_local`, matching the tenants handler, which audits each
  accepted PATCH field.
- A refused request (401/403, 400, 409) returns before the store is called or
  fails inside the store before the transaction commits, so it writes none.

## D4 — the CHECK rewrite is tested against the definition it replaces

Migration `20261120000000_llm_settings_audit.sql` rewrites the CHECK from
`20261116000000_vendor_initial_review_audit.sql`, keeping all 33 values and
appending three. `internal/db/llm_settings_audit_integration_test.go` reads
both lists, inserts every value, checks the down migration refuses while a row
with any new value exists, and re-applies the up migration.

## D5 — per-tenant fairness on rerank runs: deferred to #2329

The ticket's optional item: one tenant could take every `Stager.runs` slot
(default 4 per process). Deferred from this change, because it is a
scheduling change to `Stager.suggest`, separate from auditing.

The original wording here understated the cost. A waiting inline run gave
up when its request context ended and marked its batch **failed**, so a busy
tenant's reranks did not only delay another tenant's import: they could fail
it. #2329 fixes it with fair slot sharing between tenants and by deferring an
inline run that gets no slot to the background instead of failing it; see
`docs/audit-log/2329-llm-settings-audit-follow-ups-decisions.md` D3.
