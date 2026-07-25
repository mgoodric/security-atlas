// Package postmortems pulls PagerDuty postmortem / retrospective METADATA over a
// bounded look-back window via the read-only PagerDuty REST API. It proves that
// incidents are reviewed and that corrective actions are tracked (SOC 2 CC7.5
// "recover from identified security incidents"; the slice-372 IR
// continuous-improvement loop) — NOT the story.
//
// The load-bearing guard (P0-538 / threat-model I — DOMINANT): a postmortem is
// dense free-text that routinely embeds customer data, responder PII, and
// root-cause prose. This package decodes ONLY the META-FACTS an auditor needs:
// that a postmortem EXISTS for an incident, the linked incident id, the review
// status (e.g. not_started / in_progress / in_review / published), the
// created / published timestamps, and a CORRECTIVE-ACTION rollup (how many
// action items are tracked and how many are completed). It NEVER materializes
// or emits the postmortem narrative body, the timeline free-text, the
// root-cause prose, an action item's operator-authored title/description, or
// any customer/responder PII.
//
// The structural guarantee: RawPostmortem / Postmortem / RawActionItem have NO
// field capable of holding narrative free-text or an action-item title BY
// CONSTRUCTION, and json.Decode discards JSON keys with no matching struct
// field — so the narrative fields never enter memory as connector data even
// when the PagerDuty payload carries them. A reflection test
// (postmortems_guard_test.go) fails the build if any field is ever added that
// could hold free-text; a drop test feeds a fake response WITH a narrative body
// and proves it never reaches a record.
package postmortems

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// MaxRecords caps how many postmortem-metadata records one run will emit
// (threat-model D — DoS guard). The bounded look-back window plus the
// per-page limit plus this hard cap keep a run bounded regardless of how many
// postmortems the source returns.
const MaxRecords = 1000

// API is the narrow surface Collect depends on. The concrete implementation
// issues read-only PagerDuty API calls; tests pass a fake. v0 reads a bounded
// page set; cursor pagination beyond the cap is a documented follow-on
// (threat-model D).
type API interface {
	ListPostmortems(ctx context.Context, since, until time.Time) ([]RawPostmortem, error)
}

// RawActionItem is the narrow, free-text-free view of one corrective-action
// item. There is NO Title/Description/Body/Note field here BY CONSTRUCTION: an
// action item's title is operator-authored free-text (it routinely names the
// affected customer, system, or root cause) and is therefore excluded — only
// the item's existence and completion state are collected, for the rollup.
type RawActionItem struct {
	// Completed is the corrective-action completion state — the only fact the
	// rollup needs. PagerDuty exposes a resolved/completed status on a response
	// play / action item; we collapse it to a boolean.
	Completed bool
}

// RawPostmortem is the narrow, free-text-free view the PagerDuty client returns
// for one postmortem. The HTTP client maps the API response into this shape,
// discarding the narrative body / timeline / root-cause prose and every
// free-text field at the decode boundary. Tests construct it directly. There is
// NO Body/Narrative/Title/Summary/Description/Cause/Notes/Timeline field here
// BY CONSTRUCTION (P0-538).
type RawPostmortem struct {
	ID          string
	IncidentID  string
	Status      string
	CreatedAt   time.Time
	PublishedAt time.Time // zero if not yet published
	ActionItems []RawActionItem
}

// Postmortem is the normalized metadata view for one postmortem. It carries the
// corrective-action ROLLUP (count + completed/open split) — never an action
// item's free-text title. NO field can hold narrative prose BY CONSTRUCTION.
type Postmortem struct {
	ID              string
	IncidentID      string
	Status          string
	CreatedAt       time.Time
	PublishedAt     time.Time // zero if not yet published
	ActionItemCount int
	ActionItemsDone int
	ActionItemsOpen int
}

// Collect lists postmortem metadata in [since, until] and returns free-text-free
// metadata records with the corrective-action rollup. Separated from
// record-building so the cmd layer owns the observed-at clock. The result is
// hard-capped at MaxRecords (DoS guard).
func Collect(ctx context.Context, api API, since, until time.Time) ([]Postmortem, error) {
	if api == nil {
		return nil, errors.New("postmortems: API is nil")
	}
	raw, err := api.ListPostmortems(ctx, since, until)
	if err != nil {
		return nil, fmt.Errorf("list pagerduty postmortems: %w", err)
	}
	out := make([]Postmortem, 0, len(raw))
	for _, in := range raw {
		id := strings.TrimSpace(in.ID)
		incidentID := strings.TrimSpace(in.IncidentID)
		// A postmortem with no stable id or no linked incident is not auditor-
		// useful (we cannot tie the review to an incident); drop it.
		if id == "" || incidentID == "" {
			continue
		}
		done := 0
		for _, ai := range in.ActionItems {
			if ai.Completed {
				done++
			}
		}
		count := len(in.ActionItems)
		out = append(out, Postmortem{
			ID:              id,
			IncidentID:      incidentID,
			Status:          normalizeStatus(in.Status),
			CreatedAt:       in.CreatedAt.UTC(),
			PublishedAt:     in.PublishedAt.UTC(),
			ActionItemCount: count,
			ActionItemsDone: done,
			ActionItemsOpen: count - done,
		})
		if len(out) >= MaxRecords {
			break
		}
	}
	return out, nil
}

// normalizeStatus maps a PagerDuty postmortem/review status to the schema enum.
// PagerDuty's status_updates / postmortem lifecycle values are coerced into a
// stable set; an unknown status is coerced to "not_started" (the safest
// "review not yet meaningfully underway" reading) rather than emitted verbatim,
// so the schema enum holds and no free-text status string leaks.
func normalizeStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "published":
		return "published"
	case "in_review", "in-review", "review":
		return "in_review"
	case "in_progress", "in-progress", "draft", "started":
		return "in_progress"
	default:
		return "not_started"
	}
}
