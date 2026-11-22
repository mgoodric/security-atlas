package schemaregistry

import (
	"context"
	"io/fs"
	"testing"
)

func readJiraTicket(t *testing.T, v string) []byte {
	t.Helper()
	b, err := fs.ReadFile(PlatformSchemasFS(), "jira.ticket_evidence/"+v+".json")
	if err != nil {
		t.Fatalf("read %s: %v", v, err)
	}
	return b
}

// #2829: jira.ticket_evidence 1.1.0 adds assignee_account_id, labels,
// priority, severity, created_at and resolved_at, and must stay additive
// over 1.0.0, so records already pushed at 1.0.0 stay valid under kind .v1.
func TestJiraTicketEvidence110_AdditiveOver100(t *testing.T) {
	t.Parallel()
	if err := CheckAdditiveOver(readJiraTicket(t, "1.0.0"), readJiraTicket(t, "1.1.0")); err != nil {
		t.Fatalf("1.1.0 is not additive over 1.0.0: %v", err)
	}
}

func TestJiraTicketEvidence110_Validation(t *testing.T) {
	t.Parallel()
	reg, err := NewEmbedded(PlatformSchemasFS())
	if err != nil {
		t.Fatalf("NewEmbedded: %v", err)
	}
	const tenant = "11111111-1111-1111-1111-111111111111"
	const id = `"ticket_key":"VULN-1","status":"Done"`
	cases := []struct {
		name    string
		version string
		payload string
		ok      bool
	}{
		{"all_new_fields", "1.1.0", `{` + id + `,"assignee_account_id":"5b10a","labels":["vulnerability"],"priority":"Highest","severity":"critical","created_at":"2026-11-01T09:00:00.123Z","resolved_at":"2026-11-05T17:00:00Z"}`, true},
		{"none_of_the_new_fields", "1.1.0", `{` + id + `}`, true},
		{"unknown_severity", "1.1.0", `{` + id + `,"severity":"Critical"}`, false},
		{"labels_not_array", "1.1.0", `{` + id + `,"labels":"vulnerability"}`, false},
		{"empty_label", "1.1.0", `{` + id + `,"labels":[""]}`, false},
		{"empty_priority", "1.1.0", `{` + id + `,"priority":""}`, false},
		{"empty_account_id", "1.1.0", `{` + id + `,"assignee_account_id":""}`, false},
		{"v100_rejects_new_fields", "1.0.0", `{` + id + `,"labels":["vulnerability"]}`, false},
		{"v100_payload_under_v110", "1.1.0", `{` + id + `,"project_key":"VULN","resolution":"Done","assignee":"Emma"}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := reg.ValidatePayload(context.Background(), tenant, "jira.ticket_evidence.v1", tc.version, []byte(tc.payload))
			if tc.ok && err != nil {
				t.Fatalf("want valid, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("want rejected, got valid")
			}
		})
	}
}
