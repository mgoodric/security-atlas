// Package devrecord builds the canonical endpoint.device_posture.v1 evidence
// record from a normalized devposture.Device. Both MDM connectors (Jamf +
// Intune, slice 490) share this builder because they share the evidence kind.
//
// The builder is the single choke point that turns connector-side device
// posture into a pushed record: it sets the idempotency key (from the shared
// idem package), the evidence kind / schema version, the scope dimensions, the
// source attribution, and the PII-bounded payload. There is no code path here
// that could place geolocation, an app inventory, or owner contact PII into the
// payload — the input devposture.Device has no such field (P0-490-3).
package devrecord

import (
	"time"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"

	"github.com/mgoodric/security-atlas/connectors/mdm/devposture"
	"github.com/mgoodric/security-atlas/connectors/mdm/idem"
)

// EvidenceKind is the shared kind both MDM connectors emit.
const EvidenceKind = "endpoint.device_posture.v1"

// SchemaVersion is the registered semver for EvidenceKind.
const SchemaVersion = "1.0.0"

// Build turns a normalized device into a pushable EvidenceRecord. actorID is the
// connector's `connector:<vendor>:<service>@<version>` attribution; controlID is
// the SCF control to attach; service/env scope the record. The Result is always
// INCONCLUSIVE: the connector reports descriptive posture (encryption /
// screen-lock / compliance state), and the platform evaluator owns the pass/fail
// call per (control, scope).
func Build(dev devposture.Device, controlID, actorID, service, environment string) (*evidencev1.EvidenceRecord, error) {
	now := dev.ObservedAt.UTC().Truncate(time.Hour)
	pm := map[string]any{
		"source_mdm":              string(dev.SourceMDM),
		"device_id":               dev.DeviceID,
		"disk_encryption_enabled": dev.DiskEncryptionEnabled,
		"screen_lock_enabled":     dev.ScreenLockEnabled,
		"managed":                 dev.Managed,
		"enrolled":                dev.Enrolled,
		"compliance_result":       string(dev.Compliance),
	}
	// Stable optional fields — present only when the source supplied them, so a
	// record's shape is stable for a given device and a missing optional does not
	// emit an empty string (matches the slice-004 stable-optional convention).
	if dev.DeviceName != "" {
		pm["device_name"] = dev.DeviceName
	}
	if dev.OSVersion != "" {
		pm["os_version"] = dev.OSVersion
	}
	if dev.Platform != "" {
		pm["platform"] = dev.Platform
	}
	if dev.OwnerAssignmentID != "" {
		pm["owner_assignment_id"] = dev.OwnerAssignmentID
	}
	if dev.OwnerDisplayName != "" {
		pm["owner_display_name"] = dev.OwnerDisplayName
	}
	payload, err := structpb.NewStruct(pm)
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idem.DevicePostureKey(string(dev.SourceMDM), dev.DeviceID, now),
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
