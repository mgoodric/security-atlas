// Slice 192: GET /v1/me/tenants — multi-tenant directory.
//
// Returns the caller's set of available tenants, enriched with name
// metadata. The list is sourced from the caller's verified JWT's
// `atlas:available_tenants[]` claim — NEVER from a full SELECT on
// the `tenants` table (P0-192-2).
//
// CONSTITUTIONAL INVARIANTS HONORED:
//
//   - P0-192-1 / canvas §11 #13: this handler returns honest data;
//     the frontend tenant-switcher HIDES itself when the response's
//     `tenants` array has length 1.
//   - P0-192-2: the tenant list is bounded to the JWT claim's
//     available_tenants[] — no full table scan.
//   - P0-192-9: no per-tenant URL routing; the response carries
//     tenant_id values, not slugs.
//   - P0-192-10: does NOT modify slice 187's keystore/tokensign/jwt
//     packages — only consumes the verified claim via jwtmw.
//
// CACHE DURATION (D1): the BFF + frontend may cache this response
// for 60 seconds; the membership-removed UX banner relies on the
// periodic re-fetch firing at that cadence. Operators who need
// faster eviction can call /oauth/revoke (slice 190 surface) per
// the eviction-is-eventual contract (P0-192-8).
package me

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
)

// TenantsHandler serves GET /v1/me/tenants.
//
// Two pools are accepted:
//
//   - authPool: BYPASSRLS pool (atlas_migrate) — used to fetch
//     tenant.name for tenants in the JWT claim. The query is
//     bounded to the JWT's available_tenants[] (an int-bounded set,
//     usually single-digit per operator).
//
// The handler is constructed by main.go; an in-memory test harness
// without DATABASE_URL gets a nil authPool — the handler returns the
// JWT's tenant IDs without name enrichment (graceful fallback).
type TenantsHandler struct {
	authPool *pgxpool.Pool
}

// NewTenants constructs a TenantsHandler. authPool MAY be nil — in
// that case names are not enriched and the response carries the
// JWT's tenant IDs only (test-harness path).
func NewTenants(authPool *pgxpool.Pool) *TenantsHandler {
	return &TenantsHandler{authPool: authPool}
}

// tenantWire is the JSON shape of one row in the response.
type tenantWire struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Current bool   `json:"current"`
}

// listTenantsResponse is the top-level response envelope.
type listTenantsResponse struct {
	Tenants []tenantWire `json:"tenants"`
}

// ListTenants handles GET /v1/me/tenants.
//
// Reads the caller's verified JWT claims via jwtmw.FromContext.
// Loads tenant metadata (name) for the JWT's available_tenants[].
// Marks the current tenant via the JWT's current_tenant_id.
//
// Response shape:
//
//	{
//	  "tenants": [
//	    {"id": "<uuid>", "name": "<str>", "current": true},
//	    {"id": "<uuid>", "name": "<str>", "current": false},
//	    ...
//	  ]
//	}
//
// When the JWT claim's available_tenants is empty:
// returns `{"tenants":[]}`. The frontend HIDES the switcher in
// that case (P0-192-1 — single-tenant chrome rule applies to the
// zero-tenant edge too).
//
// When the JWT is missing (caller not authenticated via the JWT
// middleware): returns 401.
func (h *TenantsHandler) ListTenants(w http.ResponseWriter, r *http.Request) {
	claims := jwtmw.FromContext(r.Context())
	if claims == nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "no jwt context")
		return
	}

	out := listTenantsResponse{Tenants: []tenantWire{}}

	// Empty available_tenants → empty list. Honest output; frontend
	// hides the switcher chrome per canvas §11 #13.
	if len(claims.AvailableTenants) == 0 {
		httpresp.WriteJSON(w, http.StatusOK, out)
		return
	}

	names, err := h.loadTenantNames(r.Context(), claims.AvailableTenants)
	if err != nil {
		httperr.WriteInternal(w, r, "list tenants", err)
		return
	}

	for _, t := range claims.AvailableTenants {
		name, ok := names[t]
		if !ok {
			// Tenant in the claim but not in the names map — the
			// authPool may have been nil OR the tenant row was
			// deleted between claim issuance and this request. Use a
			// placeholder name so the frontend can still render +
			// surface the eventual-eviction semantic (P0-192-8). The
			// banner UX will catch up on the next re-fetch.
			name = ""
		}
		out.Tenants = append(out.Tenants, tenantWire{
			ID:      t.String(),
			Name:    name,
			Current: t == claims.CurrentTenantID,
		})
	}
	httpresp.WriteJSON(w, http.StatusOK, out)
}

// loadTenantNames batch-fetches tenant names by id from the BYPASSRLS
// pool. The query is bounded by the input list — PK lookups only,
// no scans (P0-192-2).
//
// Returns an empty map when authPool is nil (test-harness path). The
// caller treats missing names as empty string (the frontend has a
// fallback for it).
func (h *TenantsHandler) loadTenantNames(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	out := make(map[uuid.UUID]string, len(ids))
	if h.authPool == nil || len(ids) == 0 {
		return out, nil
	}

	// pgx supports passing a uuid slice via the array binding. The
	// `= ANY($1)` form is the safe PK-bounded query that maps to an
	// index-only scan on tenants(id) (the table's PK index).
	uuidArgs := make([]string, 0, len(ids))
	for _, id := range ids {
		uuidArgs = append(uuidArgs, id.String())
	}

	rows, err := h.authPool.Query(ctx,
		`SELECT id, name FROM tenants WHERE id = ANY($1::uuid[])`,
		uuidArgs,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return out, nil
		}
		return nil, fmt.Errorf("query tenants: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			id   uuid.UUID
			name string
		)
		if err := rows.Scan(&id, &name); err != nil {
			return nil, fmt.Errorf("scan tenant row: %w", err)
		}
		out[id] = name
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows err: %w", err)
	}
	return out, nil
}

// (writeJSON / writeError / writeServerErr live in audit_period.go)
