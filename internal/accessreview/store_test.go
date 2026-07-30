package accessreview

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestParseManualCSVAppliesScope(t *testing.T) {
	csv := strings.NewReader(`system,entitlement,user_id,email,source_ref
github,admin,u1,u1@example.test,team-admins
github,read,u2,u2@example.test,team-read
aws,admin,u3,u3@example.test,iam-admin
`)
	items, err := parseManualCSV(csv, Scope{Systems: []string{"github"}, Entitlements: []string{"admin"}})
	if err != nil {
		t.Fatalf("parseManualCSV: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	if got := items[0]; got.Source != SourceManualCSV || got.System != "github" || got.Entitlement != "admin" || got.PrincipalUserID != "u1" {
		t.Fatalf("unexpected item: %+v", got)
	}
}

func TestParseManualCSVRequiresColumns(t *testing.T) {
	_, err := parseManualCSV(strings.NewReader("system,user_id\ngithub,u1\n"), Scope{})
	if err == nil || !strings.Contains(err.Error(), "entitlement") {
		t.Fatalf("err = %v, want missing entitlement", err)
	}
}

func TestCleanUniqueEmptyIsNonNil(t *testing.T) {
	if cleanUnique(nil) == nil {
		t.Fatal("cleanUnique(nil) = nil, want non-nil empty slice (pgx encodes nil as SQL NULL, violating the NOT NULL scope columns)")
	}
}

func TestWriteRevokeCSV(t *testing.T) {
	attested := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	var buf bytes.Buffer
	err := WriteRevokeCSV(&buf, []RevokeDecision{{
		ItemID:          uuid.New(),
		System:          "github",
		Entitlement:     "admin",
		PrincipalUserID: "u1",
		PrincipalEmail:  "u1@example.test",
		ReviewerID:      "reviewer-a",
		Reason:          "No longer on the platform team",
		AttestedAt:      attested,
	}})
	if err != nil {
		t.Fatalf("WriteRevokeCSV: %v", err)
	}
	got := buf.String()
	want := "system,entitlement,principal_user_id,principal_email,reviewer_id,reason,attested_at\n" +
		"github,admin,u1,u1@example.test,reviewer-a,No longer on the platform team,2026-07-30T12:00:00Z\n"
	if got != want {
		t.Fatalf("csv = %q, want %q", got, want)
	}
}

func TestWriteRevokeCSVEmptyListStillWritesHeader(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteRevokeCSV(&buf, nil); err != nil {
		t.Fatalf("WriteRevokeCSV: %v", err)
	}
	if !strings.HasPrefix(buf.String(), "system,entitlement,") || strings.Count(buf.String(), "\n") != 1 {
		t.Fatalf("csv = %q, want header only", buf.String())
	}
}
