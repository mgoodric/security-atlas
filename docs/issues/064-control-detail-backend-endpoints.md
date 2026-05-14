# 064 — Control-detail backend read endpoints

**Cluster:** Controls / backend
**Estimate:** 1.5-2d
**Type:** AFK

## Narrative

Surface the missing per-control read endpoints that slice 041 (Control detail view) needs to fully ship. Slice 041 shipped the `/controls/[id]` UI plus four BFF proxies for the surfaces that already exist on main (coverage, control state, effectiveness, framework-scope intersection), and four **binding empty-state placeholders** for surfaces with no backend: the evidence stream, the linked-policies rail, the linked-risks rail, and the control-history / audit-log rail. Slice 041's decisions log (§2, §6, "Revisit once in use") records each placeholder and names the exact frontend seam.

This slice fills the four missing endpoints. It mirrors the 060→062 precedent: the frontend slice shipped the UI shells + wire-shape contracts as binding placeholders, and the backend slice fills in the real endpoints behind them.

Four endpoints:

1. **`GET /v1/evidence?control_id=...`** — paginated evidence-ledger records that fed a control's evaluation, default last-30-day window. Reuses slice 012's control→evidence resolution (the eval engine already knows how to find the evidence for a control); this endpoint returns the raw ledger rows instead of the computed state. This is the AC-4 gap slice 041 recorded as PARTIAL.
2. **`GET /v1/controls/{id}/policies`** — policies linked to the control via slice 022's policy→control mapping.
3. **`GET /v1/controls/{id}/risks`** — risks linked to the control via slice 020's `risk_control_links` table (merged in batch 22), with each link's weight + residual contribution.
4. **`GET /v1/controls/{id}/history`** — the control's evaluation history from slice 012's `control_evaluations` append-only ledger, newest-first. This is the audit-log rail the mockup shows.

The slice adds no new backend capability and no migration — it surfaces existing data (the evidence ledger, the policy library, `risk_control_links`, the evaluation ledger) behind per-control read paths. It delivers value because slice 041's control-detail view — already merged and the v1 control surface — can bind its four placeholders to real data.

## Acceptance criteria

- [ ] AC-1: `GET /v1/evidence?control_id=<id>` — returns paginated evidence-ledger records resolved for the control, default 30-day window (`?since=` / `?until=` ISO override), `?cursor=<opaque>` + `?limit=<int>` (max 200, default 50). Row shape: `{evidence_id, evidence_kind, observed_at, source, content_hash, scope_cell}`. Resolution reuses slice 012's control→evidence path — does NOT fabricate or re-derive linkage.
- [ ] AC-2: `GET /v1/controls/{id}/policies` — returns policies linked to the control via slice 022's policy→control mapping. Row shape: `{policy_id, title, version, status}`.
- [ ] AC-3: `GET /v1/controls/{id}/risks` — returns risks linked to the control via slice 020's `risk_control_links`. Row shape: `{risk_id, title, inherent_score, residual_score, link_weight}`.
- [ ] AC-4: `GET /v1/controls/{id}/history` — returns the control's evaluation history from slice 012's `control_evaluations` ledger, newest-first, paginated (`?cursor=` + `?limit=`, same bounds as AC-1). Row shape: `{evaluated_at, scope_cell, computed_state, freshness_status, evidence_count}`.
- [ ] AC-5: every endpoint is tenant-scoped through the standard RLS path — slice 033 middleware is the sole tenant-context setter; no endpoint accepts `tenant_id` in query or body. Read authz reuses the existing control-read check (auditor + grc_engineer + control_owner roles per slice 025/035); a role without control-read access gets 403.
- [ ] AC-6: all four endpoints mounted via the `httpserver.go` mount-append pattern (known-safe). Wire shapes match slice 041's four BFF proxy contracts (`web/app/api/controls/[id]/*` + the evidence route) — slice 041's merged PR (gh#93) is the spec.
- [ ] AC-7: integration test per endpoint (≥6 tests): evidence list respects the 30-day window + control scoping · policies endpoint returns 022-linked policies · risks endpoint returns 020-linked risks with residual · history endpoint returns `control_evaluations` rows newest-first · all four return 403 for an unauthorized role · all four are RLS-isolated across tenants (real Postgres, never mocked).
- [ ] AC-8: `CHANGELOG.md` entry under `[Unreleased]/Added`.

## Follow-up (out of scope — noted, not an AC)

Re-pointing slice 041's four frontend placeholders (`evidence-stream-section`, linked-policies / linked-risks / audit-log rail) to these endpoints is a small mechanical frontend change. Slice 041's decisions log identifies the exact seam (`web/app/(authed)/controls/[id]/page.tsx` + the four-BFF-route pattern). It is left as a follow-up frontend touch — this slice ships the endpoints + the wire-shape contracts only, keeping 064 single-language and AFK. Slice 041's AC-4 flips PARTIAL → PASS once the frontend is re-pointed.

## Constitutional invariants honored

- **Invariant 2 (ingestion/evaluation separated, append-only ledger):** the evidence endpoint (AC-1) and history endpoint (AC-4) are pure reads over append-only ledgers — no evaluation writeback, no evidence mutation.
- **Invariant 6 (RLS):** every endpoint reads through standard tenant-scoped tables; RLS policies fire on each underlying SELECT. The endpoints add no new table and no `BYPASSRLS` path.
- **Slice 033 D1** (tenancy middleware is the sole tenant-context setter): no endpoint accepts `tenant_id` in query or body.
- **Invariant 1 (one control, N framework satisfactions):** the policies / risks / evidence resolutions key off the control, not a per-framework duplicate.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` (evidence ledger + evaluation stage)
- `Plans/canvas/02-primitives.md` (Control, Risk, Evidence, Policy)
- `docs/issues/041-control-detail-view.md` + `docs/audit-log/041-control-detail-view-decisions.md` (the frontend slice + its placeholder gap inventory — §2, §6, "Revisit once in use")

## Dependencies

- **012** (control state evaluation engine — `control_evaluations` ledger + the control→evidence resolution path)
- **013** + **015** (evidence ledger write API + ingestion stage)
- **020** (`risk_control_links` table + residual derivation)
- **022** (policy library + policy→control mapping)
- **041** (control detail view — merged; its four BFF proxy contracts are the wire-shape spec)

All dependencies merged.

## Anti-criteria (P0 — block merge)

- Does NOT bypass tenant RLS on any of the four reads.
- Does NOT fabricate control→evidence, control→policy, or control→risk linkage — every linkage resolves through an existing merged table or query path.
- Does NOT accept `tenant_id` in query or body (slice 033 D1).
- Does NOT permit a role without control-read authz to reach any endpoint.
- Does NOT introduce an N+1 across rows — each endpoint is one query (plus at most one count for pagination).
- Does NOT add a migration — this slice is read-only over existing schema.

## Skill mix (3–5)

- Go HTTP read handlers + `httpserver.go` mount-append
- sqlc query layer (cursor-paginated reads)
- Reusing slice 012's control→evidence resolution rather than re-deriving it
- RLS-aware read endpoints + role-gated authz
- Cursor pagination over append-only ledgers
