// Package metrics serves the slice-076 HTTP API for the metrics catalog,
// cascade, observation series, target settings, and manual-input writes.
//
// Routes (mounted onto the root chi router by internal/api/httpserver.go):
//
//	GET    /v1/metrics                          list catalog (optional level/category filter)
//	GET    /v1/metrics/cascade                  recursive cascade descent
//	GET    /v1/metrics/{id}                     one metric + parents + children
//	GET    /v1/metrics/{id}/observations        observations series
//	POST   /v1/metrics/{id}/inputs              append a manual input (audit-trail)
//	GET    /v1/metrics/{id}/target              read tenant's target
//	PUT    /v1/metrics/{id}/target              upsert tenant's target
//
// All routes require an authenticated tenant context. The catalog +
// cascade reads are platform-shared (visible to every tenant). The
// observation / input / target reads + writes are RLS-bound to the
// caller's tenant.
//
// AC-11: POST /v1/metrics/{id}/inputs requires the existing `admin`
// role (the slice-076 `metric_admin` role-extension is deferred to a
// follow-on slice, see decisions log D9).
package metrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// DefaultCascadeDepth caps the recursive-CTE walk in GetCascade. 3
// levels matches the spec (board → program → team). Callers can
// override via ?depth=N but the handler hard-caps at MaxCascadeDepth
// to prevent runaway content-bug traversal.
const DefaultCascadeDepth = 3

// MaxCascadeDepth is the hard cap. The DB query's recursive CTE
// terminates at depth_limit so even a content bug can't drag through
// more than this many levels.
const MaxCascadeDepth = 6

// DefaultObservationLimit is the page size for GET observations.
const DefaultObservationLimit = 200

// MaxObservationLimit caps the page size.
const MaxObservationLimit = 1000

// DefaultObservationWindow is how far back the GET observations call
// defaults to when ?since is omitted.
const DefaultObservationWindow = 90 * 24 * time.Hour

// Handler bundles every slice-076 route over the platform pool.
type Handler struct {
	pool *pgxpool.Pool
}

// New constructs a Handler.
func New(pool *pgxpool.Pool) *Handler {
	return &Handler{pool: pool}
}

// ===== wire shapes =====

type metricWire struct {
	ID               string   `json:"id"`
	Level            string   `json:"level"`
	Category         string   `json:"category"`
	Name             string   `json:"name"`
	Description      string   `json:"description"`
	Unit             string   `json:"unit"`
	Cadence          string   `json:"cadence"`
	ComputeStrategy  string   `json:"compute_strategy"`
	ComputeEvaluator string   `json:"compute_evaluator,omitempty"`
	SourceSlices     []string `json:"source_slices"`
	Notes            string   `json:"notes,omitempty"`
}

type metricDetailWire struct {
	Metric   metricWire   `json:"metric"`
	Parents  []metricWire `json:"parents"`
	Children []metricWire `json:"children"`
}

type cascadeNodeWire struct {
	MetricID string `json:"metric_id"`
	ParentID string `json:"parent_id,omitempty"`
	Depth    int32  `json:"depth"`
}

type observationWire struct {
	ID           string          `json:"id"`
	MetricID     string          `json:"metric_id"`
	ObservedAt   time.Time       `json:"observed_at"`
	NumericValue string          `json:"numeric_value"`
	Dimensions   json.RawMessage `json:"dimensions"`
	Source       string          `json:"source"`
	CreatedAt    time.Time       `json:"created_at"`
}

type observationsPageWire struct {
	Observations []observationWire `json:"observations"`
	Count        int               `json:"count"`
}

type inputCreateReq struct {
	NumericValue float64         `json:"numeric_value"`
	ObservedAt   *time.Time      `json:"observed_at,omitempty"`
	Dimensions   json.RawMessage `json:"dimensions,omitempty"`
	Notes        string          `json:"notes,omitempty"`
}

type inputWire struct {
	ID              string          `json:"id"`
	MetricID        string          `json:"metric_id"`
	InputAt         time.Time       `json:"input_at"`
	NumericValue    string          `json:"numeric_value"`
	Dimensions      json.RawMessage `json:"dimensions"`
	EnteredByUserID string          `json:"entered_by_user_id"`
	Notes           string          `json:"notes,omitempty"`
}

type targetWire struct {
	MetricID          string  `json:"metric_id"`
	TargetValue       *string `json:"target_value,omitempty"`
	WarningThreshold  *string `json:"warning_threshold,omitempty"`
	CriticalThreshold *string `json:"critical_threshold,omitempty"`
	Direction         string  `json:"direction"`
	OwnerUserID       string  `json:"owner_user_id,omitempty"`
	Notes             string  `json:"notes,omitempty"`
}

type targetUpsertReq struct {
	TargetValue       *float64 `json:"target_value,omitempty"`
	WarningThreshold  *float64 `json:"warning_threshold,omitempty"`
	CriticalThreshold *float64 `json:"critical_threshold,omitempty"`
	Direction         string   `json:"direction"`
	OwnerUserID       string   `json:"owner_user_id,omitempty"`
	Notes             string   `json:"notes,omitempty"`
}

// ===== ListCatalog (GET /v1/metrics) =====

// ListCatalog handles GET /v1/metrics?level=&category=. The catalog is
// platform-shared so the read does not require a tenant context; the
// upstream auth middleware enforces "authenticated".
func (h *Handler) ListCatalog(w http.ResponseWriter, r *http.Request) {
	level := r.URL.Query().Get("level")
	category := r.URL.Query().Get("category")
	params := dbx.ListMetricsCatalogParams{
		Level:    level,
		Category: category,
	}
	rows, err := dbx.New(h.pool).ListMetricsCatalog(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list catalog: "+err.Error())
		return
	}
	out := make([]metricWire, 0, len(rows))
	for _, row := range rows {
		out = append(out, metricWireFromRow(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"metrics": out,
		"count":   len(out),
	})
}

// ===== GetCatalog (GET /v1/metrics/{id}) =====

// GetCatalog handles GET /v1/metrics/{id}. Returns the metric definition
// plus its immediate parents and immediate children (one level only).
// Full cascade walk is GET /v1/metrics/cascade.
func (h *Handler) GetCatalog(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "id required")
		return
	}
	q := dbx.New(h.pool)
	row, err := q.GetMetricCatalog(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "metric not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "get catalog: "+err.Error())
		return
	}
	parents, err := q.ListMetricCatalogParents(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list parents: "+err.Error())
		return
	}
	children, err := q.ListMetricCatalogChildren(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list children: "+err.Error())
		return
	}
	out := metricDetailWire{
		Metric:   metricWireFromRow(row),
		Parents:  make([]metricWire, 0, len(parents)),
		Children: make([]metricWire, 0, len(children)),
	}
	for _, p := range parents {
		out.Parents = append(out.Parents, metricWireFromRow(p))
	}
	for _, c := range children {
		out.Children = append(out.Children, metricWireFromRow(c))
	}
	writeJSON(w, http.StatusOK, out)
}

// ===== GetCascade (GET /v1/metrics/cascade) =====

// GetCascade handles GET /v1/metrics/cascade?level=board&depth=N.
// Returns a flat list of (metric_id, parent_id, depth) the consumer
// reassembles into a tree. The depth cap prevents runaway traversal.
func (h *Handler) GetCascade(w http.ResponseWriter, r *http.Request) {
	level := r.URL.Query().Get("level")
	if level == "" {
		level = "board"
	}
	depth := DefaultCascadeDepth
	if raw := r.URL.Query().Get("depth"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			depth = n
		}
	}
	truncated := false
	if depth > MaxCascadeDepth {
		depth = MaxCascadeDepth
		truncated = true
	}
	rows, err := dbx.New(h.pool).GetMetricCascade(r.Context(), dbx.GetMetricCascadeParams{
		Level:      level,
		DepthLimit: int32(depth),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cascade: "+err.Error())
		return
	}
	out := make([]cascadeNodeWire, 0, len(rows))
	for _, row := range rows {
		parent := ""
		if row.ParentID != nil {
			parent = *row.ParentID
		}
		out = append(out, cascadeNodeWire{
			MetricID: row.MetricID,
			ParentID: parent,
			Depth:    row.Depth,
		})
	}
	if truncated {
		w.Header().Set("X-Cascade-Truncated", "true")
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"nodes":      out,
		"count":      len(out),
		"depth":      depth,
		"truncated":  truncated,
		"root_level": level,
	})
}

// ===== ListObservations (GET /v1/metrics/{id}/observations) =====

// ListObservations handles GET /v1/metrics/{id}/observations.
// since / until are ISO8601; defaults are now-90d to now. limit caps
// to MaxObservationLimit.
func (h *Handler) ListObservations(w http.ResponseWriter, r *http.Request) {
	ctx, _, ok := h.tenantContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "id required")
		return
	}
	since := time.Now().Add(-DefaultObservationWindow)
	until := time.Now()
	if raw := r.URL.Query().Get("since"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "since must be RFC3339")
			return
		}
		since = t
	}
	if raw := r.URL.Query().Get("until"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "until must be RFC3339")
			return
		}
		until = t
	}
	limit := DefaultObservationLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= MaxObservationLimit {
			limit = n
		}
	}
	var rows []dbx.MetricObservation
	err := h.runInTx(ctx, func(q *dbx.Queries) error {
		var inErr error
		rows, inErr = q.ListMetricObservations(ctx, dbx.ListMetricObservationsParams{
			MetricID: id,
			Since:    pgtype.Timestamptz{Time: since, Valid: true},
			Until:    pgtype.Timestamptz{Time: until, Valid: true},
			Limit:    int32(limit),
		})
		return inErr
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "observations: "+err.Error())
		return
	}
	page := observationsPageWire{
		Observations: make([]observationWire, 0, len(rows)),
	}
	for _, o := range rows {
		page.Observations = append(page.Observations, observationWireFromRow(o))
	}
	page.Count = len(page.Observations)
	writeJSON(w, http.StatusOK, page)
}

// ===== CreateInput (POST /v1/metrics/{id}/inputs) =====

// CreateInput handles POST /v1/metrics/{id}/inputs. Requires the admin
// role (the slice-076 `metric_admin` role-extension is deferred — see
// decisions log D9). The handler verifies the catalog row's
// compute_strategy = 'manual_input' before accepting the write.
func (h *Handler) CreateInput(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.tenantContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	if !cred.IsAdmin {
		writeError(w, http.StatusForbidden, "admin role required")
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "id required")
		return
	}
	var req inputCreateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	// Verify the catalog row exists and is manual_input.
	cat, err := dbx.New(h.pool).GetMetricCatalog(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "metric not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "lookup: "+err.Error())
		return
	}
	if cat.ComputeStrategy != "manual_input" && cat.ComputeStrategy != "external_integration" {
		writeError(w, http.StatusConflict,
			fmt.Sprintf("metric %s has compute_strategy=%q; manual input not permitted", id, cat.ComputeStrategy))
		return
	}
	enteredBy, err := uuid.Parse(cred.UserID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "credential user id is not a UUID")
		return
	}
	tenantUUID, err := uuid.Parse(cred.TenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "tenant id is not a UUID")
		return
	}
	when := time.Now().UTC()
	if req.ObservedAt != nil {
		when = req.ObservedAt.UTC()
	}
	dims := []byte("{}")
	if len(req.Dimensions) > 0 {
		dims = []byte(req.Dimensions)
	}
	var numeric pgtype.Numeric
	if err := numeric.Scan(fmt.Sprintf("%g", req.NumericValue)); err != nil {
		writeError(w, http.StatusBadRequest, "invalid numeric_value")
		return
	}
	var row dbx.MetricInput
	err = h.runInTx(ctx, func(q *dbx.Queries) error {
		var inErr error
		row, inErr = q.InsertMetricInput(ctx, dbx.InsertMetricInputParams{
			TenantID:        pgtype.UUID{Bytes: tenantUUID, Valid: true},
			MetricID:        id,
			InputAt:         pgtype.Timestamptz{Time: when, Valid: true},
			NumericValue:    numeric,
			Dimensions:      dims,
			EnteredByUserID: pgtype.UUID{Bytes: enteredBy, Valid: true},
			Notes:           req.Notes,
		})
		return inErr
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "insert input: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, inputWireFromRow(row))
}

// ===== Target read + upsert =====

// GetTarget handles GET /v1/metrics/{id}/target.
func (h *Handler) GetTarget(w http.ResponseWriter, r *http.Request) {
	ctx, _, ok := h.tenantContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "id required")
		return
	}
	var row dbx.MetricTarget
	err := h.runInTx(ctx, func(q *dbx.Queries) error {
		var inErr error
		row, inErr = q.GetMetricTarget(ctx, id)
		return inErr
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "no target set")
			return
		}
		writeError(w, http.StatusInternalServerError, "get target: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, targetWireFromRow(row))
}

// UpsertTarget handles PUT /v1/metrics/{id}/target.
func (h *Handler) UpsertTarget(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.tenantContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	if !cred.IsAdmin {
		writeError(w, http.StatusForbidden, "admin role required")
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "id required")
		return
	}
	var req targetUpsertReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	switch req.Direction {
	case "higher_is_better", "lower_is_better", "target_is_better":
	default:
		writeError(w, http.StatusBadRequest, "direction must be higher_is_better, lower_is_better, or target_is_better")
		return
	}
	tenantUUID, err := uuid.Parse(cred.TenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "tenant id is not a UUID")
		return
	}
	target := numericPtr(req.TargetValue)
	warn := numericPtr(req.WarningThreshold)
	crit := numericPtr(req.CriticalThreshold)
	var owner pgtype.UUID
	if req.OwnerUserID != "" {
		u, err := uuid.Parse(req.OwnerUserID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "owner_user_id must be a UUID")
			return
		}
		owner = pgtype.UUID{Bytes: u, Valid: true}
	}
	var row dbx.MetricTarget
	err = h.runInTx(ctx, func(q *dbx.Queries) error {
		var inErr error
		row, inErr = q.UpsertMetricTarget(ctx, dbx.UpsertMetricTargetParams{
			TenantID:          pgtype.UUID{Bytes: tenantUUID, Valid: true},
			MetricID:          id,
			TargetValue:       target,
			WarningThreshold:  warn,
			CriticalThreshold: crit,
			Direction:         req.Direction,
			OwnerUserID:       owner,
			Notes:             req.Notes,
		})
		return inErr
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "upsert target: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, targetWireFromRow(row))
}

// ===== helpers =====

func (h *Handler) tenantContext(r *http.Request) (context.Context, credstore.Credential, bool) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || cred.TenantID == "" {
		return nil, credstore.Credential{}, false
	}
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		return nil, credstore.Credential{}, false
	}
	return r.Context(), cred, true
}

// runInTx opens a tenant-bound transaction, applies the GUC, runs the
// callback through a dbx.Queries scoped to the tx, commits.
func (h *Handler) runInTx(ctx context.Context, fn func(*dbx.Queries) error) error {
	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return fmt.Errorf("apply tenant: %w", err)
	}
	if err := fn(dbx.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func numericPtr(f *float64) pgtype.Numeric {
	if f == nil {
		return pgtype.Numeric{}
	}
	var n pgtype.Numeric
	_ = n.Scan(fmt.Sprintf("%g", *f))
	return n
}

func metricWireFromRow(row dbx.MetricsCatalog) metricWire {
	out := metricWire{
		ID:              row.ID,
		Level:           row.Level,
		Category:        row.Category,
		Name:            row.Name,
		Description:     row.Description,
		Unit:            row.Unit,
		Cadence:         row.Cadence,
		ComputeStrategy: row.ComputeStrategy,
		SourceSlices:    append([]string{}, row.SourceSlices...),
		Notes:           row.Notes,
	}
	if row.ComputeEvaluator != nil {
		out.ComputeEvaluator = *row.ComputeEvaluator
	}
	return out
}

func observationWireFromRow(row dbx.MetricObservation) observationWire {
	dims := json.RawMessage(row.Dimensions)
	if len(dims) == 0 {
		dims = json.RawMessage("{}")
	}
	return observationWire{
		ID:           uuidString(row.ID),
		MetricID:     row.MetricID,
		ObservedAt:   row.ObservedAt.Time,
		NumericValue: numericString(row.NumericValue),
		Dimensions:   dims,
		Source:       row.Source,
		CreatedAt:    row.CreatedAt.Time,
	}
}

func inputWireFromRow(row dbx.MetricInput) inputWire {
	dims := json.RawMessage(row.Dimensions)
	if len(dims) == 0 {
		dims = json.RawMessage("{}")
	}
	return inputWire{
		ID:              uuidString(row.ID),
		MetricID:        row.MetricID,
		InputAt:         row.InputAt.Time,
		NumericValue:    numericString(row.NumericValue),
		Dimensions:      dims,
		EnteredByUserID: uuidString(row.EnteredByUserID),
		Notes:           row.Notes,
	}
}

func targetWireFromRow(row dbx.MetricTarget) targetWire {
	out := targetWire{
		MetricID:  row.MetricID,
		Direction: row.Direction,
		Notes:     row.Notes,
	}
	if v := numericStringMaybe(row.TargetValue); v != "" {
		out.TargetValue = &v
	}
	if v := numericStringMaybe(row.WarningThreshold); v != "" {
		out.WarningThreshold = &v
	}
	if v := numericStringMaybe(row.CriticalThreshold); v != "" {
		out.CriticalThreshold = &v
	}
	if row.OwnerUserID.Valid {
		out.OwnerUserID = uuid.UUID(row.OwnerUserID.Bytes).String()
	}
	return out
}

func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}

func numericString(n pgtype.Numeric) string {
	if !n.Valid {
		return "0"
	}
	v, err := n.Value()
	if err != nil || v == nil {
		return "0"
	}
	switch s := v.(type) {
	case string:
		return s
	default:
		return fmt.Sprintf("%v", v)
	}
}

func numericStringMaybe(n pgtype.Numeric) string {
	if !n.Valid {
		return ""
	}
	return numericString(n)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
