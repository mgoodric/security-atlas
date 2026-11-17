# #2424 — auditor evidence list bounded by the population window: decisions

Follow-up to #2157 D3 (`2157-auditor-period-resource-attrs-decisions.md`),
whose accepted widening let a whole-control population's list run over any
time range.

detection_tier_actual: none
detection_tier_target: integration

### D1 — the bound lives in the hydrator, not the handler

The controldetail `Evidence` handler stays period-unaware. The authz
hydrator (`internal/api/audit.ResourceAttrs`) gives the list an
`audit_period_ids` attribute only when the request's own `since` and `until`
fall inside a covering population's window, so the existing
`evidence_period_matches` rego rule needs no change. Every other role skips
the auditor rule, so its lists are unaffected.

### D2 — explicit bounds only

The handler defaults a missing `since` to 30 days ago and a missing `until`
to now. The hydrator does not reproduce those defaults: a missing or
malformed bound is denied. Each bound is parsed the same way the handler
parses it (`time.RFC3339`), so the hydrator checks the range the handler
then reads.

### D3 — both ends inside the window and freeze horizon

`periodsCovering` takes `[since, until]`. A population covers the range when
`populationWindowCovers` holds for both ends: on or after the window start,
on or before the window end, and on or before `frozen_at`. A record passes
its `observed_at` as both ends, which keeps #2157 D2 as it was. A range that
only partly overlaps the window is denied rather than clipped, because
clipping would mean rewriting the request's query.

### D4 — no `/audit` UI change

The ticket asked the `/audit` UI to send the window. The audit workspace
never calls `GET /v1/evidence`. Under invariant 10, `web/lib/api/audit.ts`
deliberately exposes no live evidence-list surface. So there is no call site
to change, and no e2e through the workspace would exercise this. The
dispatcher agreed (2026-11-16).

### Found, not fixed

The per-control list's envelope carries `total`, the **tenant-wide** ledger
count (`CountEvidenceForTenant`), so an admitted auditor learns how many
evidence records the whole tenant holds. It is metadata about the tenant
rather than any record. It needs its own ticket.
