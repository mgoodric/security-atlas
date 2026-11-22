package hostcapacity

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
)

// EvidenceKind is the vendor-neutral host capacity kind (#2935). It is
// routed by type (internal/evidence/routed): a record reaches every control
// whose queries declare it.
const EvidenceKind = "monitoring.host_capacity.v1"

// SchemaVersion is the registered semver for EvidenceKind.
const SchemaVersion = "1.0.0"

// RoleProduction is the only host_role this surface emits: every host comes
// from the operator's production scope.
const RoleProduction = "production"

func idemKey(host string, observedAt time.Time) string {
	hour := observedAt.UTC().Truncate(time.Hour).Format(time.RFC3339)
	sum := sha256.Sum256([]byte("datadog.host_capacity|" + host + "|" + hour))
	return hex.EncodeToString(sum[:])
}

// Build turns one host's readings into a pushable record. scope is the
// production scope the host was selected by; it becomes host_role_source.
// The result is always INCONCLUSIVE: the record is descriptive, and the
// control's query owns pass or fail.
func Build(h Host, scope, controlID, actorID, service, environment string) (*evidencev1.EvidenceRecord, error) {
	observed := h.WindowEnd.UTC().Truncate(time.Hour)
	pm := map[string]any{
		"source_vendor":    "datadog",
		"host_name":        h.HostName,
		"host_role":        RoleProduction,
		"host_role_source": "datadog_scope:" + scope,
		"window_start":     h.WindowStart.UTC().Format(time.RFC3339),
		"window_end":       h.WindowEnd.UTC().Format(time.RFC3339),
		"cpu_sample_count": h.CPUSampleCount,
	}
	if h.MemoryUsedPercent != nil {
		pm["memory_used_percent"] = *h.MemoryUsedPercent
	}
	if h.CPUP95Percent != nil {
		pm["cpu_p95_5m_percent"] = *h.CPUP95Percent
	}
	if h.DiskUsedPercent != nil {
		pm["disk_used_percent"] = *h.DiskUsedPercent
	}
	payload, err := structpb.NewStruct(pm)
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idemKey(h.HostName, observed),
		EvidenceKind:   EvidenceKind,
		SchemaVersion:  SchemaVersion,
		ControlId:      controlID,
		Scope: []*evidencev1.ScopeDimension{
			{Key: "service", Values: []string{service}},
			{Key: "environment", Values: []string{environment}},
		},
		ObservedAt: timestamppb.New(observed),
		Result:     evidencev1.Result_RESULT_INCONCLUSIVE,
		Payload:    payload,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "connector",
			ActorId:   actorID,
		},
	}, nil
}
