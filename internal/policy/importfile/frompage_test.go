package importfile

import (
	"strings"
	"testing"
	"time"
)

// #2850: a synced page is held to the import's rules, and its errors name
// page properties and the page body rather than front matter.
func TestFromPage(t *testing.T) {
	t.Parallel()
	valid := Fields{
		Title:          "Access Control Policy",
		Version:        "3.2",
		ApprovedByName: "Jane Doe",
		ApprovedAt:     "2025-03-01",
		EffectiveDate:  "2025-03-15",
		NextReviewAt:   "2026-03-01",
		SourceURL:      "https://acme.atlassian.net/wiki/spaces/SEC/pages/1001",
	}
	in, err := FromPage("1001", valid, "  # Access\n\nMFA.  ")
	if err != nil {
		t.Fatalf("FromPage: %v", err)
	}
	if in.SourceFile != "1001" || in.BodyMd != "# Access\n\nMFA." || in.OwnerRole != DefaultRole ||
		in.SourceURL != valid.SourceURL || in.NextReviewAt == nil ||
		!in.HistoricalApprovedAt.Equal(time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("FromPage = %+v", in)
	}

	for name, c := range map[string]struct {
		edit func(*Fields)
		body string
		name string
		want string
	}{
		"missing approver": {func(f *Fields) { f.ApprovedByName = " " }, "Body.", "1", `missing required page property "approved_by_name"`},
		"empty body":       {func(*Fields) {}, "  ", "1", "page body is empty"},
		"long page id":     {func(*Fields) {}, "Body.", strings.Repeat("9", 501), "page id is longer than 500 characters"},
		"bad url":          {func(f *Fields) { f.SourceURL = "javascript:alert(1)" }, "Body.", "1", "must be an absolute http or https URL"},
		"bad review date":  {func(f *Fields) { f.NextReviewAt = "soon" }, "Body.", "1", `next_review_at "soon" is not a date`},
	} {
		f := valid
		c.edit(&f)
		_, err := FromPage(c.name, f, c.body)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want it to contain %q", name, err, c.want)
		}
	}
}
