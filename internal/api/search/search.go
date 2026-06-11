// Package search serves the slice-268 unified `/v1/search` endpoint —
// a single GET that aggregates lexical matches across controls,
// risks, evidence, and (slice 661) the bundled SCF anchor catalog, and
// returns them sorted by relevance.
//
// Wire shape (slice 268; slice 661 adds the `anchors` type):
//
//	GET /v1/search?q=<query>&types=anchors,controls,risks,evidence&limit=N
//
//	{
//	  "hits": [
//	    {"id":"<uuid>", "type":"anchors"|"controls"|"risks"|"evidence",
//	     "title":"...", "snippet":"...", "relevance_score":0.0..1.0}
//	  ],
//	  "count":  N,
//	  "partial_types": ["risks"]    // types filtered out by OPA
//	}
//
// Slice 661 — the `anchors` type fixes the empty-tenant gap (ATLAS-002):
// on a fresh tenant with zero instantiated controls, the ~53 bundled SCF
// anchors (CRY-04, etc.) were unindexed, so the operator's most natural
// query ("find CRY-04" / "encryption") returned nothing. The anchor
// branch reads the tenant-AGNOSTIC scf_anchors catalog directly (no RLS
// — invariant #6 is preserved because there is no tenant column to leak
// across; the three tenant types keep their RLS-bound path unchanged).
//
// Design (slice 268 narrative + decisions):
//
//   - **D1 endpoint vs UI-side fan-out**: single endpoint. One OPA admit
//     decision per request, bounded latency, server-side cross-type
//     sort + global cap. UI-side fan-out would force three independent
//     paginations + a client-side merge.
//   - **D2 lexical relevance, not vector**: token-overlap scoring
//     (count_of_matched_tokens / total_q_tokens). Future slice can
//     swap to pg_trgm or pgvector behind the same API shape.
//   - **D3 per-type OPA admit**: the package re-invokes the OPA engine
//     PER TYPE with `resource.type = controls|risks|evidence`. Types
//     where OPA denies are dropped from the merge and surface in the
//     response's `partial_types` array. This is more lenient than a
//     single "search admit" rule and matches the per-domain admit
//     pattern of slices 064 + 050.
//
// Constitutional invariants honored:
//
//   - **#6 RLS / tenancy**: per-type queries run via the atlas_app pool,
//     with tenancy.ApplyTenant set inside a transaction. The aggregate
//     merge happens AFTER each per-type query returns; no cross-tenant
//     data ever lives in the same query's row set.
//   - **AI-assist boundary**: n/a — pure lexical search.
//
// Anti-criteria honored (P0, slice 268):
//
//   - **P0-A1**: no new schema migration. Hand-written ILIKE on
//     existing columns (controls.title/description, risks.title/
//     description, evidence_records.evidence_kind/control_ref).
//   - **P0-A2**: no full-text-search infrastructure (no pgvector /
//     pg_trgm / GIN tsvector). Future slice if quality demands it.
//   - **P0-A3**: cross-tenant isolation enforced by RLS; verified by
//     `cross_tenant_isolation_integration_test.go` (AC-6).
//   - **P0-A4**: per-type OPA admit is independent.
//   - **P0-A5**: q must be ≥ 2 chars; 400 otherwise.
//   - **P0-A6**: hard cap of 50 results per response (default 25);
//     per-type top-K of 25 before the merge.
package search

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Result-cap constants. Public so tests can pin the contract.
const (
	// MinQueryLen is the minimum length of the `q` query parameter
	// (P0-A5). Shorter values trip a 400.
	MinQueryLen = 2
	// MaxLimit is the hard ceiling on `limit` (P0-A6). Larger values
	// trip a 400.
	MaxLimit = 50
	// DefaultLimit is the assumed `limit` when the caller omits it.
	DefaultLimit = 25
	// PerTypeTopK is the per-type cap applied BEFORE the merge. Three
	// types × 25 = up to 75 candidates feeding the global merge, which
	// then truncates to `limit` (≤ MaxLimit) after sort.
	PerTypeTopK = 25
	// SnippetMaxLen is the AC-3 snippet cap (≤ 120 chars).
	SnippetMaxLen = 120
)

// Type names — exported so callers / tests can spell them safely.
const (
	TypeControls = "controls"
	TypeRisks    = "risks"
	TypeEvidence = "evidence"
	// TypeAnchors is the slice-661 SCF-anchor catalog result type. Unlike
	// the other three, anchors are platform-bundled, tenant-AGNOSTIC
	// reference data (scf_anchors carries no tenant_id and no RLS — see
	// migration 20260511000001_scf_anchors.sql line 24). The anchor branch
	// therefore reads the catalog directly, NOT through the tenant-GUC
	// path. controls/risks/evidence stay RLS-scoped exactly as before.
	TypeAnchors = "anchors"
)

// allTypes is the default `types` set when the caller omits the param
// (declared in canonical sort order so the response's partial_types
// array reads deterministically). `anchors` sorts first alphabetically.
var allTypes = []string{TypeAnchors, TypeControls, TypeEvidence, TypeRisks}

// Handler bundles the slice-268 routes. It owns:
//
//   - `pool`: the atlas_app pgxpool used for every per-type query
//     (RLS-gated via tenancy.ApplyTenant inside a tx).
//   - `engine`: the slice-035 OPA engine. The handler invokes it once
//     per requested type to compute the per-type admit (D3). When nil
//     (unit-test server with no authz wired), the handler admits every
//     type — the upstream middleware is the production gate.
type Handler struct {
	pool   *pgxpool.Pool
	engine *authz.Engine
}

// New constructs a Handler. The OPA engine is optional; pass nil for
// unit-test servers that don't wire authz.
func New(pool *pgxpool.Pool, engine *authz.Engine) *Handler {
	return &Handler{pool: pool, engine: engine}
}

// ----- wire shapes -----

// SearchHit is the on-the-wire shape of a single result row. The same
// shape is emitted regardless of underlying type (the `type` field is
// the discriminator).
//
// `RelevanceScore` is the AC-3 lexical score: matched / total tokens
// in the query, capped at 1.0. Higher is better.
type SearchHit struct {
	ID             string  `json:"id"`
	Type           string  `json:"type"`
	Title          string  `json:"title"`
	Snippet        string  `json:"snippet"`
	RelevanceScore float64 `json:"relevance_score"`
}

type searchResp struct {
	Hits         []SearchHit `json:"hits"`
	Count        int         `json:"count"`
	PartialTypes []string    `json:"partial_types"`
}

// Handle serves GET /v1/search. See package doc for the contract.
//
// Validation runs first (q length, limit, types). OPA admit runs next
// (per-type Decide; denied types are recorded in partial_types and
// skipped). Each admitted type is queried under the tenant GUC; the
// hits are merged + sorted and truncated to `limit` ≤ MaxLimit.
func (h *Handler) Handle(w http.ResponseWriter, r *http.Request) {
	// Tenant context check. The upstream slice-033 tenancy middleware
	// has already lifted the credential's tenant id onto r.Context();
	// a missing tenant means an unauthenticated path reached the
	// handler — 401 keeps shape with the rest of the /v1/* surface.
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}

	// AC-2 / AC-7: validate q length.
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) < MinQueryLen {
		httpresp.WriteError(w, http.StatusBadRequest,
			fmt.Sprintf("q must be at least %d characters", MinQueryLen))

		return
	}

	// AC-2 / AC-7: validate limit.
	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	// AC-2 / AC-7: validate types CSV. Empty (default) = all three.
	requestedTypes, err := parseTypes(r.URL.Query().Get("types"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	// D3: per-type OPA admit. Build one Decide call per type. Types
	// where Decide returns Allow=false are dropped from the query
	// set and surfaced in partial_types (AC-5) so the FE can render
	// "Risks results hidden — request access" hints.
	admitted, partial := h.partitionByAdmit(r, requestedTypes)

	// Run the per-type queries against the tenant-gated pool. Each
	// call opens its own short-lived tx so the GUC scope is bounded
	// and concurrent searches don't share a tx.
	tokens := tokenize(q)
	var hits []SearchHit
	for _, t := range admitted {
		typeHits, qerr := h.searchType(r.Context(), t, q, tokens)
		if qerr != nil {
			httperr.WriteInternal(w, r, "search "+t, qerr)
			return
		}
		hits = append(hits, typeHits...)
	}

	// AC-4: aggregate merge by relevance_score DESC; ties broken by
	// type ASC then id ASC for stable ordering.
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].RelevanceScore != hits[j].RelevanceScore {
			return hits[i].RelevanceScore > hits[j].RelevanceScore
		}
		if hits[i].Type != hits[j].Type {
			return hits[i].Type < hits[j].Type
		}
		return hits[i].ID < hits[j].ID
	})

	// AC-2 / P0-A6: truncate to `limit`. The defensive belt-and-
	// suspenders: limit is already ≤ MaxLimit (parseLimit enforces).
	if len(hits) > limit {
		hits = hits[:limit]
	}

	// partial_types is always an array on the wire (never null) so
	// downstream clients can iterate unconditionally.
	if partial == nil {
		partial = []string{}
	}

	httpresp.WriteJSON(w, http.StatusOK, searchResp{
		Hits:         hits,
		Count:        len(hits),
		PartialTypes: partial,
	})

}

// partitionByAdmit splits requestedTypes into (admitted, denied) by
// calling engine.Decide once per type with resource.type set to the
// type name. When the engine is nil (unit test path) every type is
// admitted — the upstream middleware is the production gate.
//
// The partial_types order mirrors the input order so the response is
// deterministic for a given request.
func (h *Handler) partitionByAdmit(r *http.Request, requestedTypes []string) (admitted, denied []string) {
	if h.engine == nil {
		return requestedTypes, nil
	}
	admitted = make([]string, 0, len(requestedTypes))
	denied = nil
	for _, t := range requestedTypes {
		in := authz.BuildInput(r, nil)
		in.Resource.Type = t
		in.Action = "read"
		decision, err := h.engine.Decide(r.Context(), in)
		if err != nil || !decision.Allow {
			denied = append(denied, t)
			continue
		}
		admitted = append(admitted, t)
	}
	return admitted, denied
}

// searchType dispatches one per-type query. Each branch issues a
// hand-written ILIKE under a short-lived tx with the tenant GUC
// applied. Hand-written rather than sqlc to keep the query shape
// dynamic (the same query body is reused for three types with
// different tables / column projections) and to avoid a schema regen
// for the single-purpose lexical query.
//
// The cred.UserID surfaced on the audit channel via the upstream
// authz middleware is sufficient for the slice's threat model — no
// new audit-log surface is introduced (slice 268 narrative §I).
func (h *Handler) searchType(ctx context.Context, t, q string, tokens []string) ([]SearchHit, error) {
	switch t {
	case TypeControls:
		return h.searchControls(ctx, q, tokens)
	case TypeRisks:
		return h.searchRisks(ctx, q, tokens)
	case TypeEvidence:
		return h.searchEvidence(ctx, q, tokens)
	case TypeAnchors:
		return h.searchAnchors(ctx, q, tokens)
	default:
		// Unreachable — parseTypes already validated the set.
		return nil, fmt.Errorf("unknown search type %q", t)
	}
}

// searchControls queries `controls` (active versions only —
// superseded rows are excluded so the UCF read shape matches
// slice 151's ListActiveControls projection). Searches title +
// description per-token; a row matches when ANY token appears in
// either column. Relevance is per-row token overlap on the
// concatenation (see relevance()).
func (h *Handler) searchControls(ctx context.Context, q string, tokens []string) ([]SearchHit, error) {
	stmt, args := buildTokenizedQuery(`
		SELECT id::text, title, description
		FROM controls
		WHERE superseded_by IS NULL
		  AND `, []string{"title", "description"}, tokens, q, "ORDER BY title ASC")
	rows, err := h.queryInTenantTx(ctx, stmt, args...)
	if err != nil {
		return nil, err
	}
	out := make([]SearchHit, 0, len(rows))
	for _, r := range rows {
		out = append(out, SearchHit{
			ID:             r["id"],
			Type:           TypeControls,
			Title:          r["title"],
			Snippet:        snippet(r["title"]+" "+r["description"], q),
			RelevanceScore: relevance(tokens, r["title"]+" "+r["description"]),
		})
	}
	return out, nil
}

// searchRisks queries `risks`. Searches title + description per-token;
// a row matches when ANY token appears in either column. NO existing
// risks-search query exists upstream (the spec calls this out) —
// this is the minimal addition.
func (h *Handler) searchRisks(ctx context.Context, q string, tokens []string) ([]SearchHit, error) {
	stmt, args := buildTokenizedQuery(`
		SELECT id::text, title, description
		FROM risks
		WHERE `, []string{"title", "description"}, tokens, q, "ORDER BY title ASC")
	rows, err := h.queryInTenantTx(ctx, stmt, args...)
	if err != nil {
		return nil, err
	}
	out := make([]SearchHit, 0, len(rows))
	for _, r := range rows {
		out = append(out, SearchHit{
			ID:             r["id"],
			Type:           TypeRisks,
			Title:          r["title"],
			Snippet:        snippet(r["title"]+" "+r["description"], q),
			RelevanceScore: relevance(tokens, r["title"]+" "+r["description"]),
		})
	}
	return out, nil
}

// searchEvidence queries `evidence_records`. The table has no `title`
// or `description` columns; the searchable text is the
// `evidence_kind` + `control_ref` pair. The synthesized title is
// "<evidence_kind> · <control_ref>" so the FE can render a stable
// label.
//
// Newest-first ordering by observed_at because evidence is
// append-only and the most recent observation per kind is usually the
// most relevant; we explicitly do NOT collapse to one row per
// (kind, control_ref) — letting the global merge truncate is simpler
// and matches the per-type top-K contract.
func (h *Handler) searchEvidence(ctx context.Context, q string, tokens []string) ([]SearchHit, error) {
	stmt, args := buildTokenizedQuery(`
		SELECT id::text,
		       COALESCE(evidence_kind, '') AS evidence_kind,
		       control_ref
		FROM evidence_records
		WHERE `, []string{"evidence_kind", "control_ref"}, tokens, q, "ORDER BY observed_at DESC, id ASC")
	rows, err := h.queryInTenantTx(ctx, stmt, args...)
	if err != nil {
		return nil, err
	}
	out := make([]SearchHit, 0, len(rows))
	for _, r := range rows {
		label := r["evidence_kind"]
		if label == "" {
			label = "evidence"
		}
		title := label + " · " + r["control_ref"]
		out = append(out, SearchHit{
			ID:             r["id"],
			Type:           TypeEvidence,
			Title:          title,
			Snippet:        snippet(title, q),
			RelevanceScore: relevance(tokens, title),
		})
	}
	return out, nil
}

// searchAnchors queries the bundled `scf_anchors` SCF catalog (slice
// 661). Unlike the three tenant-scoped branches above, this is the ONE
// tenant-AGNOSTIC search surface: scf_anchors has no tenant_id and no
// RLS — it is platform-bundled reference data (migration
// 20260511000001_scf_anchors.sql line 24). The query therefore runs on
// the plain pool (queryCatalog), NOT queryInTenantTx, and carries no
// tenant predicate. This is invariant-#6-safe BY CONSTRUCTION: there is
// no tenant column to leak across, and the controls/risks/evidence
// branches keep their RLS-bound tenant path untouched.
//
// Latest-version dedup: an anchor (scf_id) exists once per SCF
// framework_version, so a multi-version catalog would otherwise return
// the same anchor N times. We mirror ListSCFAnchorsLatest
// (internal/db/queries/scf_anchors.sql) — join framework_versions +
// frameworks and filter `slug='scf' AND status='current' AND
// tenant_id IS NULL`, so only the current version participates and each
// scf_id appears at most once.
//
// Matched columns: scf_id (so `CRY-04` matches by code) + title +
// description (so `encryption` matches by name). The synthesized title
// is "<scf_id> — <title>" so the FE renders a stable, code-prefixed
// label. The hit ID is the anchor UUID, which the FE links to
// /catalog/scf/<id> (the existing anchor-detail page accepts the UUID).
func (h *Handler) searchAnchors(ctx context.Context, q string, tokens []string) ([]SearchHit, error) {
	stmt, args := buildTokenizedQuery(`
		SELECT a.id::text, a.scf_id, a.title, a.description
		FROM scf_anchors a
		JOIN framework_versions fv ON fv.id = a.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug = 'scf' AND fv.status = 'current' AND f.tenant_id IS NULL
		  AND `, []string{"a.scf_id", "a.title", "a.description"}, tokens, q, "ORDER BY a.scf_id ASC")
	rows, err := h.queryCatalog(ctx, stmt, args...)
	if err != nil {
		return nil, err
	}
	out := make([]SearchHit, 0, len(rows))
	for _, r := range rows {
		haystack := r["scf_id"] + " " + r["title"] + " " + r["description"]
		title := r["scf_id"] + " — " + r["title"]
		out = append(out, SearchHit{
			ID:             r["id"],
			Type:           TypeAnchors,
			Title:          title,
			Snippet:        snippet(haystack, q),
			RelevanceScore: relevance(tokens, haystack),
		})
	}
	return out, nil
}

// buildTokenizedQuery composes the WHERE clause + parameter list for
// a per-token ILIKE query against the supplied columns. A row matches
// when ANY token appears in ANY column — the lexical OR shape
// matching the per-token relevance scoring.
//
// `prefix` is the SELECT + FROM + initial WHERE prefix (caller-owned
// so the per-table base — e.g. `superseded_by IS NULL AND` — can be
// inlined). `orderBy` is appended after the WHERE clause; the LIMIT
// clause uses the final placeholder.
//
// Returns (stmt, args) ready to pass to pgx Query. The last arg is
// always PerTypeTopK so the per-type cap stays consistent across the
// three branches.
//
// Falls back to a single-pattern ILIKE on the raw query when the
// caller passed an empty tokens slice — defensive, since Handle
// validates q is ≥ MinQueryLen chars before reaching here.
func buildTokenizedQuery(prefix string, columns, tokens []string, q, orderBy string) (string, []any) {
	if len(tokens) == 0 {
		tokens = []string{strings.ToLower(strings.TrimSpace(q))}
	}
	var clauses []string
	var args []any
	for _, token := range tokens {
		pattern := "%" + escapeLike(token) + "%"
		args = append(args, pattern)
		var perCol []string
		for _, col := range columns {
			perCol = append(perCol, fmt.Sprintf("%s ILIKE $%d", col, len(args)))
		}
		clauses = append(clauses, "("+strings.Join(perCol, " OR ")+")")
	}
	args = append(args, PerTypeTopK)
	stmt := prefix + "(" + strings.Join(clauses, " OR ") + ") " + orderBy + fmt.Sprintf(" LIMIT $%d", len(args))
	return stmt, args
}

// queryInTenantTx opens a short-lived tx on the pool, applies the
// tenant GUC, runs `stmt` with the supplied args, and returns rows as
// generic string-map records. The map is keyed on the SELECT'd
// column names — only the three queries above use this helper so a
// minimal projection keeps the helper simple.
//
// RLS is the primary tenant-isolation guard (constitutional invariant
// #6); a missing GUC denies all rows.
func (h *Handler) queryInTenantTx(ctx context.Context, stmt string, args ...any) ([]map[string]string, error) {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return nil, fmt.Errorf("apply tenant: %w", err)
	}

	rows, err := tx.Query(ctx, stmt, args...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	cols := rows.FieldDescriptions()
	colNames := make([]string, len(cols))
	for i, c := range cols {
		colNames[i] = string(c.Name)
	}

	var out []map[string]string
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		row := make(map[string]string, len(values))
		for i, v := range values {
			row[colNames[i]] = stringifyValue(v)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return out, nil
}

// queryCatalog runs `stmt` directly on the pool with NO tenant GUC
// applied — the slice-661 anchor path. It is deliberately distinct from
// queryInTenantTx: scf_anchors is platform-bundled, tenant-AGNOSTIC
// catalog data (no tenant_id, no RLS), so there is nothing to scope and
// applying a tenant GUC would be meaningless. Every authenticated
// caller reads the same anchor rows (the OPA `anchors` catalog-read
// admit already gates the endpoint-level access; see
// policies/authz/defaults.rego catalog_resources).
//
// CRITICAL invariant-#6 boundary: this helper is used ONLY by
// searchAnchors. The controls/risks/evidence branches MUST continue to
// use queryInTenantTx so their RLS tenant isolation is preserved.
func (h *Handler) queryCatalog(ctx context.Context, stmt string, args ...any) ([]map[string]string, error) {
	rows, err := h.pool.Query(ctx, stmt, args...)
	if err != nil {
		return nil, fmt.Errorf("catalog query: %w", err)
	}
	defer rows.Close()

	cols := rows.FieldDescriptions()
	colNames := make([]string, len(cols))
	for i, c := range cols {
		colNames[i] = string(c.Name)
	}

	var out []map[string]string
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return nil, fmt.Errorf("catalog scan: %w", err)
		}
		row := make(map[string]string, len(values))
		for i, v := range values {
			row[colNames[i]] = stringifyValue(v)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog rows: %w", err)
	}
	return out, nil
}

// stringifyValue coerces pgx-returned values (strings, []byte, etc.)
// into the string shape the search helpers expect. The three queries
// here only project text columns, so the coercion stays simple.
func stringifyValue(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	default:
		return fmt.Sprintf("%v", t)
	}
}

// ----- helpers -----

// parseLimit returns the request's `limit` parameter, defaulted to
// DefaultLimit and capped at MaxLimit (P0-A6 + AC-7). A non-numeric
// value is a 400; a value > MaxLimit is a 400 (the cap is hard).
func parseLimit(raw string) (int, error) {
	if raw == "" {
		return DefaultLimit, nil
	}
	var n int
	if _, err := fmt.Sscanf(raw, "%d", &n); err != nil {
		return 0, errors.New("limit must be an integer")
	}
	if n < 1 {
		return 0, errors.New("limit must be ≥ 1")
	}
	if n > MaxLimit {
		return 0, fmt.Errorf("limit must be ≤ %d", MaxLimit)
	}
	return n, nil
}

// parseTypes splits the optional `types` CSV. An empty value defaults
// to all three. Unknown type names trip a 400 (AC-7). The returned
// slice preserves the canonical sort order regardless of the order
// the caller supplied so the per-type query order — and therefore
// the partial_types order — is deterministic.
func parseTypes(raw string) ([]string, error) {
	if raw == "" {
		return append([]string{}, allTypes...), nil
	}
	known := map[string]bool{
		TypeControls: true,
		TypeRisks:    true,
		TypeEvidence: true,
		TypeAnchors:  true,
	}
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		t := strings.TrimSpace(part)
		if t == "" {
			continue
		}
		if !known[t] {
			return nil, fmt.Errorf("unknown type %q (allowed: anchors,controls,risks,evidence)", t)
		}
		seen[t] = true
	}
	if len(seen) == 0 {
		return append([]string{}, allTypes...), nil
	}
	out := make([]string, 0, len(seen))
	for _, t := range allTypes {
		if seen[t] {
			out = append(out, t)
		}
	}
	return out, nil
}

// tokenize splits q into lower-cased non-empty tokens. Used for the
// relevance score's denominator + matched-count.
func tokenize(q string) []string {
	fields := strings.FieldsFunc(strings.ToLower(q), func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == ',' || r == ';'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// relevance computes the lexical relevance score: matched tokens
// divided by total query tokens. A query of "iam access" against
// haystack "IAM access review for AWS" scores 1.0; against
// "encryption at rest" scores 0.0.
//
// The DB-side ILIKE already pre-filtered to "at least one token
// matched", so the floor is 1 / len(tokens). The ceiling is 1.0 (all
// tokens present).
//
// Token comparison is substring-based to mirror ILIKE's semantics —
// the haystack "encryption" matches the query token "encrypt".
func relevance(tokens []string, haystack string) float64 {
	if len(tokens) == 0 {
		return 0
	}
	hs := strings.ToLower(haystack)
	matched := 0
	for _, t := range tokens {
		if strings.Contains(hs, t) {
			matched++
		}
	}
	return float64(matched) / float64(len(tokens))
}

// snippet returns ≤ SnippetMaxLen runes of haystack centered on the
// first matched token in q. When q does not appear in the haystack
// (defensive — DB ILIKE matched, but the haystack concatenation
// could differ from the indexed column), the snippet is the haystack
// prefix.
//
// The rune-bounded measurement keeps the AC-3 contract honest in the
// face of multi-byte ellipsis (`…` is 3 bytes / 1 rune) — a strict
// byte cap would have the prefix path overshoot by 2 bytes.
func snippet(haystack, q string) string {
	if haystack == "" {
		return ""
	}
	hayRunes := []rune(haystack)
	if len(hayRunes) <= SnippetMaxLen {
		return haystack
	}
	lower := strings.ToLower(haystack)
	idx := strings.Index(lower, strings.ToLower(q))
	if idx < 0 {
		// Fall back to a prefix snippet when the literal q isn't a
		// substring (a multi-token query may have matched on
		// disjoint columns).
		return string(hayRunes[:SnippetMaxLen-1]) + "…"
	}
	// Convert the byte-index match to a rune-index for centering.
	matchRuneIdx := len([]rune(haystack[:idx]))
	half := SnippetMaxLen / 2
	start := matchRuneIdx - half
	if start < 0 {
		start = 0
	}
	end := start + SnippetMaxLen
	if end > len(hayRunes) {
		end = len(hayRunes)
		start = end - SnippetMaxLen
		if start < 0 {
			start = 0
		}
	}
	out := string(hayRunes[start:end])
	if start > 0 {
		// Drop one leading rune to make room for the leading ellipsis.
		out = "…" + string(hayRunes[start+1:end])
	}
	if end < len(hayRunes) {
		// Drop one trailing rune to make room for the trailing
		// ellipsis. `outRunes` reflects current state including the
		// optional leading ellipsis.
		outRunes := []rune(out)
		out = string(outRunes[:len(outRunes)-1]) + "…"
	}
	return out
}

// escapeLike escapes the three LIKE meta-characters (% _ \\) so user
// input cannot widen the match. The pattern wraps with `%...%` after
// escaping. Without this, a `q=50%` would match every row.
func escapeLike(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\\', '%', '_':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}
