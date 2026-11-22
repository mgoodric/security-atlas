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
	rec, err := buildRepoProtectionRecord(s, "example", "prod", "scf:TDA-06", defaultProductionTopic)
	if err != nil {
		t.Fatalf("buildRepoProtectionRecord: %v", err)
	}
	if rec.GetEvidenceKind() != "github.repo_protection.v2" || rec.GetSchemaVersion() != "2.1.0" {
		t.Fatalf("kind/version = %s/%s; want github.repo_protection.v2/2.1.0", rec.GetEvidenceKind(), rec.GetSchemaVersion())
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
		"required_status_checks", "allow_force_pushes",
	}
	topics := func(t ...string) *[]string { return &t }
	checks := func(c ...string) *[]string { return &c }
	cases := map[string]struct {
		in      githubrepo.ProtectionState
		present []string
		absent  []string
	}{
		"fetch_failed": {
			in: githubrepo.ProtectionState{
				RepoID: 1296269, RepoFullName: "octocat/Hello-World", DefaultBranch: "master",
				Topics: topics("iac"),
				Result: githubrepo.ResultInconclusive, Reason: "branch protection: github: HTTP 502", ObservedAt: now,
			},
			present: []string{"repo_id", "repo_full_name", "default_branch", "reason", "topics", "deploys_to_production"},
			absent:  protection,
		},
		"topics_not_listed": {
			in: githubrepo.ProtectionState{
				RepoID: 9, RepoFullName: "octocat/quiet", DefaultBranch: "main",
				RequiredReviews: intp(1), RequiredStatusChecks: checks(), AllowForcePushes: boolp(false),
				ProtectionSource: githubrepo.SourceClassic, Result: githubrepo.ResultPass, ObservedAt: now,
			},
			present: []string{"required_status_checks", "allow_force_pushes"},
			absent:  []string{"topics", "deploys_to_production"},
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
				RequiredStatusChecks: checks(), AllowForcePushes: boolp(true), Topics: topics(),
				Result: githubrepo.ResultFail, Reason: "no branch protection or ruleset on default branch", ObservedAt: now,
			},
			present: append([]string{"repo_id"}, protection...),
		},
		"both_sources_all_read": {
			in: githubrepo.ProtectionState{
				RepoID: 8, RepoFullName: "octocat/api", DefaultBranch: "main",
				RequiredReviews: intp(3), RequireCodeOwnerReviews: boolp(true), RequireSignedCommits: boolp(true),
				RequireLinearHistory: boolp(true), EnforceAdmins: boolp(true), ProtectionSource: githubrepo.SourceBoth,
				RequiredStatusChecks: checks("ci/build", "ci/test"), AllowForcePushes: boolp(false), Topics: topics("production", "iac"),
				Result: githubrepo.ResultPass, Reason: "required_reviews=3", ObservedAt: now,
			},
			present: append([]string{"repo_id", "topics", "deploys_to_production"}, protection...),
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

// #2827: deploys_to_production is whether the listed topics include the
// --production-topic, and travels with topics.
func TestBuildRepoProtectionRecord_DeploysToProduction(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		topics []string
		topic  string
		want   bool
	}{
		{[]string{"production", "iac"}, defaultProductionTopic, true},
		{[]string{"iac"}, defaultProductionTopic, false},
		{[]string{}, defaultProductionTopic, false},
		{[]string{"prod"}, "prod", true},
		{[]string{"production"}, "prod", false},
	}
	for _, tc := range cases {
		s := githubrepo.ProtectionState{
			RepoID: 1, RepoFullName: "o/r", DefaultBranch: "main", Topics: &tc.topics,
			Result: githubrepo.ResultInconclusive, ObservedAt: now,
		}
		rec, err := buildRepoProtectionRecord(s, "o", "prod", "", tc.topic)
		if err != nil {
			t.Fatalf("buildRepoProtectionRecord: %v", err)
		}
		got, ok := rec.GetPayload().AsMap()["deploys_to_production"]
		if !ok || got != tc.want {
			t.Errorf("topics %v, --production-topic %q: deploys_to_production = %v (present %v); want %v", tc.topics, tc.topic, got, ok, tc.want)
		}
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
