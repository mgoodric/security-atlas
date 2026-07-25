// Package alerthistory pulls Grafana alert-instance state-history — the record
// of when an alert rule actually transitioned into / out of its firing state —
// via the read-only Grafana alerting state-history API (GET /api/v1/rules/
// history, Viewer role).
//
// This is the slice-535 firing-history surface, the sibling of slice 488's
// monitoring.alert_config.v1 alert-rule-inventory surface: slice 488 reads
// which rules are CONFIGURED (SOC 2 CC7.2); this surface reads what actually
// FIRED and resolved (SOC 2 CC7.3/CC7.4). It emits the vendor-neutral
// monitoring.alert_firing.v1 kind (one record per firing transition) shared
// with the Datadog firing collector.
//
// Profile: bounded PULL on the operator's schedule (the slice-636 precedent).
// Each run reads a bounded look-back window of state-history transitions and
// pushes one record per firing via the single Push RPC (invariant #3). This is
// NOT continuous monitoring and NOT an event-driven receiver — the Grafana
// state-history API is a query surface with no first-class push this connector
// receives. The window is named honestly (--grafana-firing-lookback, default
// 24h).
//
// The load-bearing guard (P0-535 / threat-model I, Information Disclosure
// DOMINANT): the collector's client maps each transition to firing.RawFiring,
// which can hold ONLY the rule uid, the firing state, the timestamp, and the
// OPAQUE contact-point handle. It is structurally INCAPABLE of holding the
// alert annotation/message body, the triggering metric VALUES (the labels'
// numeric values), the secret contact-point settings (webhook URL / token), or
// recipient PII. The drop test feeds a transition carrying all of those and
// proves none reaches a record.
package alerthistory

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mgoodric/security-atlas/connectors/monitoring/firing"
)

// API is the narrow surface Collect depends on. The concrete implementation
// issues read-only Grafana state-history API GETs; tests pass a fake. The
// implementation reads a bounded look-back window with a hard per-run
// transition cap (DoS guard).
type API interface {
	// ListStateHistory returns the alert-instance state transitions observed in
	// [since, now]. The bounded window keeps the read honest and small.
	ListStateHistory(ctx context.Context, since time.Time) ([]firing.RawFiring, error)
}

// Collect lists every alert-instance state transition in the bounded look-back
// window and returns normalized, body-free Firings. now is injectable for
// deterministic tests (nil -> time.Now UTC); lookback is the bounded window
// (<=0 defaults to 24h). Normalization (state folding, PII drop, ordering) is
// delegated to the shared firing.Collect so Datadog + Grafana produce identical
// record shapes.
func Collect(ctx context.Context, api API, lookback time.Duration, now func() time.Time) ([]firing.Firing, error) {
	if api == nil {
		return nil, errors.New("alerthistory: API is nil")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if lookback <= 0 {
		lookback = 24 * time.Hour
	}
	since := now().UTC().Add(-lookback)
	raw, err := api.ListStateHistory(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("list grafana alert state-history: %w", err)
	}
	return firing.Collect(firing.VendorGrafana, raw, now), nil
}
