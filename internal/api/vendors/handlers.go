// Package vendors serves the slice-024 HTTP API for the vendor lite module.
// Routes (all auth-gated by the platform's bearer middleware, tenant resolved
// from the credential):
//
//	POST   /v1/vendors                  create a vendor
//	GET    /v1/vendors                  list (filter by criticality / overdue)
//	GET    /v1/vendors/{id}             read one
//	PATCH  /v1/vendors/{id}             full-row replace (lite — no merge)
//	DELETE /v1/vendors/{id}             remove (CASCADE clears scope cells)
//	GET    /v1/vendors/burndown         review-on-time fractions per band
//
// PATCH is semantically a PUT-shaped replace here — the AC says "create/edit
// form" without nailing down RFC 7396 semantics, so we keep it lite. A real
// JSON-merge-patch surface can land in phase 2 once update conflicts matter.
package vendors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/tenancy"
	"github.com/mgoodric/security-atlas/internal/vendor"
)

// Handler bundles the slice-024 routes. The middleware mounts the credential
// into context; we resolve the tenant id from there into the tenancy context
// the store understands.
type Handler struct {
	store *vendor.Store
	now   func() time.Time // injected for tests; defaults to time.Now in production
}

// New constructs a Handler over a vendor.Store. now defaults to time.Now;
// tests can override via NewWithClock.
func New(store *vendor.Store) *Handler {
	return &Handler{store: store, now: time.Now}
}

// NewWithClock is identical to New but lets tests pin "now" so cutoff math
// stays deterministic.
func NewWithClock(store *vendor.Store, now func() time.Time) *Handler {
	return &Handler{store: store, now: now}
}

// ----- wire types -----

type vendorWire struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Domain         *string   `json:"domain,omitempty"`
	Criticality    string    `json:"criticality"`
	ContractStart  *string   `json:"contract_start,omitempty"`
	ContractEnd    *string   `json:"contract_end,omitempty"`
	DPASigned      bool      `json:"dpa_signed"`
	DPASignedAt    *string   `json:"dpa_signed_at,omitempty"`
	ReviewCadence  string    `json:"review_cadence"`
	LastReviewDate *string   `json:"last_review_date,omitempty"`
	Overdue        bool      `json:"overdue"`
	OwnerUser      string    `json:"owner_user"`
	LinkedSOWURI   *string   `json:"linked_sow_uri,omitempty"`
	Notes          string    `json:"notes"`
	ScopeCellIDs   []string  `json:"scope_cell_ids"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type writeReq struct {
	Name           string   `json:"name"`
	Domain         *string  `json:"domain"`
	Criticality    string   `json:"criticality"`
	ContractStart  *string  `json:"contract_start"`
	ContractEnd    *string  `json:"contract_end"`
	DPASigned      bool     `json:"dpa_signed"`
	DPASignedAt    *string  `json:"dpa_signed_at"`
	ReviewCadence  string   `json:"review_cadence"`
	LastReviewDate *string  `json:"last_review_date"`
	OwnerUser      string   `json:"owner_user"`
	LinkedSOWURI   *string  `json:"linked_sow_uri"`
	Notes          string   `json:"notes"`
	ScopeCellIDs   []string `json:"scope_cell_ids"`
}

// reviewWire is the JSON shape for one row in the vendor_reviews ledger
// (slice 688). reviewed_at is date-granular (YYYY-MM-DD); created_at is the
// immutable insert timestamp.
type reviewWire struct {
	ID         string    `json:"id"`
	VendorID   string    `json:"vendor_id"`
	ReviewedAt string    `json:"reviewed_at"`
	Reviewer   string    `json:"reviewer"`
	Outcome    string    `json:"outcome"`
	Notes      string    `json:"notes"`
	CreatedAt  time.Time `json:"created_at"`
}

// recordReviewReq is the POST body for recording a completed review.
type recordReviewReq struct {
	ReviewedAt string `json:"reviewed_at"`
	Reviewer   string `json:"reviewer"`
	Outcome    string `json:"outcome"`
	Notes      string `json:"notes"`
}

type burndownBandWire struct {
	Criticality    string  `json:"criticality"`
	Total          int64   `json:"total"`
	Overdue        int64   `json:"overdue"`
	OnTimeFraction float64 `json:"on_time_fraction"`
}

type burndownWire struct {
	AsOf  time.Time          `json:"as_of"`
	Bands []burndownBandWire `json:"bands"`
	Total burndownBandWire   `json:"total"`
}

// ----- handlers -----

// CreateVendor handles POST /v1/vendors.
func (h *Handler) CreateVendor(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	in, herr := decodeWrite(r)
	if herr != nil {
		httpresp.WriteError(w, herr.status, herr.msg)
		return
	}
	v, err := h.store.Create(ctx, in)
	if err != nil {
		h.writeStoreErr(w, r, "create vendor", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusCreated, map[string]any{"vendor": h.toWire(v)})
}

// ListVendors handles GET /v1/vendors. Query params:
//
//	?criticality=high|medium|low   filter
//	?overdue=true                  overdue-only
//	?as_of=2026-05-11              cutoff for overdue (defaults to now)
func (h *Handler) ListVendors(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	f := vendor.ListFilter{}
	if c := strings.TrimSpace(r.URL.Query().Get("criticality")); c != "" {
		crit := vendor.Criticality(c)
		if !crit.Valid() {
			httpresp.WriteError(w, http.StatusBadRequest, "criticality must be low|medium|high")
			return
		}
		f.Criticality = &crit
	}
	if r.URL.Query().Get("overdue") == "true" {
		f.OverdueOnly = true
		if v := strings.TrimSpace(r.URL.Query().Get("as_of")); v != "" {
			t, err := time.Parse("2006-01-02", v)
			if err != nil {
				httpresp.WriteError(w, http.StatusBadRequest, "as_of must be YYYY-MM-DD")
				return
			}
			f.Cutoff = t
		} else {
			f.Cutoff = h.now()
		}
	}
	rows, err := h.store.List(ctx, f)
	if err != nil {
		h.writeStoreErr(w, r, "list vendors", err)
		return
	}
	out := make([]vendorWire, 0, len(rows))
	for _, v := range rows {
		out = append(out, h.toWire(v))
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"vendors": out})
}

// GetVendor handles GET /v1/vendors/{id}.
func (h *Handler) GetVendor(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	v, err := h.store.Get(ctx, id)
	if err != nil {
		h.writeStoreErr(w, r, "get vendor", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"vendor": h.toWire(v)})
}

// UpdateVendor handles PATCH /v1/vendors/{id}. Replace semantics; see package
// doc comment.
func (h *Handler) UpdateVendor(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	in, herr := decodeWrite(r)
	if herr != nil {
		httpresp.WriteError(w, herr.status, herr.msg)
		return
	}
	v, err := h.store.Update(ctx, id, in)
	if err != nil {
		h.writeStoreErr(w, r, "update vendor", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"vendor": h.toWire(v)})
}

// DeleteVendor handles DELETE /v1/vendors/{id}.
func (h *Handler) DeleteVendor(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	if err := h.store.Delete(ctx, id); err != nil {
		h.writeStoreErr(w, r, "delete vendor", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListReviews handles GET /v1/vendors/{id}/reviews — the vendor's review
// history, newest-first (slice 688 AC-3). RLS scopes the read to the active
// tenant; a cross-tenant or fabricated id yields an empty series.
func (h *Handler) ListReviews(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	rows, err := h.store.ListReviews(ctx, id)
	if err != nil {
		h.writeStoreErr(w, r, "list vendor reviews", err)
		return
	}
	out := make([]reviewWire, 0, len(rows))
	for _, rv := range rows {
		out = append(out, toReviewWire(rv))
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"reviews": out})
}

// RecordReview handles POST /v1/vendors/{id}/reviews — append a completed
// review to the ledger (slice 688 AC-5). The store also keeps the vendor's
// last_review_date scalar consistent with the most-recent ledger row.
func (h *Handler) RecordReview(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	var req recordReviewReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	reviewedAt, derr := parseOptDate(&req.ReviewedAt)
	if derr != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "reviewed_at: "+derr.Error())
		return
	}
	if reviewedAt == nil {
		httpresp.WriteError(w, http.StatusBadRequest, "reviewed_at is required (YYYY-MM-DD)")
		return
	}
	in := vendor.RecordReviewInput{
		VendorID:   id,
		ReviewedAt: *reviewedAt,
		Reviewer:   req.Reviewer,
		Outcome:    vendor.ReviewOutcome(strings.TrimSpace(req.Outcome)),
		Notes:      req.Notes,
	}
	rv, err := h.store.RecordReview(ctx, in)
	if err != nil {
		h.writeStoreErr(w, r, "record vendor review", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusCreated, map[string]any{"review": toReviewWire(rv)})
}

// Burndown handles GET /v1/vendors/burndown?criticality=high&as_of=YYYY-MM-DD.
func (h *Handler) Burndown(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	var crit *vendor.Criticality
	if c := strings.TrimSpace(r.URL.Query().Get("criticality")); c != "" {
		v := vendor.Criticality(c)
		if !v.Valid() {
			httpresp.WriteError(w, http.StatusBadRequest, "criticality must be low|medium|high")
			return
		}
		crit = &v
	}
	asOf := h.now()
	if v := strings.TrimSpace(r.URL.Query().Get("as_of")); v != "" {
		t, err := time.Parse("2006-01-02", v)
		if err != nil {
			httpresp.WriteError(w, http.StatusBadRequest, "as_of must be YYYY-MM-DD")
			return
		}
		asOf = t
	}
	bd, err := h.store.Burndown(ctx, asOf, crit)
	if err != nil {
		h.writeStoreErr(w, r, "burndown", err)
		return
	}
	out := burndownWire{
		AsOf:  bd.AsOf,
		Bands: make([]burndownBandWire, 0, len(bd.Bands)),
		Total: burndownBandWire{
			Criticality:    "",
			Total:          bd.Total.Total,
			Overdue:        bd.Total.Overdue,
			OnTimeFraction: bd.Total.OnTimeFraction,
		},
	}
	for _, b := range bd.Bands {
		out.Bands = append(out.Bands, burndownBandWire{
			Criticality:    string(b.Criticality),
			Total:          b.Total,
			Overdue:        b.Overdue,
			OnTimeFraction: b.OnTimeFraction,
		})
	}
	httpresp.WriteJSON(w, http.StatusOK, out)
}

// ----- helpers -----

type httpErr struct {
	status int
	msg    string
}

func decodeWrite(r *http.Request) (vendor.CreateVendorInput, *httpErr) {
	var req writeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return vendor.CreateVendorInput{}, &httpErr{http.StatusBadRequest, "invalid JSON body"}
	}
	in := vendor.CreateVendorInput{
		Name:          req.Name,
		Domain:        req.Domain,
		Criticality:   vendor.Criticality(req.Criticality),
		DPASigned:     req.DPASigned,
		ReviewCadence: vendor.ReviewCadence(req.ReviewCadence),
		OwnerUser:     req.OwnerUser,
		LinkedSOWURI:  req.LinkedSOWURI,
		Notes:         req.Notes,
	}
	var err error
	if in.ContractStart, err = parseOptDate(req.ContractStart); err != nil {
		return vendor.CreateVendorInput{}, &httpErr{http.StatusBadRequest, "contract_start: " + err.Error()}
	}
	if in.ContractEnd, err = parseOptDate(req.ContractEnd); err != nil {
		return vendor.CreateVendorInput{}, &httpErr{http.StatusBadRequest, "contract_end: " + err.Error()}
	}
	if in.DPASignedAt, err = parseOptDate(req.DPASignedAt); err != nil {
		return vendor.CreateVendorInput{}, &httpErr{http.StatusBadRequest, "dpa_signed_at: " + err.Error()}
	}
	if in.LastReviewDate, err = parseOptDate(req.LastReviewDate); err != nil {
		return vendor.CreateVendorInput{}, &httpErr{http.StatusBadRequest, "last_review_date: " + err.Error()}
	}
	for _, s := range req.ScopeCellIDs {
		id, err := uuid.Parse(s)
		if err != nil {
			return vendor.CreateVendorInput{}, &httpErr{http.StatusBadRequest, "scope_cell_ids: " + s + " is not a UUID"}
		}
		in.ScopeCellIDs = append(in.ScopeCellIDs, id)
	}
	return in, nil
}

func parseOptDate(s *string) (*time.Time, error) {
	if s == nil {
		return nil, nil
	}
	v := strings.TrimSpace(*s)
	if v == "" {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02", v)
	if err != nil {
		return nil, fmt.Errorf("must be YYYY-MM-DD")
	}
	return &t, nil
}

func (h *Handler) toWire(v vendor.Vendor) vendorWire {
	cellIDs := make([]string, 0, len(v.ScopeCellIDs))
	for _, id := range v.ScopeCellIDs {
		cellIDs = append(cellIDs, id.String())
	}
	w := vendorWire{
		ID:            v.ID.String(),
		Name:          v.Name,
		Domain:        v.Domain,
		Criticality:   string(v.Criticality),
		DPASigned:     v.DPASigned,
		ReviewCadence: string(v.ReviewCadence),
		OwnerUser:     v.OwnerUser,
		LinkedSOWURI:  v.LinkedSOWURI,
		Notes:         v.Notes,
		ScopeCellIDs:  cellIDs,
		CreatedAt:     v.CreatedAt,
		UpdatedAt:     v.UpdatedAt,
		Overdue:       v.IsOverdueAsOf(h.now()),
	}
	w.ContractStart = dateString(v.ContractStart)
	w.ContractEnd = dateString(v.ContractEnd)
	w.DPASignedAt = dateString(v.DPASignedAt)
	w.LastReviewDate = dateString(v.LastReviewDate)
	return w
}

func toReviewWire(rv vendor.Review) reviewWire {
	return reviewWire{
		ID:         rv.ID.String(),
		VendorID:   rv.VendorID.String(),
		ReviewedAt: rv.ReviewedAt.Format("2006-01-02"),
		Reviewer:   rv.Reviewer,
		Outcome:    string(rv.Outcome),
		Notes:      rv.Notes,
		CreatedAt:  rv.CreatedAt,
	}
}

func dateString(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format("2006-01-02")
	return &s
}

func (h *Handler) tenantContext(r *http.Request) (context.Context, bool) {
	// Slice 033: tenancy.Middleware (httpserver.go) lifted cred.TenantID
	// onto r.Context() via tenancy.WithTenant. Confirm the tenant is set
	// — its absence means no credential (the 401-shaped path).
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		return nil, false
	}
	return r.Context(), true
}

func (h *Handler) writeStoreErr(w http.ResponseWriter, r *http.Request, op string, err error) {
	switch {
	case errors.Is(err, vendor.ErrVendorNotFound):
		httpresp.WriteError(w, http.StatusNotFound, "vendor not found")
	case errors.Is(err, vendor.ErrInvalidInput):
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, vendor.ErrDuplicateDomain):
		httpresp.WriteError(w, http.StatusConflict, "a vendor with this domain already exists")
	default:
		httperr.WriteInternal(w, r, op, err)
	}
}
