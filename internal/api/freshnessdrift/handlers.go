// Package freshnessdrift serves the slice-016 evidence-freshness and
// control-drift HTTP API. Routes (appended onto the platform root router by
// httpserver.go):
//
//	GET /v1/evidence/freshness?bucket=class   AC-1: freshness distribution
//	GET /v1/controls/drift?since=7d           AC-3: pass->fail drift report
//
// Both endpoints are pure reads over the slice-016 read-model tables — a GET
// never triggers a refresh (the freshnessdrift Scheduler + RefreshSubscriber
// own that). The handlers run with the tenant set by upstream auth
// middleware; the freshness.Store / drift.Store open their own per-call
// transaction and apply the tenant GUC so RLS is enforced.
package freshnessdrift

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/drift"
	"github.com/mgoodric/security-atlas/internal/freshness"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Handler bundles the slice-016 read routes over the freshness + drift Stores.
type Handler struct {
	freshness *freshness.Store
	drift     *drift.Store
}

// New constructs a Handler.
func New(freshnessStore *freshness.Store, driftStore *drift.Store) *Handler {
	return &Handler{freshness: freshnessStore, drift: driftStore}
}

// defaultDriftWindow is the ?since= default — 7 days, matching the dashboard
// mockup's "Recent drift - last 7 days" panel.
const defaultDriftWindow = 7 * 24 * time.Hour

// maxDriftWindow caps ?since= so a pathological ?since=99999d cannot ask the
// read model for an unbounded scan. 400 days mirrors the longest freshness
// class (annual) — a year-plus of drift history is the practical ceiling.
const maxDriftWindow = 400 * 24 * time.Hour

// ----- freshness wire shapes -----

// freshnessClassBucket is one row of the AC-1 by-class distribution.
type freshnessClassBucket struct {
	FreshnessClass string `json:"freshness_class"`
	Total          int    `json:"total"`
	Fresh          int    `json:"fresh"`
	Stale          int    `json:"stale"`
}

// Freshness handles GET /v1/evidence/freshness.
//
// Query params:
//   - ?bucket=class  the only supported bucketing in v1 — groups the
//     freshness read model by freshness_class and reports total / fresh /
//     stale counts per class. Any other ?bucket= value is rejected 400.
//     Omitting ?bucket= returns the same class distribution (class is the
//     default and only bucket).
//
// The AC-1 / AC-2 contract: the response carries the per-class distribution
// with stale counts. Stale controls are FLAGGED (counted) here — they are
// never deleted from the evidence ledger (AC-6).
func (h *Handler) Freshness(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	if bucket := r.URL.Query().Get("bucket"); bucket != "" && bucket != "class" {
		writeError(w, http.StatusBadRequest, "bucket must be 'class' (the only supported bucketing)")
		return
	}

	rows, err := h.freshness.List(ctx)
	if err != nil {
		httperr.WriteInternal(w, r, "freshnessdrift", err)
		return
	}

	// Bucket by freshness_class. A control with no declared class buckets
	// under the literal "unclassified" key so it is still visible in the
	// distribution rather than silently dropped.
	type agg struct{ total, fresh, stale int }
	byClass := make(map[string]*agg)
	order := make([]string, 0)
	totalStale := 0
	for _, cf := range rows {
		key := cf.FreshnessClass
		if key == "" {
			key = "unclassified"
		}
		a, ok := byClass[key]
		if !ok {
			a = &agg{}
			byClass[key] = a
			order = append(order, key)
		}
		a.total++
		if cf.IsStale {
			a.stale++
			totalStale++
		} else {
			a.fresh++
		}
	}

	buckets := make([]freshnessClassBucket, 0, len(order))
	for _, key := range order {
		a := byClass[key]
		buckets = append(buckets, freshnessClassBucket{
			FreshnessClass: key,
			Total:          a.total,
			Fresh:          a.fresh,
			Stale:          a.stale,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"bucket":      "class",
		"buckets":     buckets,
		"total":       len(rows),
		"total_stale": totalStale,
	})
}

// ----- drift wire shapes -----

// driftRowWire is one flipped-out-of-passing control in the AC-3 response.
type driftRowWire struct {
	ControlID     string `json:"control_id"`
	LastPassing   string `json:"last_passing"`
	CurrentResult string `json:"current_result"`
}

// Drift handles GET /v1/controls/drift.
//
// Query params:
//   - ?since=Nd  the lookback window, in days (e.g. 7d). Defaults to 7d.
//     Capped at 400d. A malformed value is rejected 400.
//
// The AC-3 contract: the response carries the signed drift delta over the
// window plus the controls that flipped OUT of passing, each with its
// last-passing date and current (no-longer-passing) result.
func (h *Handler) Drift(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}

	window := defaultDriftWindow
	if raw := r.URL.Query().Get("since"); raw != "" {
		parsed, perr := parseSinceDays(raw)
		if perr != nil {
			writeError(w, http.StatusBadRequest, "since must be of the form 'Nd' (days), e.g. 7d")
			return
		}
		window = parsed
		if window > maxDriftWindow {
			window = maxDriftWindow
		}
	}

	report, err := h.drift.Report(ctx, window)
	if err != nil {
		httperr.WriteInternal(w, r, "freshnessdrift", err)
		return
	}

	flips := make([]driftRowWire, 0, len(report.FlippedToOut))
	for _, fr := range report.FlippedToOut {
		flips = append(flips, driftRowWire{
			ControlID:     fr.ControlID.String(),
			LastPassing:   fr.LastPassing.UTC().Format("2006-01-02"),
			CurrentResult: fr.CurrentResult,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"since":             report.SinceDate.UTC().Format("2006-01-02"),
		"through":           report.ThroughDate.UTC().Format("2006-01-02"),
		"delta":             report.Delta,
		"flipped_out_count": len(flips),
		"flipped_out":       flips,
	})
}

// parseSinceDays parses a "Nd" duration (days only) into a time.Duration.
// Days is the only supported unit — drift is a day-over-day signal, so a
// sub-day window is meaningless. Returns an error for any other shape. Uses
// strconv.ParseInt (not Atoi+cast) so the parsed value is a bounded int.
func parseSinceDays(raw string) (time.Duration, error) {
	s := strings.TrimSpace(raw)
	if !strings.HasSuffix(s, "d") {
		return 0, errBadSince
	}
	numPart := strings.TrimSuffix(s, "d")
	days, err := strconv.ParseInt(numPart, 10, 32)
	if err != nil {
		return 0, errBadSince
	}
	if days < 1 {
		return 0, errBadSince
	}
	return time.Duration(days) * 24 * time.Hour, nil
}

// ----- helpers -----

// errBadSince is the sentinel for a malformed ?since= value.
var errBadSince = badSinceError{}

type badSinceError struct{}

func (badSinceError) Error() string { return "since must be of the form 'Nd' (days)" }

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
