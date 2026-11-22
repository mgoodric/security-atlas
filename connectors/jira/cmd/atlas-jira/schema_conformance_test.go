package main

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/mgoodric/security-atlas/internal/api/schemaregistry"

	"github.com/mgoodric/security-atlas/connectors/jira/internal/jiratickets"
	"github.com/mgoodric/security-atlas/connectors/jira/internal/lineartickets"
)

const conformanceTenant = "11111111-1111-1111-1111-111111111111"

// #2829: every shape the connector builds at schema 1.1.0 passes the
// check the DB-backed ingest stage applies to a push: protojson-marshal
// the payload, then ValidatePayload against the registered schema for the
// record's kind and schema_version. The in-memory server the other tests
// push to only checks registration.
func TestBuildTicketRecord_PayloadConformsTo110(t *testing.T) {
	t.Parallel()
	reg, err := schemaregistry.NewEmbedded(schemaregistry.PlatformSchemasFS())
	if err != nil {
		t.Fatalf("NewEmbedded: %v", err)
	}
	obs := time.Date(2026, 11, 21, 14, 30, 0, 0, time.UTC)
	created := time.Date(2026, 11, 1, 9, 0, 0, 123_000_000, time.UTC)
	resolved := time.Date(2026, 11, 5, 17, 0, 0, 0, time.UTC)
	jira := map[string]jiratickets.Ticket{
		"all_fields": {
			TicketKey: "VULN-1", ProjectKey: "VULN", Summary: "CVE", Status: "Done", Resolution: "Done",
			Assignee: "Emma", AssigneeAccountID: "5b10ac8d82e05b22cc7d4ef5", URL: "https://acme.atlassian.net/browse/VULN-1",
			Labels: []string{"vulnerability"}, Priority: "Highest", Severity: "critical",
			CreatedAt: &created, ResolvedAt: &resolved, ObservedAt: obs,
		},
		"open_unassigned": {
			TicketKey: "SEC-2", Status: "To Do", Labels: []string{"security-training"},
			Priority: "Major", CreatedAt: &created, ObservedAt: obs,
		},
		"bare": {TicketKey: "CR-3", Status: "Open", ObservedAt: obs},
	}
	for name, tk := range jira {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rec, err := buildJiraTicketRecord(tk, "prod", "")
			if err != nil {
				t.Fatalf("buildJiraTicketRecord: %v", err)
			}
			validate(t, reg, rec.GetEvidenceKind(), rec.GetSchemaVersion(), rec.GetPayload())
			p := rec.GetPayload().AsMap()
			if _, ok := p["severity"]; ok != (tk.Severity != "") {
				t.Errorf("severity present = %v, want %v", ok, tk.Severity != "")
			}
			if _, ok := p["resolved_at"]; ok != (tk.ResolvedAt != nil) {
				t.Errorf("resolved_at present = %v, want %v", ok, tk.ResolvedAt != nil)
			}
			if _, ok := p["labels"]; ok != (len(tk.Labels) > 0) {
				t.Errorf("labels present = %v, want %v", ok, len(tk.Labels) > 0)
			}
			if tk.CreatedAt != nil && p["created_at"] != "2026-11-01T09:00:00.123Z" {
				t.Errorf("created_at = %v, want 2026-11-01T09:00:00.123Z", p["created_at"])
			}
		})
	}
	t.Run("linear", func(t *testing.T) {
		t.Parallel()
		rec, err := buildLinearTicketRecord(lineartickets.Ticket{TicketKey: "ENG-4", Status: "Done", ObservedAt: obs}, "prod", "")
		if err != nil {
			t.Fatalf("buildLinearTicketRecord: %v", err)
		}
		validate(t, reg, rec.GetEvidenceKind(), rec.GetSchemaVersion(), rec.GetPayload())
	})
}

func validate(t *testing.T, reg *schemaregistry.Service, kind, version string, payload *structpb.Struct) {
	t.Helper()
	if kind != "jira.ticket_evidence.v1" || version != "1.1.0" {
		t.Fatalf("kind/version = %s/%s; want jira.ticket_evidence.v1/1.1.0", kind, version)
	}
	body, err := protojson.Marshal(payload)
	if err != nil {
		t.Fatalf("protojson.Marshal: %v", err)
	}
	if err := reg.ValidatePayload(context.Background(), conformanceTenant, kind, version, body); err != nil {
		t.Fatalf("ingest schema validation rejected the connector payload: %v", err)
	}
}
