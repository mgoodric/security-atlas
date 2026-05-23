# 229 — UI honesty: dashboard header lacks tenant + snapshot-freshness subtitle

**Cluster:** Quality / UI parity (frontend)
**Estimate:** 0.5d
**Type:** AFK
**Status:** `ready`

## Narrative

Surfaced during the slice 204 per-page UI parity audit fleet (page slug: `dashboard`; mockup file: `Plans/mockups/dashboard.html`). Category (i) layout / chrome parity, with a (iii) data-bound-surface-that-lies overlap.

The dashboard mockup's H1 region (`Plans/mockups/dashboard.html` lines 117–124) renders three contextual strings:

1. **Tenant + environment slug** next to the H1: `"Sentinel Labs · production"`.
2. **Snapshot freshness subtitle**: `"Snapshot taken 18 minutes ago · evidence freshness 87% within window"`.

The live `/dashboard` header (`web/app/(authed)/dashboard/page.tsx` lines 96–102) renders only:

- H1: `"Program"`
- subtitle: `"The home screen for the security program — live posture, drift, risk, and what is coming up."`

The live subtitle is generic marketing copy — it does NOT communicate which tenant the operator is viewing, when the displayed data was computed, or what the aggregate freshness posture is. The mockup's subtitle is functional context (so the operator knows whether the dashboard is "stale at 4am" or "live within the last hour"); the live subtitle is decoration.

**Why this is a finding even though it's not a regression.** The dashboard already has the data — `freshnessQ.data` is fetched in the page and a tenant context is available via the `TenantSwitcher`. The values exist; the chrome simply doesn't surface them. The mockup encodes the right design intent (operator orientation in 1 line); the live build's chrome is a missed binding.

**Why `ready`.** The TenantSwitcher (`web/components/auth/tenant-switcher.tsx`) already reads the current tenant. The freshness query (`fetchDashboardFreshness`) already returns a `total` + `total_stale` from which "87% within window" is computable. The "Snapshot taken N minutes ago" needs a `received_at` from the freshness response (slice 016 added this on the freshness read-model). Implementation is a 2-3 line subtitle binding in `page.tsx`.

## Threat model

**S — Spoofing.** Tenant name is already exposed in the TenantSwitcher; rendering it next to the H1 surfaces no new data.

**I — Info disclosure.** "Sentinel Labs · production" is mockup placeholder; live binds to the active tenant only — no cross-tenant exposure.

**Verdict.** **no-mitigations-needed.** Pure UI chrome binding.

## Acceptance criteria

- **AC-1.** The dashboard H1 row displays `"{tenant.name} · {tenant.environment}"` (or `"{tenant.name}"` only if environment is unset) next to the "Program" H1.
- **AC-2.** The subtitle displays `"Snapshot taken {relativeTime} · evidence freshness {pct}% within window"` where `relativeTime` is derived from the freshness response's most-recent `received_at` and `pct` is `100 * (1 - total_stale / total)` rounded to int.
- **AC-3.** When the freshness query is loading, the subtitle renders a skeleton instead of the generic marketing copy.
- **AC-4.** When the freshness query errors, the subtitle renders `"Snapshot unavailable"` instead of the generic marketing copy.
- **AC-5.** When `total === 0` (bootstrap seed state), the subtitle renders `"No evidence ingested yet"` — honest about empty state, not "100% fresh of 0".

## Constitutional invariants honored

- **Invariant 6 (RLS at the DB layer).** The freshness endpoint already RLS-scopes to the active tenant; this slice only re-renders the existing response — no new data path.
- **AI-assist boundary (`CLAUDE.md` "AI-assist boundary").** Subtitle is deterministic computation, no LLM generation. Hallucination-free by construction.

## Canvas references

- `Plans/canvas/07-metrics.md` — freshness is a first-class KPI, surfacing it in the dashboard header is the canonical example.
- `Plans/canvas/04-evidence-engine.md` §4.5 — evidence freshness as a primary signal.

## Dependencies

- **Slice 016** (freshness read-model) — merged. Provides the data.
- **Slice 040** (program dashboard) — merged. Provides the freshness query already wired.

## Anti-criteria (P0 — block merge)

- **P0-A1.** DOES NOT render the mockup's literal `"Sentinel Labs · production"` string. The active tenant name comes from the auth context.
- **P0-A2.** DOES NOT render `"100% fresh"` when `total === 0`. AC-5 covers this honestly.

## Surfaced by

Slice 204 dashboard audit (parent). See `docs/audit-log/204-page-audit-dashboard.md` finding F-204D-2.
