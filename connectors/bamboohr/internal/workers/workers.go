// Package workers pulls BambooHR worker-lifecycle records via the read-only
// BambooHR custom-report API (GET /v1/reports/custom with a minimal `fields`
// selector; requires only a read-only worker-directory role).
//
// The load-bearing guard (P0-491-3 / threat-model I): the client requests and
// decodes ONLY each worker's id, status, hire/termination dates, job title,
// department, manager (supervisor) assignment id, and work email. It NEVER
// materializes or emits SSN / national id, compensation / pay rate, home
// address, bank / payment details, benefits / health enrollment,
// performance-review fields, date of birth, personal phone, or protected-class
// data — those fields are simply not requested (the `fields=` selector lists the
// lifecycle fields only) and are not decoded into RawWorker. The struct shape is
// the second line of that guard.
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
// issues read-only BambooHR API calls; tests pass a fake. v0 reads the full
// report in one pass; cursor pagination is a documented follow-on (threat-model D).
type API interface {
	ListWorkers(ctx context.Context) ([]RawWorker, error)
}

// OneAPI is the narrow single-worker re-read surface the event-driven
// (subscribe) profile depends on (slice 573).
type OneAPI interface {
	GetWorker(ctx context.Context, workerID string) (RawWorker, bool, error)
}

// FetchOne re-reads ONE worker by id and maps it to the PII-bounded
// worker.RawWorker, reusing the same status/date mapping as the pull path so a
// webhook-triggered record is identical in shape to a polled one (slice 573).
func FetchOne(ctx context.Context, api OneAPI, workerID string) (worker.RawWorker, bool, error) {
	if api == nil {
		return worker.RawWorker{}, false, errors.New("workers: OneAPI is nil")
	}
	raw, ok, err := api.GetWorker(ctx, workerID)
	if err != nil {
		return worker.RawWorker{}, false, fmt.Errorf("get bamboohr worker %s: %w", workerID, err)
	}
	if !ok || strings.TrimSpace(raw.ID) == "" {
		return worker.RawWorker{}, false, nil
	}
	return worker.RawWorker{
		WorkerID:            raw.ID,
		Status:              mapStatus(raw.Status, raw.TerminationDate),
		StartDate:           parseDate(raw.HireDate),
		EndDate:             parseDate(raw.TerminationDate),
		Title:               raw.JobTitle,
		Department:          raw.Department,
		ManagerAssignmentID: raw.ManagerAssignmentID,
		WorkEmail:           raw.WorkEmail,
	}, true, nil
}

// RawWorker is the narrow, PII-bounded view the BambooHR client returns for one
// worker. The HTTP client maps the BambooHR report response into this shape,
// discarding all sensitive PII at the decode boundary. Tests construct it
// directly.
//
// There is intentionally no Ssn / PayRate / Compensation / HomeAddress /
// BankAccount / Benefits / Performance / DateOfBirth / PersonalPhone field on
// this struct (P0-491-3).
type RawWorker struct {
	ID                  string
	Status              string
	HireDate            string
	TerminationDate     string
	JobTitle            string
	Department          string
	ManagerAssignmentID string
	WorkEmail           string
}

// Collect lists every visible worker and returns PII-bounded worker.RawWorkers
// ready for worker.Normalize. Separated from normalization so the cmd layer owns
// the observed-at clock.
func Collect(ctx context.Context, api API) ([]worker.RawWorker, error) {
	if api == nil {
		return nil, errors.New("workers: API is nil")
	}
	raw, err := api.ListWorkers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list bamboohr workers: %w", err)
	}
	out := make([]worker.RawWorker, 0, len(raw))
	for _, w := range raw {
		if strings.TrimSpace(w.ID) == "" {
			continue
		}
		out = append(out, worker.RawWorker{
			WorkerID:            w.ID,
			Status:              mapStatus(w.Status, w.TerminationDate),
			StartDate:           parseDate(w.HireDate),
			EndDate:             parseDate(w.TerminationDate),
			Title:               w.JobTitle,
			Department:          w.Department,
			ManagerAssignmentID: w.ManagerAssignmentID,
			WorkEmail:           w.WorkEmail,
		})
	}
	return out, nil
}

// mapStatus maps BambooHR's status vocabulary to the shared lifecycle status.
// BambooHR reports "Active" / "Inactive". An Inactive worker WITH a termination
// date is a leaver (terminated); an Inactive worker without one is treated as
// on-leave (BambooHR uses Inactive for both, so the termination date
// disambiguates).
func mapStatus(s, terminationDate string) worker.EmploymentStatus {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "active":
		return worker.StatusActive
	case "inactive":
		td := strings.TrimSpace(terminationDate)
		if td != "" && td != "0000-00-00" {
			return worker.StatusTerminated
		}
		return worker.StatusOnLeave
	default:
		return worker.StatusUnknown
	}
}

// parseDate parses a BambooHR date string (ISO date) into a UTC time. BambooHR
// uses the sentinel "0000-00-00" for an unset date; that and any empty /
// unparseable value yield the zero time (omitted downstream).
func parseDate(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" || s == "0000-00-00" {
		return time.Time{}
	}
	for _, layout := range []string{"2006-01-02", time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
