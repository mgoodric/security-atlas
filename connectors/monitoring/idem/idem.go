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
