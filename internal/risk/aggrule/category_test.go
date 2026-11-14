package aggrule

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// #1813: the meta-risk category is the first scored child's active
// category, otherwise the tenant's default; never a hard-coded value.
func TestMetaRiskCategoriesPick(t *testing.T) {
	t.Parallel()
	privacy, financial, custom := uuid.New(), uuid.New(), uuid.New()
	rows := []dbx.OrgRiskCategory{
		{ID: pgUUID(privacy), Slug: "privacy", Archived: true},
		{ID: pgUUID(financial), Slug: "financial", IsDefault: true},
		{ID: pgUUID(custom), Slug: "cloud-security"},
	}
	child := func(id uuid.UUID, cat dbx.RiskCategory) dbx.Risk {
		return dbx.Risk{CategoryID: pgUUID(id), Category: cat}
	}
	regulatory := uuid.New()
	withActive := make([]dbx.OrgRiskCategory, 0, len(rows)+1)
	withActive = append(withActive, rows...)
	withActive = append(withActive, dbx.OrgRiskCategory{ID: pgUUID(regulatory), Slug: "regulatory"})

	cases := []struct {
		name   string
		rows   []dbx.OrgRiskCategory
		scored []dbx.Risk
		want   dbx.RiskCategory
		err    bool
	}{
		{"first active child wins", withActive,
			[]dbx.Risk{child(privacy, "privacy"), child(regulatory, "regulatory")}, "regulatory", false},
		{"all children archived takes the default", rows,
			[]dbx.Risk{child(privacy, "privacy")}, "financial", false},
		{"no scored children takes the default", rows, nil, "financial", false},
		{"child without a reference takes the default", rows,
			[]dbx.Risk{{Category: "integrity", CategoryID: pgtype.UUID{}}}, "financial", false},
		{"custom default keeps the first child", []dbx.OrgRiskCategory{
			{ID: pgUUID(privacy), Slug: "privacy", Archived: true},
			{ID: pgUUID(custom), Slug: "cloud-security", IsDefault: true},
		}, []dbx.Risk{child(privacy, "privacy")}, "privacy", false},
		{"nothing to take a category from", nil, nil, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := categoriesFromRows(tc.rows).pick(tc.scored)
			if tc.err {
				if !errors.Is(err, errNoMetaRiskCategory) {
					t.Fatalf("pick: err = %v, want errNoMetaRiskCategory", err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("pick = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}
