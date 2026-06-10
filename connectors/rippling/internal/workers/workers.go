// Package workers pulls Rippling worker-lifecycle records via the read-only
// Rippling employee-directory API (GET /platform/api/employees with a minimal
// `fields` selector; requires only a read-only directory scope).
//
// The load-bearing guard (P0-491-3 / threat-model I): the client requests and
// decodes ONLY each worker's id, employment status, start/end dates, title,
// department, manager assignment id, and work email. It NEVER materializes or
// emits SSN / national id, compensation / salary, home address, bank / payment
// details, benefits / health enrollment, performance-review fields, date of
// birth, personal phone, or protected-class data — those fields are simply not
// requested (the `fields=` query asks for the lifecycle fields only) and are not
// decoded into RawWorker. The struct shape is the second line of that guard.
package workers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mgoodric/security-atlas/connectors/hris/worker"
)

// API is the narrow surface Collect depends on. The concrete implementation
// issues read-only Rippling API calls; tests pass a fake. v0 reads the first
// bounded page; cursor pagination is a documented follow-on (threat-model D).
type API interface {
	ListWorkers(ctx context.Context) ([]RawWorker, error)
}

// OneAPI is the narrow single-worker re-read surface the event-driven
// (subscribe) profile depends on (slice 573). The concrete client issues a
// read-only GET for one worker's minimal lifecycle fields.
type OneAPI interface {
	GetWorker(ctx context.Context, workerID string) (RawWorker, bool, error)
}

// FetchOne re-reads ONE worker by id and maps it to the PII-bounded
// worker.RawWorker, reusing the same status/date mapping as the pull path so a
// webhook-triggered record is identical in shape to a polled one (slice 573).
// ok=false when the source no longer returns the worker.
func FetchOne(ctx context.Context, api OneAPI, workerID string) (worker.RawWorker, bool, error) {
	if api == nil {
		return worker.RawWorker{}, false, errors.New("workers: OneAPI is nil")
	}
	raw, ok, err := api.GetWorker(ctx, workerID)
	if err != nil {
		return worker.RawWorker{}, false, fmt.Errorf("get rippling worker %s: %w", workerID, err)
	}
	if !ok || strings.TrimSpace(raw.ID) == "" {
		return worker.RawWorker{}, false, nil
	}
	return worker.RawWorker{
		WorkerID:            raw.ID,
		Status:              mapStatus(raw.EmploymentStatus),
		StartDate:           parseDate(raw.StartDate),
		EndDate:             parseDate(raw.EndDate),
		Title:               raw.Title,
		Department:          raw.Department,
		ManagerAssignmentID: raw.ManagerAssignmentID,
		WorkEmail:           raw.WorkEmail,
	}, true, nil
}

// RawWorker is the narrow, PII-bounded view the Rippling client returns for one
// worker. The HTTP client maps the Rippling directory response into this shape,
// discarding all sensitive PII at the decode boundary. Tests construct it
// directly.
//
// There is intentionally no Ssn / Compensation / HomeAddress / BankAccount /
// Benefits / Performance / DateOfBirth / PersonalPhone field on this struct
// (P0-491-3).
type RawWorker struct {
	ID                  string
	EmploymentStatus    string
	StartDate           string
	EndDate             string
	Title               string
	Department          string
	ManagerAssignmentID string
	WorkEmail           string
}

// Collect lists every visible worker and returns PII-bounded
// worker.RawWorkers ready for worker.Normalize. Separated from normalization so
// the cmd layer owns the observed-at clock.
func Collect(ctx context.Context, api API) ([]worker.RawWorker, error) {
	if api == nil {
		return nil, errors.New("workers: API is nil")
	}
	raw, err := api.ListWorkers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list rippling workers: %w", err)
	}
	out := make([]worker.RawWorker, 0, len(raw))
	for _, w := range raw {
		if strings.TrimSpace(w.ID) == "" {
			continue
		}
		out = append(out, worker.RawWorker{
			WorkerID:            w.ID,
			Status:              mapStatus(w.EmploymentStatus),
			StartDate:           parseDate(w.StartDate),
			EndDate:             parseDate(w.EndDate),
			Title:               w.Title,
			Department:          w.Department,
			ManagerAssignmentID: w.ManagerAssignmentID,
			WorkEmail:           w.WorkEmail,
		})
	}
	return out, nil
}

// mapStatus maps Rippling's employmentStatus vocabulary to the shared lifecycle
// status. Rippling reports e.g. ACTIVE / TERMINATED / LEAVE / PENDING.
func mapStatus(s string) worker.EmploymentStatus {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "ACTIVE":
		return worker.StatusActive
	case "TERMINATED", "OFFBOARDED":
		return worker.StatusTerminated
	case "LEAVE", "ON_LEAVE":
		return worker.StatusOnLeave
	case "PENDING", "PREHIRE", "ACCEPTED":
		return worker.StatusPending
	default:
		return worker.StatusUnknown
	}
}

// parseDate parses a Rippling date string (ISO date or date-time) into a UTC
// time. An empty / unparseable value yields the zero time (omitted downstream).
func parseDate(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{"2006-01-02", time.RFC3339, "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
