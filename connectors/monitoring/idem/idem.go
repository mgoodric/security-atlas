// Package idem derives idempotency keys for the monitoring connector family
// (Datadog + Grafana, slice 488). One key shape across both vendors, parallel
// to the slice 004 / 486 / 487 idem packages.
//
//	AlertConfigKey: sha256("monitoring.alert_config|<vendor>/<rule_id>|<hour>")
//
// The vendor + rule_id together uniquely identify a monitor / alert rule;
// truncating observed_at to the UTC hour collapses same-rule re-runs within the
// hour into one ledger row.
//
// Anti-criterion: every push from either monitoring connector derives its
// idempotency_key here. The cmd layer never invents one ad-hoc and never pushes
// with an empty key.
package idem

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// AlertConfigKey returns the idempotency key for one monitor / alert rule.
func AlertConfigKey(vendor, ruleID string, observedAt time.Time) string {
	hour := observedAt.UTC().Truncate(time.Hour).Format(time.RFC3339)
	sum := sha256.Sum256([]byte("monitoring.alert_config|" + vendor + "/" + ruleID + "|" + hour))
	return hex.EncodeToString(sum[:])
}

// AlertFiringKey returns the idempotency key for one alert-firing event
// (slice 535):
//
//	sha256("monitoring.alert_firing|<vendor>/<rule_id>/<fired_at>|<hour>")
//
// The (vendor, rule_id, fired_at) triple uniquely identifies a single firing
// of a rule: a busy rule fires many distinct times, and each firing is its own
// audit-relevant event, so fired_at is part of the key (a config record keys on
// (vendor, rule_id) alone — there is one config per rule). Including the
// firedAt instant is the dedup JUDGMENT that makes an overlapping look-back
// window re-read collapse onto the same ledger row instead of double-writing
// (threat-model R). The observed-at UTC hour is also folded in, matching the
// config key's hour-granularity replay shape; firedAt is rendered to the second
// in RFC3339 so two firings within the same hour stay distinct rows.
func AlertFiringKey(vendor, ruleID string, firedAt, observedAt time.Time) string {
	fired := firedAt.UTC().Format(time.RFC3339)
	hour := observedAt.UTC().Truncate(time.Hour).Format(time.RFC3339)
	sum := sha256.Sum256([]byte("monitoring.alert_firing|" + vendor + "/" + ruleID + "/" + fired + "|" + hour))
	return hex.EncodeToString(sum[:])
}
