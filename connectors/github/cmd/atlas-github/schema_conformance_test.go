package main

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/mgoodric/security-atlas/internal/api/schemaregistry"

	"github.com/mgoodric/security-atlas/connectors/github/internal/githubrepo"
)

func intp(n int) *int    { return &n }
func boolp(b bool) *bool { return &b }

// validatePushed runs a record built by buildRepoProtectionRecord through
// the check the DB-backed ingest stage applies to every push:
// protojson-marshal the payload, then ValidatePayload against the
// registered schema for the record's kind and schema_version. The
// in-memory platform the other tests push to only checks registration
// (#2815).
func validatePushed(t *testing.T, s githubrepo.ProtectionState) (map[string]any, error) {
	t.Helper()
	reg, err := schemaregistry.NewEmbedded(schemaregistry.PlatformSchemasFS())
	if err != nil {
		t.Fatalf("NewEmbedded: %v", err)
	}
	rec, err := buildRepoProtectionRecord(s, "example", "prod", "scf:TDA-06")
	if err != nil {
		t.Fatalf("buildRepoProtectionRecord: %v", err)
	}
	if rec.GetEvidenceKind() != "github.repo_protection.v2" || rec.GetSchemaVersion() != "2.0.0" {
		t.Fatalf("kind/version = %s/%s; want github.repo_protection.v2/2.0.0", rec.GetEvidenceKind(), rec.GetSchemaVersion())
	}
	payloadJSON, err := protojson.Marshal(rec.GetPayload())
	if err != nil {
		t.Fatalf("protojson.Marshal: %v", err)
	}
	return rec.GetPayload().AsMap(),
		reg.ValidatePayload(context.Background(), tenantA, rec.GetEvidenceKind(), rec.GetSchemaVersion(), payloadJSON)
}

// TestBuildRepoProtectionRecord_ConformsAndOmitsUnread covers the record
// shapes the connector emits. Each must validate, and no unread field may
// appear in the payload as 0 or false (#2925).
func TestBuildRepoProtectionRecord_ConformsAndOmitsUnread(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	protection := []string{
		"required_reviews", "require_code_owner_reviews", "require_signed_commits",
		"require_linear_history", "enforce_admins", "protection_source",
	}
	cases := map[string]struct {
		in      githubrepo.ProtectionState
		present []string
		absent  []string
	}{
		"fetch_failed": {
			in: githubrepo.ProtectionState{
				RepoID: 1296269, RepoFullName: "octocat/Hello-World", DefaultBranch: "master",
				Result: githubrepo.ResultInconclusive, Reason: "branch protection: github: HTTP 502", ObservedAt: now,
			},
			present: []string{"repo_id", "repo_full_name", "default_branch", "reason"},
			absent:  protection,
		},
		"ruleset_bypass_withheld": {
			in: githubrepo.ProtectionState{
				RepoID: 1296269, RepoFullName: "octocat/Hello-World", DefaultBranch: "master",
				RequiredReviews: intp(2), RequireCodeOwnerReviews: boolp(true), RequireSignedCommits: boolp(false),
				RequireLinearHistory: boolp(false), ProtectionSource: githubrepo.SourceRulesets,
				Result: githubrepo.ResultPass, Reason: "required_reviews=2; admin enforcement unknown", ObservedAt: now,
			},
			present: []string{"repo_id", "required_reviews", "protection_source", "reason"},
			absent:  []string{"enforce_admins"},
		},
		"unprotected": {
			in: githubrepo.ProtectionState{
				RepoID: 7, RepoFullName: "octocat/open", DefaultBranch: "main",
				RequiredReviews: intp(0), RequireCodeOwnerReviews: boolp(false), RequireSignedCommits: boolp(false),
				RequireLinearHistory: boolp(false), EnforceAdmins: boolp(false), ProtectionSource: githubrepo.SourceNone,
				Result: githubrepo.ResultFail, Reason: "no branch protection or ruleset on default branch", ObservedAt: now,
			},
			present: append([]string{"repo_id"}, protection...),
		},
		"both_sources_all_read": {
			in: githubrepo.ProtectionState{
				RepoID: 8, RepoFullName: "octocat/api", DefaultBranch: "main",
				RequiredReviews: intp(3), RequireCodeOwnerReviews: boolp(true), RequireSignedCommits: boolp(true),
				RequireLinearHistory: boolp(true), EnforceAdmins: boolp(true), ProtectionSource: githubrepo.SourceBoth,
				Result: githubrepo.ResultPass, Reason: "required_reviews=3", ObservedAt: now,
			},
			present: append([]string{"repo_id"}, protection...),
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			payload, err := validatePushed(t, tc.in)
			if err != nil {
				t.Fatalf("ingest schema validation rejected the connector payload: %v", err)
			}
			for _, k := range tc.present {
				if _, ok := payload[k]; !ok {
					t.Errorf("payload missing %q: %v", k, payload)
				}
			}
			for _, k := range tc.absent {
				if v, ok := payload[k]; ok {
					t.Errorf("payload carries unread %q = %v", k, v)
				}
			}
		})
	}
}

// A v1-shaped payload (no repo_id) must not validate as v2, so a record
// cannot claim the new kind without the stable id.
func TestRepoProtectionV2_RequiresRepoID(t *testing.T) {
	reg, err := schemaregistry.NewEmbedded(schemaregistry.PlatformSchemasFS())
	if err != nil {
		t.Fatalf("NewEmbedded: %v", err)
	}
	v1Shaped := []byte(`{"repo_full_name":"octocat/Hello-World","default_branch":"master","required_reviews":1,"enforce_admins":true}`)
	if err := reg.ValidatePayload(context.Background(), tenantA, "github.repo_protection.v2", "2.0.0", v1Shaped); err == nil {
		t.Fatal("v2 accepted a payload without repo_id")
	}
	if err := reg.ValidatePayload(context.Background(), tenantA, "github.repo_protection.v1", "1.0.0", v1Shaped); err != nil {
		t.Fatalf("v1 stays registered and must still accept its payload: %v", err)
	}
}
