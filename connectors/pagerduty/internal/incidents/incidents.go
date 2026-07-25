// Package incidents pulls PagerDuty incident SUMMARIES over a bounded look-back
// window via the read-only PagerDuty REST API (GET /incidents?since=&until=).
//
// The load-bearing guard (P0-489-3 / threat-model I): the client decodes ONLY
// each incident's id, number, status, urgency, the affected service's id/name,
// and the created/resolved timestamps. It NEVER materializes or emits the
// incident's free-text title/body/notes (which can embed customer data), the
// postmortem text, or any responder personal contact detail. json.Decode
// discards JSON keys with no matching struct field, so the unwanted free-text
// fields never enter memory as connector data.
package incidents

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// API is the narrow surface Collect depends on. The concrete implementation
// issues read-only PagerDuty API calls; tests pass a fake. v0 reads the first
// bounded page; cursor pagination is a documented follow-on (threat-model D).
type API interface {
	ListIncidents(ctx context.Context, since, until time.Time) ([]RawIncident, error)
}

// RawIncident is the narrow, free-text-free view the PagerDuty client returns
// for one incident. The HTTP client maps the API response into this shape,
// discarding the title/body/notes and every free-text field at the decode
// boundary. Tests construct it directly. There is no Title/Body/Notes field
// here BY CONSTRUCTION.
type RawIncident struct {
	ID          string
	Number      int
	Status      string
	Urgency     string
	ServiceID   string
	ServiceName string
	CreatedAt   time.Time
	ResolvedAt  time.Time // zero if unresolved
}

// Incident is the normalized summary view for one incident.
type Incident struct {
	ID          string
	Number      int
	Status      string
	Urgency     string
	ServiceID   string
	ServiceName string
	CreatedAt   time.Time
	ResolvedAt  time.Time // zero if unresolved
}

// Collect lists incidents in [since, until] and returns free-text-free summary
// records. Separated from record-building so the cmd layer owns the
// observed-at clock.
func Collect(ctx context.Context, api API, since, until time.Time) ([]Incident, error) {
	if api == nil {
		return nil, errors.New("incidents: API is nil")
	}
	raw, err := api.ListIncidents(ctx, since, until)
	if err != nil {
		return nil, fmt.Errorf("list pagerduty incidents: %w", err)
	}
	out := make([]Incident, 0, len(raw))
	for _, in := range raw {
		id := strings.TrimSpace(in.ID)
		if id == "" {
			continue
		}
		out = append(out, Incident{
			ID:          id,
			Number:      in.Number,
			Status:      normalizeStatus(in.Status),
			Urgency:     normalizeUrgency(in.Urgency),
			ServiceID:   strings.TrimSpace(in.ServiceID),
			ServiceName: strings.TrimSpace(in.ServiceName),
			CreatedAt:   in.CreatedAt.UTC(),
			ResolvedAt:  in.ResolvedAt.UTC(),
		})
	}
	return out, nil
}

// normalizeStatus maps a PagerDuty incident status to the schema enum. PagerDuty
// statuses are exactly triggered / acknowledged / resolved; an unknown status
// is coerced to "triggered" (the safest "still open" reading) rather than
// emitted verbatim, so the schema enum holds.
func normalizeStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "acknowledged":
		return "acknowledged"
	case "resolved":
		return "resolved"
	default:
		return "triggered"
	}
}

// normalizeUrgency maps a PagerDuty urgency to the schema enum (high / low).
func normalizeUrgency(s string) string {
	if strings.EqualFold(strings.TrimSpace(s), "low") {
		return "low"
	}
	return "high"
}
