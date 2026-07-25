// Package worker is the shared normalization layer for the HRIS connector family
// (Rippling + BambooHR, slice 491). Both HRIS sources' worker records reduce to
// the same shape — a worker with a stable id, an employment status
// (active/terminated/etc.), a hire date, an optional end/termination date, a
// role/title, a department, an optional manager assignment, and (where the
// access-review join requires it) the work email — so they share one evidence
// kind (hris.worker_lifecycle.v1) and one normalizer.
//
// The load-bearing guard (P0-491-3 / threat-model I): the HRIS holds the MOST
// sensitive PII in the customer's stack. A RawWorker carries worker-lifecycle
// facts ONLY. It deliberately has NO field for SSN / national id, compensation /
// salary, home address, bank / payment details, benefits / health enrollment,
// performance-review fields, date of birth, personal phone, or
// gender / ethnicity / protected-class data. The type system itself is the first
// line of the over-collection defence: there is nowhere to put any of those
// fields. The vendor clients decode only the listed lifecycle fields at the API
// boundary, and the API requests scope the field set to the lifecycle minimum, so
// the excluded PII never enters memory as connector data. The cmd-layer test
// asserts no banned key/substring reaches an emitted record (AC-10).
package worker

import (
	"sort"
	"strings"
	"time"
)

// HRIS identifies the source system. Maps 1:1 to the schema's source_hris enum.
type HRIS string

const (
	// HRISRippling is the Rippling HRIS connector.
	HRISRippling HRIS = "rippling"
	// HRISBambooHR is the BambooHR HRIS connector.
	HRISBambooHR HRIS = "bamboohr"
)

// EmploymentStatus is the worker's normalized lifecycle state. Descriptive — the
// platform evaluator owns the access-review pass/fail per (control, scope); this
// is the HRIS's own assessment of where the worker sits in the
// joiner/mover/leaver lifecycle.
type EmploymentStatus string

const (
	// StatusActive is a current, employed worker.
	StatusActive EmploymentStatus = "active"
	// StatusTerminated is a worker whose employment has ended (the leaver signal
	// the deprovisioning + access-review controls hinge on).
	StatusTerminated EmploymentStatus = "terminated"
	// StatusOnLeave is a worker on leave (still employed; not a leaver).
	StatusOnLeave EmploymentStatus = "on_leave"
	// StatusPending is a future-dated joiner not yet started.
	StatusPending EmploymentStatus = "pending"
	// StatusUnknown is a worker whose status the source did not report (or
	// reported in a form we do not map). The evaluator treats unknown
	// conservatively.
	StatusUnknown EmploymentStatus = "unknown"
)

// RawWorker is the narrow, PII-bounded view a vendor client returns for one
// worker. The vendor clients map their API response into this shape, discarding
// SSN, compensation, home address, bank/payment, benefits/health,
// performance-review, date-of-birth, personal-phone, and protected-class fields
// at the decode boundary. Tests construct it directly.
//
// There is intentionally NO Ssn / Compensation / Salary / HomeAddress /
// BankAccount / Benefits / PerformanceRating / DateOfBirth / PersonalPhone /
// Gender / Ethnicity field on this struct (P0-491-3). A leak would be a compile
// error.
type RawWorker struct {
	// WorkerID is the HRIS-native stable identifier (opaque, non-secret) — the
	// stable key for the worker.
	WorkerID string
	// Status is the source's employment-lifecycle state. Empty falls back to
	// StatusUnknown.
	Status EmploymentStatus
	// StartDate is the worker's hire / start date (the joiner fact). Optional —
	// zero value is "not reported".
	StartDate time.Time
	// EndDate is the worker's end / termination date (the leaver fact). Optional —
	// zero value means no termination date (typically an active worker).
	EndDate time.Time
	// Title is the worker's role / job title (the mover fact). Descriptive,
	// non-sensitive. Optional.
	Title string
	// Department is the worker's department / team (the mover fact). Descriptive,
	// non-sensitive. Optional.
	Department string
	// ManagerAssignmentID is the OPAQUE worker id of this worker's manager — the
	// minimum identity needed to route an access review. NEVER the manager's
	// personal contact detail. Optional.
	ManagerAssignmentID string
	// WorkEmail is the worker's WORK email — the only contact field collected, and
	// only because the access-review join keys roster against IdP/app accounts by
	// work email. NEVER a personal email. Optional (stable key is WorkerID).
	WorkEmail string
}

// Worker is the normalized record the cmd layer turns into an evidence record.
// Field names map 1:1 to the hris.worker_lifecycle.v1 schema.
type Worker struct {
	SourceHRIS          HRIS
	WorkerID            string
	Status              EmploymentStatus
	StartDate           time.Time
	EndDate             time.Time
	Title               string
	Department          string
	ManagerAssignmentID string
	WorkEmail           string
	ObservedAt          time.Time
}

// Normalize converts a vendor's raw workers into normalized Workers, stamping the
// source HRIS + a single observed-at. now is injectable for deterministic tests
// (nil -> time.Now UTC). Workers missing an id are dropped (the schema requires
// it) rather than emitting an invalid record. An unset/unknown status normalizes
// to StatusUnknown.
func Normalize(hris HRIS, raw []RawWorker, now func() time.Time) []Worker {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	observedAt := now().UTC().Truncate(time.Hour)
	out := make([]Worker, 0, len(raw))
	for _, w := range raw {
		id := strings.TrimSpace(w.WorkerID)
		if id == "" {
			continue
		}
		out = append(out, Worker{
			SourceHRIS:          hris,
			WorkerID:            id,
			Status:              normalizeStatus(w.Status),
			StartDate:           w.StartDate.UTC(),
			EndDate:             w.EndDate.UTC(),
			Title:               strings.TrimSpace(w.Title),
			Department:          strings.TrimSpace(w.Department),
			ManagerAssignmentID: strings.TrimSpace(w.ManagerAssignmentID),
			WorkEmail:           strings.TrimSpace(w.WorkEmail),
			ObservedAt:          observedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].WorkerID < out[j].WorkerID })
	return out
}

func normalizeStatus(s EmploymentStatus) EmploymentStatus {
	switch s {
	case StatusActive, StatusTerminated, StatusOnLeave, StatusPending:
		return s
	default:
		return StatusUnknown
	}
}
