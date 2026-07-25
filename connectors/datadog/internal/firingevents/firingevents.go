// Package firingevents pulls Datadog monitor alert-FIRING history — the record
// of when a monitor actually entered its alert state and recovered — via the
// read-only Datadog Events API (GET /api/v1/events filtered to
// sources=monitor_alert, requires the events_read scope).
//
// This is the slice-535 firing-history surface, the sibling of slice 488's
// monitoring.alert_config.v1 monitor-inventory surface: slice 488 reads which
// monitors are CONFIGURED (SOC 2 CC7.2); this surface reads what actually FIRED
// and recovered (SOC 2 CC7.3/CC7.4 — the entity evaluates events and responds).
// It emits the vendor-neutral monitoring.alert_firing.v1 kind (one record per
// firing) shared with the Grafana firing collector.
//
// Profile: bounded PULL on the operator's schedule (the slice-636 precedent).
// Each run reads a bounded look-back window of monitor-alert events and pushes
// one record per firing via the single Push RPC (invariant #3). This is NOT
// continuous monitoring and NOT an event-driven receiver — the Datadog Events
// API is a search/poll surface with no first-class push this connector
// receives. The window is named honestly (--datadog-firing-lookback, default
// 24h).
//
// The load-bearing guard (P0-535 / threat-model I, Information Disclosure
// DOMINANT): the collector's RawEvent maps to firing.RawFiring, which can hold
// ONLY the firing rule's monitor id, the firing state, the timeline timestamps,
// and the OPAQUE notification-target handle. It is structurally INCAPABLE of
// holding the alert MESSAGE body, the triggering METRIC VALUES, the secret
// WEBHOOK URL, or recipient PII. The drop test feeds an event carrying all of
// those and proves none reaches a record.
package firingevents

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mgoodric/security-atlas/connectors/monitoring/firing"
)

// API is the narrow surface Collect depends on. The concrete implementation
// issues read-only Datadog Events API GETs against /api/v1/events; tests pass a
// fake. The implementation reads a bounded number of pages with a hard per-run
// cap (DoS guard) over a bounded look-back window.
type API interface {
	// ListMonitorEvents returns the monitor-alert events observed in [since,
	// now]. The bounded window keeps the read honest and small (NOT "all
	// history").
	ListMonitorEvents(ctx context.Context, since time.Time) ([]firing.RawFiring, error)
}

// Collect lists every monitor-alert firing in the bounded look-back window and
// returns normalized, body-free Firings. now is injectable for deterministic
// tests (nil -> time.Now UTC); lookback is the bounded window (<=0 defaults to
// 24h). Normalization (state folding, PII drop, ordering) is delegated to the
// shared firing.Collect so Datadog + Grafana produce identical record shapes.
func Collect(ctx context.Context, api API, lookback time.Duration, now func() time.Time) ([]firing.Firing, error) {
	if api == nil {
		return nil, errors.New("firingevents: API is nil")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if lookback <= 0 {
		lookback = 24 * time.Hour
	}
	since := now().UTC().Add(-lookback)
	raw, err := api.ListMonitorEvents(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("list datadog monitor events: %w", err)
	}
	return firing.Collect(firing.VendorDatadog, raw, now), nil
}
