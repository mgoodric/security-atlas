# Slice 268 — Unified `/v1/search` endpoint — decisions log

**Slice spec:** [`docs/issues/268-unified-v1-search-endpoint.md`](../issues/268-unified-v1-search-endpoint.md)
**Branch:** `backend/268-unified-search-endpoint`
**Status:** in-review

This log captures the JUDGMENT calls the implementing agent made while
building the slice. The slice spec recorded three decision slots (D1–D3)
to be settled by the engineer; this file is the record. Three additional
engineer-side calls (D4–D6) were made during implementation and are
recorded here for traceability.

---

## D1 — Endpoint vs UI-side fan-out: **endpoint** (no change from spec)

The spec recommended an endpoint. The implementer concurs without
override.

**Rationale:**

1. **Single OPA decision per request.** A UI-side fan-out forces three
   independent OPA admits at the network edge; an endpoint collapses
   that to a single in-process Decide loop (D3 below). Audit-log
   amplitude is one row per request, not three.
2. **Bounded latency.** Server-side merge + truncate runs against one
   pool's connection set; UI-side fan-out's wall-clock is the slowest of
   three round-trips plus client-side merge.
3. **Global cross-type pagination cap.** `limit ≤ 50` is enforced after
   the merge. UI-side fan-out would have to either over-request and
   trim, or accept that each domain's cap counts separately.
4. **Single OpenAPI surface.** Slice 228 names this endpoint as its
   prerequisite; an endpoint commits the contract once.

**Trade-off accepted:** the BE owns the per-type-fanout complexity. The
hand-written `buildTokenizedQuery` helper composes the WHERE clause
dynamically. Future per-type indices (pg_trgm GIN) would land here.

---

## D2 — Lexical relevance, not vector: **lexical** (no change from spec)

`relevance_score = count_of_matched_tokens / total_q_tokens`. Implemented
as substring-overlap on the lower-cased haystack (mirrors ILIKE
semantics so the DB filter and Go-side score agree).

**Rationale:**

1. **v1 scope tightness.** No new schema migration (P0-A1), no extension
   install (P0-A2). pgvector would need an extension, an embedding model
   choice, an embedding-generation pipeline, and a per-row backfill —
   net work is a separate slice.
2. **Behind-the-API swap is cheap.** The handler computes scores in Go;
   swapping the DB filter from ILIKE to `pg_trgm`'s `similarity()` or to
   a `ts_rank_cd` vector match is a per-branch change in `searchType`
   that does not touch the wire shape.
3. **Acceptable quality for a discovery surface.** A solo security
   leader running ~50–300 controls + ~50 risks + ~10k evidence records
   (canvas §1.1) returns < 50 hits per query under realistic loads;
   ranking precision matters less than recall at this scale.

**Implementation note:** the per-token DB filter was the most
non-obvious shape (D5 below). The first cut used a single `q`-as-phrase
ILIKE; integration tests flagged the bug immediately — a `?q=iam+access`
query returned zero rows when the haystack had "iam" and "access" in
different columns. The fix is a per-token OR across columns
(`buildTokenizedQuery`).

---

## D3 — Per-type OPA admit, not single search admit (no change from spec)

The handler invokes `engine.Decide` once per requested type with
`resource.type = controls | risks | evidence`, then drops denied types
from the merge and surfaces them in `partial_types`.

**Rationale:**

1. **Matches existing per-domain patterns.** The existing per-domain
   admits (`controls.read`, `risks.read`, `evidence.read`) already
   reflect the canvas §9.5 role × resource matrix. A new "search admit"
   resource would force the rego author to re-implement role × resource
   for the union — duplicating intent.
2. **Partial-result UX is load-bearing.** A viewer-role caller who
   queries `/v1/search?q=iam` SHOULD see controls hits (viewer reads
   controls per `viewer.rego`); the spec's `partial_types: ["risks"]`
   shape lets the FE render "Risks results hidden — request access"
   without faking 403 on the whole call.
3. **A single search admit would either over- or under-admit.** Granting
   "search" to viewers would silently widen their effective read scope;
   denying it would 403 the whole search.

**Implementation note:** the endpoint-level admit (the middleware-level
gate at request entry) is a NEW rego rule in `defaults.rego`:

```rego
allow if {
    input.action == "read"
    input.resource.type == "search"
    count(input.user.roles) > 0
}
```

Any signed-in role passes the middleware; the per-type narrowing
happens inside the handler. A no-role request (bearer-exempt path)
still falls through to default-deny.

---

## D4 — Hand-written ILIKE, not sqlc (engineer call)

The three per-type queries are hand-written `pgx.Tx.Query` calls inside
the search package, NOT sqlc-generated functions in `internal/db/dbx`.

**Rationale:**

1. **Dynamic query shape.** The WHERE clause is one OR per token, where
   the token count is per-request. sqlc supports `sqlc.narg` for
   optional params but not dynamic clause repetition.
2. **No new schema migration.** sqlc regen happens only when a new
   migration lands; this slice has none (P0-A1). Hand-written queries
   keep the touch surface tight.
3. **Three branches sharing one helper.** `buildTokenizedQuery` keeps
   the WHERE/ORDER/LIMIT composition DRY across the three types.

**Trade-off accepted:** the hand-written queries don't benefit from
sqlc's compile-time SQL validation. The integration tests run the
queries against a real Postgres on every CI run; that's the regression
gate.

---

## D5 — Per-token OR across columns, not phrase ILIKE (engineer call)

Initial implementation passed `q` as a single ILIKE pattern (`%q%`) —
matching only the literal contiguous substring. Integration test IST-2
caught the bug: a multi-token query like `iam access review` returned
zero rows on a row whose title contained "iam" and "access" in
non-contiguous positions.

**Fix:** the WHERE clause is now one OR per token × columns:

```sql
WHERE (title ILIKE $1 OR description ILIKE $1)
   OR (title ILIKE $2 OR description ILIKE $2)
   OR (title ILIKE $3 OR description ILIKE $3)
```

This matches the per-token relevance scoring (`relevance(tokens,
haystack) = sum(token ∈ haystack) / len(tokens)`); without it, the DB
filter rejects rows the relevance scorer would have ranked > 0.

---

## D6 — Rune-bounded snippet (engineer call)

The `snippet` helper bounds output by RUNE count, not byte count. The
spec says "≤ 120 chars excerpt"; "chars" reads ambiguously, but the
ellipsis character `…` is 3 bytes / 1 rune. Bounding by bytes overflowed
the 120-byte ceiling by 2 bytes when both leading and trailing ellipses
fired.

Bounding by runes is the closer literal reading of "chars" (Unicode
code points) and keeps the snippet visually consistent in a UI that
counts characters.

---

## Implementation surface

| File                                                                        | Change                                                                                                                                                  |
| --------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `internal/api/search/search.go`                                             | NEW — `Handler`, `Handle`, per-type queries (`searchControls`, `searchRisks`, `searchEvidence`), helpers.                                               |
| `internal/api/search/search_test.go`                                        | NEW — pure unit tests (parseLimit, parseTypes, tokenize, relevance, snippet, escapeLike).                                                               |
| `internal/api/search/integration_test.go`                                   | NEW — `integration` build tag; 6 tests: cross-tenant isolation, happy path, types filter, 400 validations, global cap, partial_types shape.             |
| `internal/api/httpserver.go`                                                | Added `searchapi` import + `searchH := searchapi.New(s.dbPool, s.authzEngine)` + `root.Get("/v1/search", searchH.Handle)` next to the dashboard routes. |
| `policies/authz/defaults.rego` + `internal/authz/rego_bundle/defaults.rego` | Added `allow if action==read AND resource.type=="search" AND count(roles)>0` so any signed-in role passes the middleware-level admit.                   |
| `internal/api/openapi/routes.go`                                            | Added `GET /v1/search` RouteSpec; `tag: search`, `tier: bearer`.                                                                                        |
| `docs/openapi.yaml`                                                         | Regenerated via `just openapi-generate` (additive — one new `paths` entry + one new tag).                                                               |
| `CHANGELOG.md`                                                              | Added bullet under `## [Unreleased] → ### Added`.                                                                                                       |

---

## Anti-criteria audit

| ID    | Description                        | Status                                                                      |
| ----- | ---------------------------------- | --------------------------------------------------------------------------- |
| P0-A1 | No new schema migration            | ✅ `git diff main -- migrations/` is empty                                  |
| P0-A2 | No full-text-search infrastructure | ✅ ILIKE-only; no pgvector / pg_trgm / GIN tsvector                         |
| P0-A3 | NEVER return cross-tenant results  | ✅ AC-6 / IST-1: two-tenant fixture asserts zero leakage in both directions |
| P0-A4 | Per-type OPA admit independent     | ✅ `partitionByAdmit` runs Decide per type                                  |
| P0-A5 | q ≥ 2 chars                        | ✅ `MinQueryLen = 2`; IST-5 covers `q=""` and `q="a"` → 400                 |
| P0-A6 | ≤ 50 results per response          | ✅ `MaxLimit = 50`; IST-6 verifies global truncation                        |

---

## Open follow-ons (NOT blockers)

1. **Slice 228**: dashboard ⌘K bar consuming this endpoint. The wire
   shape (D1 + D5) is now committed; slice 228 can integrate.
2. **Quality upgrade**: if real-world use shows lexical recall is too
   thin, swap the per-type ILIKE for `pg_trgm`'s `similarity()` (the
   extension is already installed per slice 050's migration). The
   handler interface is unchanged.
3. **Per-IP rate-limit**: slice 188's token-bucket primitive could gate
   `/v1/search` to bound DoS amplification on the ILIKE plan. Out of
   slice scope; track as a security follow-on if traffic warrants.
4. **Audit-log subject_module**: the slice spec's threat-model row
   referenced `subject_module='search'` on the OPA admit decision; the
   current implementation lets the slice-035 audit writer set
   `subject_module='core'` (its hardcoded value). Splitting the search
   admit out to its own subject_module would mean a code change in
   `internal/authz/audit.go` — out of slice scope, and the slice spec
   itself says "no new audit-log surface here". Track as a future
   slice if cross-tenant-search audit ever needs to be queryable.
