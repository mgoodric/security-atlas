// Package cfgrecord builds the canonical endpoint.config_profile.v1 evidence
// record from a normalized cfgprofile.Device. Both MDM connectors (Jamf +
// Intune, slice 556) share this builder because they share the evidence kind.
//
// The builder is the single choke point that turns connector-side configuration-
// profile detail into a pushed record: it sets the idempotency key (from the
// shared idem package), the evidence kind / schema version, the scope
// dimensions, the source attribution, and the field-bounded payload. There is no
// code path here that could place a credential payload — a Wi-Fi PSK, a VPN
// shared secret, a certificate private key, a SCEP challenge, an API token, or a
// raw payload-content blob — into the payload: the input cfgprofile.Device has no
// such field (P0-556), and the per-profile settings were already filtered to the
// compliance-relevant allow-list at normalization. As belt-and-braces the
// builder re-applies the allow-list + deny-list so a setting key that should
// never appear cannot reach the payload even if a caller hand-built a Device.
package cfgrecord

import (
	"time"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"

	"github.com/mgoodric/security-atlas/connectors/mdm/cfgprofile"
	"github.com/mgoodric/security-atlas/connectors/mdm/idem"
)

// EvidenceKind is the shared kind both MDM connectors emit for configuration-
// profile detail. Sibling to endpoint.device_posture.v1 and
// endpoint.software_inventory.v1, NOT an extension of either.
const EvidenceKind = "endpoint.config_profile.v1"

// SchemaVersion is the registered semver for EvidenceKind.
const SchemaVersion = "1.0.0"

// Build turns a normalized device's assigned configuration / compliance profiles
// into a pushable EvidenceRecord. actorID is the connector's
// `connector:<vendor>:<service>@<version>` attribution; controlID is the SCF
// control to attach; service/env scope the record. The Result is always
// INCONCLUSIVE: the connector reports a descriptive configuration baseline, and
// the platform evaluator owns the pass/fail call (e.g. "is the screen-lock
// policy enforced on every in-scope device?") per (control, scope).
func Build(dev cfgprofile.Device, controlID, actorID, service, environment string) (*evidencev1.EvidenceRecord, error) {
	now := dev.ObservedAt.UTC().Truncate(time.Hour)

	// Each profile is built from an explicit allow-list of top-level keys, and
	// each setting is re-filtered against the compliance-relevant allow-list +
	// the credential deny-list. There is no path here that copies an arbitrary
	// source map, so a Wi-Fi PSK / VPN shared secret / private key / payload blob
	// cannot leak even if the upstream type ever grew one.
	profiles := make([]any, 0, len(dev.Profiles))
	for _, p := range dev.Profiles {
		item := map[string]any{"name": p.Name}
		if p.Identifier != "" {
			item["identifier"] = p.Identifier
		}
		if p.ProfileType != "" {
			item["profile_type"] = p.ProfileType
		}
		if len(p.Scope) > 0 {
			scope := make([]any, 0, len(p.Scope))
			for _, s := range p.Scope {
				scope = append(scope, s)
			}
			item["scope"] = scope
		}
		if p.UUID != "" {
			item["uuid"] = p.UUID
		}
		if p.LastModified != "" {
			item["last_modified"] = p.LastModified
		}
		settings := make([]any, 0, len(p.Settings))
		for _, s := range p.Settings {
			// Belt-and-braces: never emit a setting whose key is off the allow-list
			// or flagged as credential-bearing, regardless of how the Device was
			// constructed (the load-bearing secret-redaction guard, P0-556).
			if !cfgprofile.AllowedSettingKeys[s.Key] || cfgprofile.IsBannedSettingKey(s.Key) {
				continue
			}
			settings = append(settings, map[string]any{"key": s.Key, "value": s.Value})
		}
		item["settings"] = settings
		profiles = append(profiles, item)
	}

	pm := map[string]any{
		"source_mdm":    string(dev.SourceMDM),
		"device_id":     dev.DeviceID,
		"profile_count": len(dev.Profiles),
		"profiles":      profiles,
	}
	payload, err := structpb.NewStruct(pm)
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idem.ConfigProfileKey(string(dev.SourceMDM), dev.DeviceID, now),
		EvidenceKind:   EvidenceKind,
		SchemaVersion:  SchemaVersion,
		ControlId:      controlID,
		Scope: []*evidencev1.ScopeDimension{
			{Key: "service", Values: []string{service}},
			{Key: "environment", Values: []string{environment}},
		},
		ObservedAt: timestamppb.New(now),
		Result:     evidencev1.Result_RESULT_INCONCLUSIVE, // descriptive — evaluator decides
		Payload:    payload,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "connector",
			ActorId:   actorID,
		},
	}, nil
}
