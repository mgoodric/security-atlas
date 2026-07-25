// Package eventgrid is the SOURCE-side Azure Event Grid change-event receiver for
// the Azure connector's `subscribe` profile (slice 522). It is a THIN adapter onto
// connectors/shared/webhookrecv (slice 656): the bounded gosec-G112 http.Server,
// the graceful Serve(ctx), the MaxBytesReader body cap, and the verify-FIRST → 401
// skeleton all live in webhookrecv. This package owns only the Event-Grid-domain
// glue:
//
//   - parse an Event Grid event envelope into the changed RESOURCE ID + resource
//     TYPE (the event is a TRIGGER, not the data),
//   - route the resource type to the EXISTING read-only Azure reader,
//   - the SubscriptionValidation handshake (validationCode echo) and the
//     per-delivery delivery-key Verifier,
//   - a bounded queue + coalescing window so an event storm collapses to one
//     re-read per resource (threat-model D).
//
// Invariant #3 (CLAUDE.md): the platform-side wire surface is ALWAYS push. This
// package is part of a CONNECTOR, not the platform — it adds NO inbound API to
// internal/api/. `subscribe` is a profiles_supported value describing how the
// connector retrieves data FROM Azure (event-driven receipt); the connector still
// re-emits via Push, and it re-emits the SAME record the pull profile builds (via
// the EXISTING reader + builder), so a forged event can never fabricate a record.
package eventgrid

import (
	"encoding/json"
	"strings"
)

// ValidationEventType is the Event Grid system event type carried when Event Grid
// validates a webhook subscription. The receiver echoes the validationCode and
// builds NO record (D2).
const ValidationEventType = "Microsoft.EventGrid.SubscriptionValidationEvent"

// ResourceType enumerates the in-scope Azure resource types this connector has an
// EXISTING reader for (D3). An event whose resource id maps to none of these is
// dropped honestly (acked 200, no re-read).
type ResourceType string

const (
	ResourceStorage  ResourceType = "storage"
	ResourceAKS      ResourceType = "aks"
	ResourceNSG      ResourceType = "nsg"
	ResourceKeyVault ResourceType = "keyvault"
	ResourceFirewall ResourceType = "firewall"
	ResourceEntra    ResourceType = "entra"
	// ResourceUnknown marks an event with no in-scope reader; the connector drops
	// it (acks 200, emits nothing) rather than inventing a reader.
	ResourceUnknown ResourceType = ""
)

// providerRoute maps the ARM resource-provider/type path segment (lower-cased) to
// the EXISTING reader that re-reads that resource type. The mapping is the routing
// table in the decisions log (D3). Lower-cased keys make the match
// case-insensitive (ARM ids are not case-stable).
var providerRoute = map[string]ResourceType{
	"microsoft.storage/storageaccounts":          ResourceStorage,
	"microsoft.containerservice/managedclusters": ResourceAKS,
	"microsoft.network/networksecuritygroups":    ResourceNSG,
	"microsoft.keyvault/vaults":                  ResourceKeyVault,
	"microsoft.network/firewallpolicies":         ResourceFirewall,
	"microsoft.authorization/roleassignments":    ResourceEntra,
}

// gridEvent is the SUMMARY-ONLY view of an Event Grid event envelope. Event Grid
// posts a batch (a JSON array of events). Only the fields the connector takes from
// the event — its TYPE, its changed resource id (subject / data.resourceUri), and
// the validation handshake's code — are modelled. There is intentionally NO field
// for arbitrary event payload data: the event is a TRIGGER, and json.Unmarshal
// discards the unmodelled payload keys at the decode boundary, so they can never
// become record data (no-fabrication).
type gridEvent struct {
	// EventType is the Event Grid event type (e.g.
	// "Microsoft.Resources.ResourceWriteSuccess" for an ARM change, or
	// ValidationEventType for the handshake).
	EventType string `json:"eventType"`
	// Subject is the ARM resource path the event concerns (Event Grid sets this to
	// the changed resource's id for resource events).
	Subject string `json:"subject"`
	// Topic is the ARM scope (subscription / resource group) the event was raised
	// under; used as a fallback when Subject is relative.
	Topic string `json:"topic"`
	// Data carries the validation code (handshake) and, for resource events, the
	// resource uri. NOTHING from Data becomes record data.
	Data struct {
		// ValidationCode is present ONLY on a SubscriptionValidationEvent.
		ValidationCode string `json:"validationCode"`
		// ValidationURL is the manual-handshake fallback Event Grid also offers; the
		// connector implements the synchronous code echo and does not visit this.
		ValidationURL string `json:"validationUrl"`
		// ResourceURI is the changed resource id for an ARM resource event (Activity
		// Log → Event Grid sets data.resourceUri).
		ResourceURI string `json:"resourceUri"`
	} `json:"data"`
}

// ParsedEvent is the connector's narrow take from one Event Grid event: ONLY the
// changed resource id, the resource type it routes to, and whether it is the
// validation handshake. NOTHING else from the event is retained — the record's data
// comes entirely from the subsequent re-read (D4).
type ParsedEvent struct {
	// ValidationCode is non-empty ONLY for a SubscriptionValidationEvent; when set,
	// the receiver echoes it and builds no record.
	ValidationCode string
	// ResourceID is the changed resource's ARM id (the re-read filters to it).
	ResourceID string
	// ResourceType is the reader this event routes to, or ResourceUnknown to drop.
	ResourceType ResourceType
}

// ParseBatch decodes an Event Grid delivery (a JSON array of events, or a single
// event object) and returns the parsed events. A malformed body returns an error
// (→ 400). The validation handshake is one event with a ValidationCode set.
//
// Event Grid delivers a JSON ARRAY; some validation handshakes arrive as a single
// object. Both shapes are accepted so a handshake is never missed.
func ParseBatch(body []byte) ([]ParsedEvent, error) {
	trimmed := strings.TrimSpace(string(body))
	var raw []gridEvent
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, err
		}
	} else {
		var one gridEvent
		if err := json.Unmarshal(body, &one); err != nil {
			return nil, err
		}
		raw = []gridEvent{one}
	}
	out := make([]ParsedEvent, 0, len(raw))
	for _, e := range raw {
		out = append(out, parseOne(e))
	}
	return out, nil
}

// parseOne maps one envelope to the connector's narrow take.
func parseOne(e gridEvent) ParsedEvent {
	if strings.EqualFold(strings.TrimSpace(e.EventType), ValidationEventType) {
		return ParsedEvent{ValidationCode: strings.TrimSpace(e.Data.ValidationCode)}
	}
	id := strings.TrimSpace(e.Subject)
	if id == "" {
		id = strings.TrimSpace(e.Data.ResourceURI)
	}
	return ParsedEvent{
		ResourceID:   id,
		ResourceType: routeResourceID(id),
	}
}

// IsValidation reports whether the parsed event is the SubscriptionValidation
// handshake (carries a validation code).
func (p ParsedEvent) IsValidation() bool { return p.ValidationCode != "" }

// routeResourceID derives the in-scope ResourceType from an ARM resource id by
// scanning for a known `<provider>/<type>` segment pair (case-insensitive). An id
// with no in-scope provider returns ResourceUnknown (the event is dropped).
//
// ARM ids look like:
//
//	/subscriptions/<sub>/resourceGroups/<rg>/providers/Microsoft.Storage/storageAccounts/<name>
//
// so the provider/type pair is the two segments after "providers".
func routeResourceID(id string) ResourceType {
	if id == "" {
		return ResourceUnknown
	}
	parts := strings.Split(id, "/")
	for i := 0; i+2 < len(parts); i++ {
		if strings.EqualFold(parts[i], "providers") {
			key := strings.ToLower(parts[i+1] + "/" + parts[i+2])
			if rt, ok := providerRoute[key]; ok {
				return rt
			}
		}
	}
	return ResourceUnknown
}

// SameResourceID compares two ARM resource ids case-insensitively (ARM ids are not
// case-stable across responses). Used by the re-read filter to match the re-read's
// resources to the event's changed resource (D4).
func SameResourceID(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}
