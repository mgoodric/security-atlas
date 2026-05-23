# 268 — Unified `/v1/search` endpoint (aggregates controls + risks + evidence)

**Cluster:** Backend (search / cross-domain query)
**Estimate:** ~1d
**Type:** JUDGMENT
**Status:** `ready`
**Parent:** spillover from slice 204 / precursor to slice 228 (dashboard global ⌘K search). Filed 2026-05-23 to unblock the not-ready spillover chain identified during the slice 204 audit-fleet aggregate. Slice 228 names this endpoint OR a UI-side fan-out as its prerequisite; this slice picks the endpoint shape so 228 + future cross-domain search surfaces have a single canonical target.

## Narrative

The slice 204 audit-fleet flagged the dashboard's mockup ⌘K global-search bar (slice 228) as not-ready because no unified search endpoint exists. Two domain-specific search slices DO ship: slice 064 (`/v1/controls?q=`) and slice 050 (`/v1/evidence?q=`). A risks search endpoint never landed. Slice 228 explicitly defers the shape choice to its implementing slice; this slice owns that decision.

The shape: `GET /v1/search?q=<query>&types=controls,risks,evidence&limit=N` returns a single typed-union result list with cross-tenant isolation (RLS-enforced via the standard atlas_app pool, NOT a privileged read). Initial scope covers three result types — controls, risks, evidence — with a `type` discriminator on each row + a stable `relevance_score` ordering across types (lexical token overlap; not full-text ranking; future slice can swap in pgvector or pg_trgm if quality demands it).

**Why endpoint vs fan-out**: a single backend endpoint serializes the cross-type sort + pagination cap (`max 50 results`) in one query plan. UI-side fan-out would make each domain's pagination independent and force the FE to do post-merge sorting — workable but more complex + harder to bound latency. Endpoint also gives a single OPA admit decision per request, not three.

### What ships in this slice

**Backend (`internal/api/search/`):**

- New package `internal/api/search/` with handler `Handle(w, r)` mounted at `chi.Get("/v1/search", ...)`. Reuses the slice 064 + slice 050 query patterns under the hood (the new package delegates per-type queries, then merges + sorts).
- Per-type query interface: `controls.Search(ctx, q, limit)`, `risks.Search(ctx, q, limit)`, `evidence.Search(ctx, q, limit)`. Each returns `[]SearchHit` with `{id, type, title, snippet, relevance_score}`.
- The risks search uses `ILIKE '%' || $1 || '%'` against `risks.title` + `risks.description` (no existing risks-search endpoint to leverage; this is the minimal addition).
- Aggregate merge: top-K from each, then global sort by `relevance_score`, capped at 50 total. `types` query param filters which domains are queried (default: all 3).

**OPA policy**: same admit set as the underlying per-type endpoints (controls.read + risks.read + evidence.read). A caller without permission on one type gets that type filtered out; the response includes a `partial_types` array noting which types were skipped due to authz.

**No new schema, no new migration.** The risks search is a single ILIKE; sufficient for v1.

**No frontend in this slice.** The dashboard ⌘K bar (slice 228) consumes this endpoint after it merges; that integration is slice 228's job.

## Threat model

| STRIDE                       | Threat                                                                                                                       | Mitigation                                                                                                                                                                                                                                                |
| ---------------------------- | ---------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **S** Spoofing               | n/a — read-only endpoint behind standard JWT auth.                                                                           | Inherits slice-190 jwtmw + slice-035 OPA.                                                                                                                                                                                                                 |
| **T** Tampering              | n/a — read-only.                                                                                                             | n/a                                                                                                                                                                                                                                                       |
| **R** Repudiation            | n/a — search queries don't mutate state. Audit-log writes could be added in a future slice for cross-tenant-search auditing. | OPA admit decision is logged via the existing slice-180 `subject_module='search'` audit channel; no new audit-log surface here.                                                                                                                           |
| **I** Information disclosure | Search results from one tenant leak to another via the union-merge step.                                                     | Per-type queries run under the standard atlas_app pool + tenancy.WithTenant GUC. The merge happens AFTER each per-type query returns; no cross-tenant data ever lives in the same query's row set. Verified by an explicit cross-tenant integration test. |
| **D** DoS                    | An empty / wildcard `q=` returns large result sets and triggers expensive ILIKE plans.                                       | Reject `q` shorter than 2 chars with 400 (per the existing slice 064 + 050 conventions). Hard `limit` cap of 50 + per-type top-K of 25. Future: add per-IP rate limit via slice-188's token-bucket primitive.                                             |
| **E** EoP                    | Caller searches across types they don't have authz on.                                                                       | OPA filters each type independently; `partial_types` array surfaces the filter to the caller so the FE doesn't show stale results without context.                                                                                                        |

## Acceptance criteria

- [ ] AC-1: `internal/api/search/` package created; `Handle(w, r)` mounted at `GET /v1/search` in `internal/api/httpserver.go`.
- [ ] AC-2: Query param: `q` (required, ≥2 chars), `types` (CSV; default `controls,risks,evidence`), `limit` (default 25, max 50).
- [ ] AC-3: Per-type query functions (`controls.Search`, `risks.Search`, `evidence.Search`) return `[]SearchHit{id, type, title, snippet, relevance_score}`. Snippet is ≤120 chars excerpt around the first matched token; `relevance_score` is `count_of_matched_tokens / total_q_tokens` (lexical).
- [ ] AC-4: Aggregate merge sorts by `relevance_score` DESC; ties broken by `type` ASC then `id` ASC for stable ordering.
- [ ] AC-5: `partial_types` array in response surfaces any type filtered out by OPA (so the FE can render "Risks results hidden — request access" hints).
- [ ] AC-6: Cross-tenant isolation integration test: two tenants each with 5 matching controls/risks/evidence; tenant A's search must NEVER return tenant B's rows.
- [ ] AC-7: 400 on `q` shorter than 2 chars; 400 on `limit > 50`; 400 on unknown `types` values.
- [ ] AC-8: Unit tests cover happy-path + each per-type filter + the OPA-filtered partial-types case.
- [ ] AC-9: Integration test covers the full HTTP surface against a real Postgres with two-tenant fixture.
- [ ] AC-10: OpenAPI spec entry added (per slice-191 RouteSpec convention).
- [ ] AC-11: CHANGELOG entry under `Added`. Slice 228's spillover gets called out as the unblocking target.

## Decisions

- **D1: Endpoint vs fan-out**: chose endpoint (see Narrative). Engineer may override with documented rationale.
- **D2: Lexical relevance, not vector**: keeps v1 scope tight. Future slice can swap implementation behind the same API contract.
- **D3: Per-type filtering via OPA, not pre-query gate**: each per-type call gets its own OPA decision; the merge step skips types the caller can't read. This is more lenient than a single-decision "search admit" rule and matches existing per-domain patterns.

## Constitutional invariants honored

- **RLS / tenancy (#6)**: per-type queries run via atlas_app + tenancy GUC; the aggregate merge composes results that are each already tenant-isolated.
- **One control, N framework satisfactions (#1)**: n/a — search is a discovery surface; doesn't touch the UCF graph.
- **Audit-log integrity (#2)**: existing slice-180 audit channels apply unchanged.
- **AI-assist boundary**: n/a — pure lexical search; no LLM in the loop.

## Anti-criteria (P0 — block merge)

- **P0-A1**: DOES NOT introduce a new schema migration.
- **P0-A2**: DOES NOT add full-text-search infrastructure (pgvector, pg_trgm, GIN tsvector). Future slice if quality demands it.
- **P0-A3**: DOES NOT return results across tenant boundaries under any circumstance — verified by AC-6.
- **P0-A4**: DOES NOT bypass per-type OPA admit. Each search type's admit is independent.
- **P0-A5**: DOES NOT accept `q=*` or empty `q` — minimum 2 chars enforced.
- **P0-A6**: DOES NOT return more than 50 results in a single response.

## Dependencies

- **#050** (evidence search) — merged. Per-type query reused.
- **#064** (controls search) — merged. Per-type query reused.
- **#190** (jwtmw) — merged. Auth substrate.
- **#035** (OPA middleware) — merged. Per-type admit.
- **#180** (audit-log subject_module) — merged. Audit channel reused.

## Unblocks

- **#228** (dashboard global ⌘K search) — the not-ready audit-fleet spillover that named this endpoint as its prerequisite. After this slice merges, #228 flips `not-ready` → `ready`.

## Skill mix

- Go HTTP handler + chi mount
- sqlc / hand-written queries for the risks ILIKE
- OPA admit-set composition
- Integration test against real Postgres
- OpenAPI RouteSpec entry

## Notes for the implementing agent

- `internal/api/search/` is a fresh package; no prior art to mimic exactly, but slice 064's package shape (`internal/api/controlsearch/`) is the closest reference for chi mount + handler signature.
- `controls.Search` should DELEGATE to the existing slice 064 query, not duplicate. Same for `evidence.Search` (slice 050).
- `risks.Search` needs a new sqlc query OR hand-written `ILIKE` against `risks` table. Pick whichever matches the existing risks-package convention.
- The `partial_types` shape is novel — document it carefully in the OpenAPI spec.
- If the engineer judges that lexical relevance is too thin and prefers `pg_trgm` similarity scoring, that's a documented D2 override (extension install is `CREATE EXTENSION IF NOT EXISTS pg_trgm;` — already present per slice 050's migration).
