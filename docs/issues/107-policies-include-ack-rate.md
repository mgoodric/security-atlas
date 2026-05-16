# 107 — `GET /v1/policies?include=ack_rate` extension for `/policies` list view

**Cluster:** Backend / API
**Estimate:** 1d
**Type:** AFK
**Status:** `ready`

## Narrative

Surfaced during slice 101, captured as follow-up per continuous-batch policy. Mirrors the slice 104 (`?include=state` for `/v1/anchors`) shape and rationale.

Slice 101 ships the `/policies` list view per the design captured in slice 093 (mockup `Plans/mockups/policies.html` + design doc `Plans/canvas/12-ui-fill-in-design-decisions.md` §7). Design doc §7 binds the policies table columns to `policyWire` + `rateResponse`:

| column                | wire source                                                               |
| --------------------- | ------------------------------------------------------------------------- |
| title                 | `policyWire.title` (`internal/api/policies/handlers.go`)                  |
| version               | `policyWire.version`                                                      |
| status                | `policyWire.status`                                                       |
| owner_role            | `policyWire.owner_role`                                                   |
| published_at          | `policyWire.published_at`                                                 |
| numerator/denominator | `rateResponse.numerator` / `.denominator` (`internal/api/policyacks/...`) |
| updated_at            | `policyWire.updated_at`                                                   |

On `main`, `GET /v1/policies` returns the policy library keyed by policy_id. `GET /v1/policies/{id}/acknowledgment-rate` returns the per-policy ack rate keyed by **policy_id**. There is no joined endpoint. Slice 101 explicitly rejected per-row fan-out (N policies × per-row rate call) per the slice text itself:

> The per-row fan-out needs care — if a tenant has 50+ policies, that's 50+ acks-rate calls per page load. Prefer extending the list endpoint with `?include=ack_rate` rather than client-side fan-out.

Slice 101 therefore ships with ack-rate cells rendered as `—` (slice 098 D1 precedent — labeled empty when the endpoint doesn't exist; no fabrication, anti-criterion P0-A2). This slice closes that gap by adding the joined endpoint so the `/policies` page can render real ack-rate for every policy with one round-trip.

## Acceptance criteria

- [ ] AC-1: `GET /v1/policies?include=ack_rate` returns the policy list joined to the tenant's current ack-rate per policy; when the policy is non-published (draft / under_review / approved / retired), the `ack_rate` field is `null`.
- [ ] AC-2: The joined response shape is `{ policies: [{ ...policyWire, ack_rate: {numerator, denominator, percent} | null }], count }` — additive to `policyWire`, no breaking change to `?include=` omitted callers.
- [ ] AC-3: `?include=ack_rate` resolves the rate column via the same `policy.AckStore.Rate` path as `GET /v1/policies/{id}/acknowledgment-rate` — no parallel computation path. The same `AcknowledgmentFreshness` window applies (slice 023).
- [ ] AC-4: The handler runs a single SQL query (CTE or LATERAL join) — NOT a per-policy loop calling `AckStore.Rate(ctx, id)` (the whole point of the extension).
- [ ] AC-5: RLS enforces tenant isolation on the joined query — the integration test asserts that Tenant A's request never sees Tenant B's ack rows (canvas §5.4).
- [ ] AC-6: For policies with `denominator = 0` (no required ack roles intersect the active user set, or freshness window not yet observed), `ack_rate.percent` is `null` and the UI renders `—` (matches slice 023 `rateResponse` shape).
- [ ] AC-7: Unit tests for the per-policy join (published policy with acks: rate passes through; draft policy: ack_rate is null; published policy with zero denominator: percent is null but numerator/denominator are 0/0).
- [ ] AC-8: Integration test covering the full RLS round-trip (Tenant A vs Tenant B) plus the non-published-status branch (`?include=ack_rate` on a tenant with only drafts returns `ack_rate: null` on every row).
- [ ] AC-9: `web/lib/api.ts` `Policy.ack_rate` field is already declared (slice 101 sketched the type); the only frontend change is `listPolicies` calls `?include=ack_rate` and the page picks up the populated cell. The `web/app/(authed)/policies/page.tsx` em-dash branch flips to the `<Progress>` branch when `ack_rate` is non-null.
- [ ] AC-10: CHANGELOG entry under `[Unreleased]` announcing the new `?include=ack_rate` query parameter.

## Constitutional invariants honored

- **Invariant 6 (RLS at DB layer):** the join query respects the tenant GUC. The handler does not source tenant from any caller-supplied field.
- **Invariant 9 (manual evidence is first-class):** ack-rate is computed against the same `policy_acknowledgments` table whether the policy is automated or manual; the join is uniform.
- **Anti-criteria honored:** the join is one query, not N — the slice exists specifically to avoid the per-row fan-out anti-pattern slice 101 P0-A2 forbids.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.5 (manual evidence first-class)
- `Plans/canvas/12-ui-fill-in-design-decisions.md` §7 (binding the column set to policyWire + rateResponse)
- `internal/api/policies/handlers.go` (`policyWire`, `ListPolicies`)
- `internal/api/policyacks/handlers.go` (`rateResponse`, `AcknowledgmentRate`)
- `internal/policy/ack_store.go` (`AckStore.Rate`)

## Dependencies

- **101** (`/policies` list view) — `in-review` at time of writing — provides the BFF + page consumer that will pick up the joined shape
- **022** (policy library) — merged — provides `policyWire` + the `policies` table
- **023** (policy acknowledgment) — merged — provides `AckStore.Rate` + the freshness window
- **033** (RLS + tenancymw) — merged — provides the tenant GUC the join trusts

## Anti-criteria (P0 — block merge)

- Does NOT add a per-policy loop in the handler (the slice exists to avoid it).
- Does NOT change the existing `?include=` omitted response shape — additive only.
- Does NOT route the join through application code that would bypass RLS — the query runs under the tenant GUC the middleware sets.
- Does NOT fabricate ack-rate for non-published policies — those rows return `ack_rate: null` and the UI renders `—` honestly (slice 101 P0-A2 + slice 098 D1 precedent).

## Skill mix

- sqlc + Atlas (joined CTE or LATERAL query against `policies` + `policy_acknowledgments`)
- Postgres RLS (per-tenant join verification)
- Negative tests (tenant isolation under join shape; non-published branch)

## Notes

This is the third `?include=` extension in the same shape (slice 104 for anchors → state, an analogous slice for evidence rollup will likely follow). The pattern is the same: ship the page against the v1 endpoint, file a backend slice to lift the per-row fan-out anti-pattern. Parent slice: 101.
