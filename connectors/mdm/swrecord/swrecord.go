// Package swrecord builds the canonical endpoint.software_inventory.v1 evidence
// record from a normalized swinventory.Device. Both MDM connectors (Jamf +
// Intune, slice 555) share this builder because they share the evidence kind.
//
// The builder is the single choke point that turns connector-side installed-
// software inventory into a pushed record: it sets the idempotency key (from the
// shared idem package), the evidence kind / schema version, the scope
// dimensions, the source attribution, and the field-bounded payload. There is
// no code path here that could place a file path, app-usage telemetry, or a
// license key into the payload — the input swinventory.Device has no such field
// (P0-555). The per-item payload is allow-listed to name + version + identifier
// + install_date.
package swrecord

import (
	"time"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"

	"github.com/mgoodric/security-atlas/connectors/mdm/idem"
	"github.com/mgoodric/security-atlas/connectors/mdm/swinventory"
)

// EvidenceKind is the shared kind both MDM connectors emit for software
// inventory. Sibling to endpoint.device_posture.v1, NOT an extension of it.
const EvidenceKind = "endpoint.software_inventory.v1"

// SchemaVersion is the registered semver for EvidenceKind.
const SchemaVersion = "1.0.0"

// Build turns a normalized device's software inventory into a pushable
// EvidenceRecord. actorID is the connector's
// `connector:<vendor>:<service>@<version>` attribution; controlID is the SCF
// control to attach; service/env scope the record. The Result is always
// INCONCLUSIVE: the connector reports a descriptive inventory, and the platform
// evaluator owns the pass/fail call (e.g. known-vulnerable-version detection)
// per (control, scope).
func Build(dev swinventory.Device, controlID, actorID, service, environment string) (*evidencev1.EvidenceRecord, error) {
	now := dev.ObservedAt.UTC().Truncate(time.Hour)

	// Each software item is built from an explicit allow-list of keys. There is
	// no path here that copies an arbitrary source map, so a file path / usage
	// count / license key cannot leak even if the upstream type ever grew one.
	software := make([]any, 0, len(dev.Software))
	for _, s := range dev.Software {
		item := map[string]any{"name": s.Name}
		if s.Version != "" {
			item["version"] = s.Version
		}
		if s.Identifier != "" {
			item["identifier"] = s.Identifier
		}
		if s.InstallDate != "" {
			item["install_date"] = s.InstallDate
		}
		software = append(software, item)
	}

	pm := map[string]any{
		"source_mdm":     string(dev.SourceMDM),
		"device_id":      dev.DeviceID,
		"software_count": len(dev.Software),
		"software":       software,
	}
	payload, err := structpb.NewStruct(pm)
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idem.SoftwareInventoryKey(string(dev.SourceMDM), dev.DeviceID, now),
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
