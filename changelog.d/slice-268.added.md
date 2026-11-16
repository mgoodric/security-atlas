- **api:** slice 268 — unified `GET /v1/search` endpoint aggregating
  lexical matches across controls, risks, and evidence into a single
  typed-union response. Closes the spillover gap surfaced by slice
  204's audit-fleet review: the dashboard mockup's global ⌘K search
  bar (slice 228) was filed `not-ready` because no unified search
  endpoint existed. This slice picks the endpoint shape (D1 — vs UI-
  side fan-out) so slice 228 + future cross-domain search surfaces
  share one canonical target. The handler at `internal/api/search/`
  re-invokes the slice 035 OPA engine PER TYPE (D3 — `resource.type
= controls|risks|evidence`), drops denied types from the merge,
  and surfaces them in the response's `partial_types` array so the FE
  can render "Risks results hidden — request access" hints. Per-type
  queries are hand-written ILIKE (D2 — lexical relevance, not vector;
  D4 — hand-written, not sqlc — the WHERE-clause-per-token shape is
  dynamic and unsuitable for static sqlc.narg) run under the
  atlas_app pool + tenancy.WithTenant GUC inside short-lived tx, so
  cross-tenant isolation is RLS-enforced (verified by
  `internal/api/search/integration_test.go` IST-1 per AC-6 / P0-A3).
  Relevance scoring is `count_of_matched_tokens / total_q_tokens`
  with per-token OR across columns (D5 — initial phrase-ILIKE missed
  rows whose matching tokens lived in disjoint columns). The merge
  sorts by `relevance_score` DESC; ties broken by `type` ASC then
  `id` ASC for stable ordering. Hard caps: `q ≥ 2` chars (P0-A5),
  `limit ≤ 50` (P0-A6, default 25), per-type top-K of 25 before
  merge. Unknown `types` values trip 400 (AC-7). The endpoint-level
  admit (middleware) is broadened in `policies/authz/defaults.rego`
  to admit `resource.type=="search"` for any signed-in role; per-
  type narrowing happens inside the handler — a no-role request
  still falls through to default-deny. OpenAPI surface added at
  `internal/api/openapi/routes.go` (`tag: search`, `tier: bearer`);
  `docs/openapi.yaml` regenerated. Test coverage: 8 unit tests
  (parseLimit, parseTypes, tokenize, relevance, snippet, escapeLike)
  - 6 integration tests (cross-tenant isolation, multi-token happy
    path, types filter, 400 validations, global cap, partial_types
    always-array shape). No new schema migration (P0-A1); no full-
    text-search infrastructure (P0-A2). Unblocks slice 228 (dashboard
    ⌘K search) — the FE integration can land independently against the
    now-frozen wire shape. See
    `docs/audit-log/268-unified-search-endpoint-decisions.md` for the
    JUDGMENT calls D1–D6.
