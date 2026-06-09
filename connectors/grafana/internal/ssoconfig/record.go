package ssoconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
)

// EvidenceKind is the slice-534 access-config kind. It is a SEPARATE kind from
// monitoring.alert_config.v1 (slice 488) because this is an IAM surface (SSO
// enforcement + RBAC posture) rather than a monitoring surface — slice 488
// deferred exactly this authn/authz surface (P0-488-7).
const EvidenceKind = "grafana.access_config.v1"

// SchemaVersion is the registered semver for EvidenceKind.
const SchemaVersion = "1.0.0"

// idemKey derives the idempotency key for the single access-config record. The
// environment + the UTC-hour-truncated observed_at uniquely identify the record
// within the hour, collapsing same-hour re-runs into one ledger row.
func idemKey(environment string, observedAt time.Time) string {
	hour := observedAt.UTC().Truncate(time.Hour).Format(time.RFC3339)
	sum := sha256.Sum256([]byte("grafana.access_config|" + environment + "|" + hour))
	return hex.EncodeToString(sum[:])
}

// Build turns a normalized AccessConfig into a pushable EvidenceRecord.
// actorID is the connector's `connector:grafana:ssoconfig@<version>`
// attribution; controlID is the SCF control to attach; service/environment
// scope the record. The Result is always INCONCLUSIVE: the connector reports
// descriptive configuration (SSO enabled + provider posture + access counts);
// the platform evaluator owns the pass/fail call per (control, scope).
//
// There is no code path here that could place a SAML private key, an OAuth
// client secret, an LDAP bind password, a signing certificate, a user name, or
// a user email into the payload — the input AccessConfig has no such field
// (P0-534). Only enabled-flags, provider TYPES, org-role mapping RULE strings,
// and COUNTS reach the payload.
func Build(cfg AccessConfig, controlID, actorID, service, environment string) (*evidencev1.EvidenceRecord, error) {
	now := cfg.ObservedAt.UTC().Truncate(time.Hour)

	pm := map[string]any{
		"sso_enabled":              cfg.SSOEnabled,
		"team_count":               cfg.TeamCount,
		"total_team_memberships":   cfg.TotalTeamMemberships,
		"user_role_assignments":    cfg.UserRoleAssignments,
		"team_role_assignments":    cfg.TeamRoleAssignments,
		"builtin_role_assignments": cfg.BuiltinRoleAssignments,
	}
	if len(cfg.Providers) > 0 {
		providers := make([]any, 0, len(cfg.Providers))
		for _, p := range cfg.Providers {
			entry := map[string]any{
				"provider_type": p.Type,
				"enabled":       p.Enabled,
			}
			if len(p.RoleMappings) > 0 {
				mappings := make([]any, 0, len(p.RoleMappings))
				for _, m := range p.RoleMappings {
					mappings = append(mappings, m)
				}
				entry["role_mappings"] = mappings
			}
			providers = append(providers, entry)
		}
		pm["sso_providers"] = providers
	}

	payload, err := structpb.NewStruct(pm)
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idemKey(environment, now),
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
