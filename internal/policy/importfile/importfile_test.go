package importfile

import (
	"strings"
	"testing"
	"time"
)

const validFile = `---
title: Information Security Policy
version: "3.2"
approved_by_name: Jane Doe
approved_at: 2025-03-01
effective_date: "2025-03-15"
next_review_at: 2026-03-01
owner_role: security_lead
source_url: https://wiki.example.com/isp
linked_controls: [iac-01, " 2b9c0d1e-0000-4000-8000-000000000001 "]
acknowledgment_required_roles: [all_staff]
---
# Purpose

Protect information.
`

func TestParse_Valid(t *testing.T) {
	t.Parallel()
	in, err := Parse("isp.md", []byte(validFile))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if in.SourceFile != "isp.md" || in.Title != "Information Security Policy" || in.Version != "3.2" {
		t.Errorf("identity fields = %q %q %q", in.SourceFile, in.Title, in.Version)
	}
	if in.HistoricalApproverName != "Jane Doe" {
		t.Errorf("approver = %q", in.HistoricalApproverName)
	}
	if want := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC); !in.HistoricalApprovedAt.Equal(want) {
		t.Errorf("approved_at = %v", in.HistoricalApprovedAt)
	}
	if want := time.Date(2025, 3, 15, 0, 0, 0, 0, time.UTC); !in.EffectiveDate.Equal(want) {
		t.Errorf("effective_date = %v", in.EffectiveDate)
	}
	if in.NextReviewAt == nil || in.NextReviewAt.Format(dateLayout) != "2026-03-01" {
		t.Errorf("next_review_at = %v", in.NextReviewAt)
	}
	if in.OwnerRole != "security_lead" || in.ApproverRole != DefaultRole {
		t.Errorf("roles = %q %q", in.OwnerRole, in.ApproverRole)
	}
	if in.SourceURL != "https://wiki.example.com/isp" {
		t.Errorf("source_url = %q", in.SourceURL)
	}
	if len(in.LinkedControlRefs) != 2 || in.LinkedControlRefs[1] != "2b9c0d1e-0000-4000-8000-000000000001" {
		t.Errorf("linked controls = %q", in.LinkedControlRefs)
	}
	if !strings.HasPrefix(in.BodyMd, "# Purpose") || strings.HasSuffix(in.BodyMd, "\n") {
		t.Errorf("body = %q", in.BodyMd)
	}
}

func TestParse_MinimalCRLF(t *testing.T) {
	t.Parallel()
	src := "---\r\ntitle: T\r\nversion: 1\r\napproved_by_name: A\r\napproved_at: 2024-01-02\r\neffective_date: 2024-01-03\r\n---\r\nBody\r\n"
	in, err := Parse("t.md", []byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if in.Version != "1" || in.BodyMd != "Body" || in.NextReviewAt != nil || in.SourceURL != "" {
		t.Errorf("got %+v", in)
	}
	if in.OwnerRole != DefaultRole || in.ApproverRole != DefaultRole {
		t.Errorf("roles = %q %q", in.OwnerRole, in.ApproverRole)
	}
}

func TestParse_Failures(t *testing.T) {
	t.Parallel()
	replace := func(old, new string) string { return strings.Replace(validFile, old, new, 1) }
	cases := []struct {
		name, src, wantErr string
	}{
		{"no front matter", "# Just markdown\n", "missing front matter"},
		{"unterminated", "---\ntitle: x\n", "unterminated front matter"},
		{"empty front matter", "---\n---\nbody\n", "front matter is empty"},
		{"missing approved_at", replace("approved_at: 2025-03-01\n", ""), `missing required front matter "approved_at"`},
		{"missing title", replace("title: Information Security Policy\n", ""), `"title"`},
		{"missing approver", replace("approved_by_name: Jane Doe\n", ""), `"approved_by_name"`},
		{"missing effective", replace("effective_date: \"2025-03-15\"\n", ""), `"effective_date"`},
		{"bad approved_at", replace("approved_at: 2025-03-01", "approved_at: March 2025"), "approved_at \"March 2025\" is not a date"},
		{"bad effective", replace("effective_date: \"2025-03-15\"", "effective_date: 2025-13-40"), "effective_date"},
		{"bad next review", replace("next_review_at: 2026-03-01", "next_review_at: soon"), "next_review_at"},
		{"unknown key", replace("owner_role: security_lead", "owner: security_lead"), "field owner not found"},
		{"javascript url", replace("https://wiki.example.com/isp", "javascript:alert(1)"), "source_url"},
		{"relative url", replace("https://wiki.example.com/isp", "/wiki/isp"), "source_url"},
		{"empty body", "---\ntitle: T\nversion: 1\napproved_by_name: A\napproved_at: 2024-01-02\neffective_date: 2024-01-03\n---\n", "policy text"},
		{"bad yaml", "---\ntitle: [unclosed\n---\nbody\n", "front matter:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Parse("f.md", []byte(tc.src))
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestCheckSourceURL(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"https://a.example/x", "http://a.example"} {
		if err := CheckSourceURL(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"data:text/html,x", "ftp://a.example", "https://", "a.example/x"} {
		if err := CheckSourceURL(bad); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
}
