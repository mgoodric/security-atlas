// Package vendor implements the slice-024 "vendor lite" module: a focused
// TPRM (third-party risk management) primitive sized for ~30–80 vendors at
// a security-product startup (canvas §1.4 + §10.1).
//
// Scope intentionally small: vendor entity, contract dates, DPA status,
// review cadence, criticality, last-review-date, owner, optional linked
// SOW URI, and a many-to-many tie to slice-017 scope_cells. Anti-criteria
// (per docs/issues/024-vendor-lite-module.md):
//
//   - NO questionnaire issuance (phase 2)
//   - NO trust-center scraping (phase 2)
//   - NO external IO — every URI/URL is stored as opaque text
//
// The store mirrors slice-017 patterns: per-request transaction, tenant
// GUC applied for RLS, sqlc-typed queries. Overdue calculation lives in
// SQL so the index can serve it and the cadence-to-interval mapping is
// in one place; the public API surfaces it as a derived boolean.
package vendor

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Criticality is the slice-024 risk band. Three buckets is enough for a
// 30–80-vendor portfolio.
type Criticality string

const (
	CriticalityLow    Criticality = "low"
	CriticalityMedium Criticality = "medium"
	CriticalityHigh   Criticality = "high"
)

// AllCriticalities is the public set; the API layer uses it to validate
// inbound filter strings without building a separate switch.
var AllCriticalities = []Criticality{CriticalityLow, CriticalityMedium, CriticalityHigh}

// ReviewCadence is the interval between vendor reviews.
type ReviewCadence string

const (
	CadenceMonthly   ReviewCadence = "monthly"
	CadenceQuarterly ReviewCadence = "quarterly"
	CadenceBiannual  ReviewCadence = "biannual"
	CadenceAnnual    ReviewCadence = "annual"
)

// AllCadences mirrors AllCriticalities for input validation.
var AllCadences = []ReviewCadence{CadenceMonthly, CadenceQuarterly, CadenceBiannual, CadenceAnnual}

// Duration returns the calendar interval for the cadence. Used by IsOverdue
// for the pure-Go (test-only) check; the SQL store has the equivalent CASE.
//
// The mapping uses simple day counts because Go's time.Time doesn't have a
// native "calendar month" type and a vendor review program is happy with
// "≈30 days" precision. The SQL store uses Postgres INTERVAL '1 month' /
// '3 months' / '6 months' / '1 year' for the actual production query;
// this Go mapping is only a fallback for clients that compute overdue in
// memory.
func (c ReviewCadence) Duration() time.Duration {
	switch c {
	case CadenceMonthly:
		return 30 * 24 * time.Hour
	case CadenceQuarterly:
		return 91 * 24 * time.Hour
	case CadenceBiannual:
		return 182 * 24 * time.Hour
	case CadenceAnnual:
		return 365 * 24 * time.Hour
	default:
		// Unknown cadence: treat as annual so the system fails closed
		// (eventually surfaces as overdue) rather than open (never).
		return 365 * 24 * time.Hour
	}
}

// Valid reports whether the cadence string matches a known enum value.
// Used at the HTTP boundary to reject typos before they reach Postgres.
func (c ReviewCadence) Valid() bool {
	for _, x := range AllCadences {
		if x == c {
			return true
		}
	}
	return false
}

// Valid reports whether the criticality string matches a known enum value.
func (c Criticality) Valid() bool {
	for _, x := range AllCriticalities {
		if x == c {
			return true
		}
	}
	return false
}

// Vendor is the public API surface for a row in the vendors table. The
// store hydrates it from sqlc-generated dbx.Vendor; the HTTP layer
// serialises it to JSON.
//
// Date fields are exposed as *time.Time so callers can distinguish
// "unset" from "epoch". UUID identifiers are unwrapped from pgtype.UUID.
type Vendor struct {
	ID             uuid.UUID
	TenantID       uuid.UUID
	Name           string
	Domain         *string
	Criticality    Criticality
	ContractStart  *time.Time
	ContractEnd    *time.Time
	DPASigned      bool
	DPASignedAt    *time.Time
	ReviewCadence  ReviewCadence
	LastReviewDate *time.Time
	OwnerUser      string
	LinkedSOWURI   *string
	Notes          string
	ScopeCellIDs   []uuid.UUID
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// IsOverdueAsOf computes the derived overdue flag without a database round
// trip. The store always populates this from the SQL query (where it
// composes with the indexed cadence math), but a pure-Go path keeps the
// API handler from re-fetching when it already has the row.
//
// Contract:
//   - NULL last_review_date  => overdue (never reviewed counts as overdue)
//   - otherwise: last_review + cadence < cutoff
func (v Vendor) IsOverdueAsOf(cutoff time.Time) bool {
	if v.LastReviewDate == nil {
		return true
	}
	due := v.LastReviewDate.Add(v.ReviewCadence.Duration())
	return due.Before(cutoff)
}

// CreateVendorInput is the payload the API/store accept for POST /v1/vendors.
// All optional fields use pointers so "absent" and "empty" stay distinct.
type CreateVendorInput struct {
	Name           string
	Domain         *string
	Criticality    Criticality
	ContractStart  *time.Time
	ContractEnd    *time.Time
	DPASigned      bool
	DPASignedAt    *time.Time
	ReviewCadence  ReviewCadence
	LastReviewDate *time.Time
	OwnerUser      string
	LinkedSOWURI   *string
	Notes          string
	ScopeCellIDs   []uuid.UUID
}

// UpdateVendorInput mirrors CreateVendorInput. UPDATE in slice 024 is a
// full-row replace; partial-update merging lands in phase 2 if needed.
type UpdateVendorInput = CreateVendorInput

// BurndownBand is one bucket in the AC-3 burndown report.
type BurndownBand struct {
	Criticality    Criticality
	Total          int64
	Overdue        int64
	OnTimeFraction float64 // (Total - Overdue) / Total; 1.0 when Total = 0
}

// Burndown is the response shape for GET /v1/vendors/burndown. The total +
// per-band breakdown together drive the dashboard panel (slice 040) and
// the quarterly board pack (slice 032).
type Burndown struct {
	AsOf  time.Time
	Bands []BurndownBand
	Total BurndownBand // criticality=="" sentinel; aggregates every row
}

// ErrVendorNotFound is returned when the requested vendor does not exist
// for the active tenant (RLS-isolated). 404-shaped.
var ErrVendorNotFound = errors.New("vendor: not found")

// ErrInvalidInput is returned when CreateVendor/UpdateVendor receive a
// payload that violates a non-DB-enforced rule (e.g., bad enum value,
// dpa_signed without dpa_signed_at). 400-shaped.
var ErrInvalidInput = errors.New("vendor: invalid input")

// ErrDuplicateDomain is returned when a vendor with the same (tenant_id,
// lower(domain)) already exists. 409-shaped.
var ErrDuplicateDomain = errors.New("vendor: duplicate domain")
