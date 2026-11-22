package policy

import (
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// #2877 pure-Go branches of property drift: which page properties differ
// from a synced row. The store path is in
// internal/api/policies/sync_property_drift_2877_integration_test.go.

func TestSyncPropertyDrift(t *testing.T) {
	t.Parallel()
	day := func(s string) time.Time {
		d, err := time.Parse("2006-01-02", s)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	review := day("2026-03-01")
	in := ImportInput{
		Title:                       "Access Control Policy",
		Version:                     "3.2",
		OwnerRole:                   "security_lead",
		ApproverRole:                "ciso",
		HistoricalApproverName:      "Jane Doe",
		HistoricalApprovedAt:        day("2025-03-01"),
		EffectiveDate:               day("2025-03-15"),
		NextReviewAt:                &review,
		AcknowledgmentRequiredRoles: []string{"employee", "contractor"},
	}
	name := "Jane Doe"
	row := dbx.Policy{
		Title:                       in.Title,
		Version:                     in.Version,
		OwnerRole:                   in.OwnerRole,
		ApproverRole:                in.ApproverRole,
		HistoricalApproverName:      &name,
		HistoricalApprovedAt:        pgtype.Date{Time: day("2025-03-01"), Valid: true},
		EffectiveDate:               pgtype.Date{Time: day("2025-03-15"), Valid: true},
		NextReviewAt:                pgtype.Timestamptz{Time: review, Valid: true},
		AcknowledgmentRequiredRoles: []string{"contractor", "employee"},
	}
	if got := syncPropertyDrift(row, in); got != nil {
		t.Fatalf("in-step row drift = %v, want nil (audience order ignored)", got)
	}

	later := day("2026-09-01")
	for name, c := range map[string]struct {
		mut  func(*ImportInput)
		want []string
	}{
		"title":         {func(i *ImportInput) { i.Title = "Access Policy" }, []string{"title"}},
		"approver name": {func(i *ImportInput) { i.HistoricalApproverName = "John Roe" }, []string{"approved_by_name"}},
		"approved at":   {func(i *ImportInput) { i.HistoricalApprovedAt = day("2025-04-01") }, []string{"approved_at"}},
		"effective":     {func(i *ImportInput) { i.EffectiveDate = day("2025-04-15") }, []string{"effective_date"}},
		"review moved":  {func(i *ImportInput) { i.NextReviewAt = &later }, []string{"next_review_at"}},
		"review gone":   {func(i *ImportInput) { i.NextReviewAt = nil }, []string{"next_review_at"}},
		"owner":         {func(i *ImportInput) { i.OwnerRole = "it_lead" }, []string{"owner_role"}},
		"approver role": {func(i *ImportInput) { i.ApproverRole = "cto" }, []string{"approver_role"}},
		"audience":      {func(i *ImportInput) { i.AcknowledgmentRequiredRoles = []string{"everyone"} }, []string{"acknowledgment_required_roles"}},
		"several, sorted": {func(i *ImportInput) {
			i.OwnerRole = "it_lead"
			i.NextReviewAt = &later
			i.ApproverRole = "cto"
		}, []string{"approver_role", "next_review_at", "owner_role"}},
		// A version or body change is not property drift.
		"version and body": {func(i *ImportInput) { i.Version = "3.3"; i.BodyMd = "other" }, nil},
	} {
		changed := in
		changed.AcknowledgmentRequiredRoles = slices.Clone(in.AcknowledgmentRequiredRoles)
		c.mut(&changed)
		if got := syncPropertyDrift(row, changed); !slices.Equal(got, c.want) {
			t.Errorf("%s: drift = %v, want %v", name, got, c.want)
		}
	}

	// A row with no review date matches a page with none.
	noReview := row
	noReview.NextReviewAt = pgtype.Timestamptz{}
	inNoReview := in
	inNoReview.NextReviewAt = nil
	if got := syncPropertyDrift(noReview, inNoReview); got != nil {
		t.Errorf("no review date on either side: drift = %v", got)
	}
}

func TestSyncDriftWhat(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		body bool
		keys []string
		want string
	}{
		{true, nil, "body"},
		{false, []string{"owner_role"}, "property owner_role"},
		{false, []string{"next_review_at", "owner_role"}, "properties next_review_at, owner_role"},
		{true, []string{"owner_role"}, "body and property owner_role"},
	} {
		if got := syncDriftWhat(c.body, c.keys); got != c.want {
			t.Errorf("syncDriftWhat(%v, %v) = %q, want %q", c.body, c.keys, got, c.want)
		}
	}
}
