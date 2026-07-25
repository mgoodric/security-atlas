package webhook

import (
	"encoding/json"
	"fmt"

	"github.com/mgoodric/security-atlas/internal/notify"
)

// Payload is the flat minimum-disclosure webhook JSON shape. It carries
// COUNTS + a deep-link only — never notification contents, evidence, or
// secrets (P0-543-1 / threat-model I). The `counts` map is keyed by the
// CLOSED human label (from notify.TypeLabel), so a raw type string from a
// row never reaches the wire.
type Payload struct {
	// Source is a constant discriminator so a receiver routing many
	// webhooks can recognize this one.
	Source string `json:"source"`
	// Event is a constant event name for this payload kind.
	Event string `json:"event"`
	// TotalUnread is the total un-muted unread count.
	TotalUnread int `json:"total_unread"`
	// Counts is label -> count (closed-label keys; no raw types).
	Counts map[string]int `json:"counts"`
	// DeepLink is the in-app notifications URL (the only detail pointer).
	DeepLink string `json:"deep_link"`
}

// BuildPayload assembles the minimum-disclosure webhook JSON from a
// Summary. The payload is structured (built by the stdlib JSON encoder, no
// string interpolation into the wire), so there is no injection surface
// (threat-model T): every value is encoded as a JSON value.
func BuildPayload(s notify.Summary) ([]byte, error) {
	if s.TotalUnread <= 0 {
		return nil, fmt.Errorf("webhook: empty summary")
	}
	counts := make(map[string]int, len(s.TypeCounts))
	for _, t := range s.SortedTypes() {
		n := s.TypeCounts[t]
		if n <= 0 {
			continue
		}
		// Aggregate into the closed label (collisions sum — defensive, the
		// label map is 1:1 today).
		counts[notify.TypeLabel(t)] += n
	}
	p := Payload{
		Source:      "security-atlas",
		Event:       "notification.digest",
		TotalUnread: s.TotalUnread,
		Counts:      counts,
		DeepLink:    s.DeepLink,
	}
	return json.Marshal(p)
}
