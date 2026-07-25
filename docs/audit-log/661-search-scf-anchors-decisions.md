# Slice 661 — decisions log (search SCF anchors)

**Slice type:** JUDGMENT
**Detection-tier classification:**

- detection_tier_actual: production
- detection_tier_target: integration

The empty-tenant search gap was surfaced by the 2026-06-10 manual empty-tenant
UI audit (ATLAS-002) — i.e. caught in a production-like manual exercise. It
should have been caught at the integration tier: the slice 268 search
integration suite tested controls/risks/evidence on a populated tenant but had
no anchor-catalog case and no empty-tenant case, so a search surface with zero
instantiated controls was never exercised. This slice adds exactly that
integration case (`TestSlice661_AnchorSearch_EmptyTenant`).

---

## D1 — The anchor query (matched columns, latest-version dedup, the non-RLS catalog read, invariant-#6 safety)

**What:** A new `searchAnchors` branch queries the bundled `scf_anchors` SCF
catalog. It matches the tokenized query (per-token `ILIKE '%token%'`, OR across
columns — the same `buildTokenizedQuery` helper the three existing branches use)
against three columns:

- `a.scf_id` — so an exact anchor code like `CRY-04` matches by code.
- `a.title` — so `encryption` matches `"Encryption At Rest"` / `"Encryption In Transit"`.
- `a.description` — so longer name/intent queries match the anchor body.

The synthesized `SearchHit.Title` is `"<scf_id> — <title>"` (e.g. `CRY-04 —
Encryption At Rest`) so the FE renders a stable, code-prefixed label. The hit
`ID` is the anchor UUID. Relevance + snippet reuse the existing `relevance()` /
`snippet()` helpers over the `scf_id + title + description` haystack.

**Latest-version dedup:** an anchor (`scf_id`) exists once per SCF
`framework_version`, so a multi-version catalog would otherwise return the same
anchor N times. The query mirrors `ListSCFAnchorsLatest`
(`internal/db/queries/scf_anchors.sql`): it joins `framework_versions fv` +
`frameworks f` and filters `f.slug = 'scf' AND fv.status = 'current' AND
f.tenant_id IS NULL`. Only the current SCF version participates, so each
`scf_id` appears at most once. (Verified: `EXPLAIN ANALYZE` returns the single
matching anchor row, not duplicates.)

**The non-RLS catalog read — why it is invariant-#6-safe:** `scf_anchors`
carries **no `tenant_id` and no RLS** — it is platform-bundled, tenant-AGNOSTIC
reference data (migration `20260511000001_scf_anchors.sql` line 24: "The SCF
catalog is platform-bundled. No tenant_id, no RLS"). The anchor branch therefore
reads the catalog through a **new, deliberately-distinct** helper `queryCatalog`
— a plain `pool.Query` with **no** `tenancy.ApplyTenant` GUC — rather than the
RLS-bound `queryInTenantTx` the three tenant types use. This is
invariant-#6-safe **by construction**: there is no tenant column on
`scf_anchors`, so there is nothing to leak across tenants and nothing to scope.
The `queryCatalog` helper is used ONLY by `searchAnchors`; the
controls/risks/evidence branches continue to use `queryInTenantTx` so their RLS
tenant isolation is byte-for-byte unchanged. (The endpoint-level access to the
anchor catalog is already gated by the OPA `anchors` catalog-read admit in
`policies/authz/defaults.rego` `catalog_resources` — a public read for any
authenticated user, so no new policy rule was needed.)

## D2 — Anchor-vs-control relationship in results

Kept simple per the AC ("don't over-engineer dedup across the two types"). An
anchor hit and an instantiated-control hit are **independent** result rows: an
anchor is the catalog template; a control is the tenant's instantiation of it.
We do NOT collapse them. Rationale:

- On the empty tenant (the bug being fixed), there are zero controls, so only
  anchors surface — exactly the desired behavior.
- On a populated tenant, a user searching `encryption` legitimately wants both
  "here is the SCF CRY-04 anchor" (catalog) and "here is YOUR encryption
  control" (tenant) — they are different navigational targets (catalog detail
  vs control detail).
- The render order surfaces anchors first (see D3 FE note), then controls, so
  the catalog template reads as the heading and the instantiation below.

Cross-type dedup (suppress the anchor when its instantiated control is also a
hit) was considered and rejected as over-engineering: it would require joining
`controls.scf_anchor_id` into the anchor query (re-introducing a tenant-scoped
read into the tenant-agnostic branch — an invariant-#6 hazard) for marginal UX
gain. Deferred; not needed for the v1 binary criterion.

## D3 — The anchor hit link target

`/catalog/scf/{anchor.id}` — the existing SCF anchor detail page
(`web/app/(authed)/catalog/scf/[id]/page.tsx`). That route's `useParams<{id}>`
is fed the anchor UUID, exactly as the catalog list view already links
(`web/app/(authed)/catalog/scf/page.tsx`: `/catalog/scf/${anchor.id}`) and as
the control detail page links its anchor
(`controls/[id]/page.tsx`: `/catalog/scf/${encodeURIComponent(anchor.id)}`). So
the search hit's `id` (the anchor UUID) resolves the detail page with no new
route and no id-shape translation. The FE `hrefForHit` encodes the id.

## D4 — Latency finding + any index (AC-5)

**Finding: no index, no migration.** The reported multi-second `CRY-04` hang is
NOT the anchor query.

- `EXPLAIN ANALYZE` of the anchor search query against the seeded catalog: a
  seq scan of the (bounded) `scf_anchors` current-version rows, **Execution
  Time 0.139 ms**, 62 rows scanned (sample fixture; the full SCF release is
  ~1,400 anchors — still a sub-millisecond bounded scan).
- A leading-wildcard `ILIKE '%token%'` **cannot use a btree index** anyway, so
  the existing `idx_scf_anchors_family_scf_id (family, scf_id)` is irrelevant to
  this access pattern and adding another btree would not help.
- The catalog is bounded reference data, so the seq-scan cost does not grow with
  tenant data. A `pg_trgm` GIN index would be premature optimization and is
  explicitly out of scope per slice 268 anti-criterion P0-A2 (no full-text /
  trigram infrastructure until quality demands it).

**Actual latency cause:** cold-start of the fresh empty-tenant server (Go
connection-pool warmup) plus the **sequential per-type fan-out** — `Handle`
calls `searchType` in a `for` loop, now four round trips (anchors + the three
tenant types) on the FIRST request. The audit hit this on the first request to
a fresh server, not in steady state. The sequential fan-out is a pre-existing
slice 268 shape (bounded, server-side, one OPA admit per request); parallelizing
it is a separate optimization not warranted by a sub-millisecond per-type query.
**Conclusion:** documented, no cheap index materially helps, so none added — the
`proto/` + migration diff stays empty.

## D5 — Coverage

- **Go unit:** `internal/api/search` floor is 78 (merged unit+integration
  profile). Added unit case `TestParseTypes_AnchorsAdmitted` and updated
  `TestParseTypes_Default` for the new 4-type default set. The `searchAnchors` /
  `queryCatalog` statements are exercised by the integration test (which counts
  toward the merged coverage profile via `-coverpkg=./...`). The pure-Go helpers
  (relevance/snippet/buildTokenizedQuery) are reused unchanged, already covered.
- **Go integration:** new `TestSlice661_AnchorSearch_EmptyTenant`
  (`integration_test.go`, `//go:build integration`) — seeds the bundled catalog
  via `scfseed.EnsureFullCatalog` + a zero-control tenant; asserts `q=CRY-04`
  (anchor-code match, hit id is a UUID) and `q=encryption` (CRY-04 + CRY-08
  title match) return anchor hits, AND that the empty tenant's search NEVER
  returns a second tenant's "encryption" control (invariant #6). Verified to
  FAIL without the fix (`got hits=[]`) and pass with it. The pre-existing
  `TestSlice268_CrossTenantIsolation` (IST-1) continues to pass — the
  tenant-scoped RLS path is unchanged.
- **Frontend vitest:** updated `components/shell/global-search.test.ts`
  (4-bucket `groupByType`, `hrefForHit` anchors → `/catalog/scf/{id}`,
  `optionIdFor("anchors", …)`, 4-way collision) and added a
  `app/api/search/route.test.ts` passthrough case asserting `types=anchors` is
  forwarded verbatim (no BFF whitelist). All 1406 web vitest pass.

## %q-log discipline

No user-tainted value reaches a log sink in this change. The only `WriteInternal`
call in `search.go` passes `"search " + t` where `t` is a validated type
_constant_ (never the user's free-text `q`). The query string `q` is never
logged. CodeQL go/log-injection is not implicated.
