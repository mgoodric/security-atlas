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
// #1819: a custom category can be the default.
func TestMetaRiskCategoriesPick(t *testing.T) {
	t.Parallel()
	privacy, financial, custom := uuid.New(), uuid.New(), uuid.New()
	rows := []dbx.OrgRiskCategory{
		{ID: pgUUID(privacy), Slug: "privacy", Archived: true},
		{ID: pgUUID(financial), Slug: "financial", IsDefault: true},
		{ID: pgUUID(custom), Slug: "cloud-security"},
	}
	child := func(id uuid.UUID) dbx.Risk {
		return dbx.Risk{CategoryID: pgUUID(id)}
	}
	regulatory := uuid.New()
	withActive := make([]dbx.OrgRiskCategory, 0, len(rows)+1)
	withActive = append(withActive, rows...)
	withActive = append(withActive, dbx.OrgRiskCategory{ID: pgUUID(regulatory), Slug: "regulatory"})

	cases := []struct {
		name   string
		rows   []dbx.OrgRiskCategory
		scored []dbx.Risk
		want   uuid.UUID
		err    bool
	}{
		{"first active child wins", withActive,
			[]dbx.Risk{child(privacy), child(regulatory)}, regulatory, false},
		{"all children archived takes the default", rows,
			[]dbx.Risk{child(privacy)}, financial, false},
		{"no scored children takes the default", rows, nil, financial, false},
		{"child without a reference takes the default", rows,
			[]dbx.Risk{{CategoryID: pgtype.UUID{}}}, financial, false},
		{"custom default is used", []dbx.OrgRiskCategory{
			{ID: pgUUID(privacy), Slug: "privacy", Archived: true},
			{ID: pgUUID(custom), Slug: "cloud-security", IsDefault: true},
		}, []dbx.Risk{child(privacy)}, custom, false},
		{"no default keeps the first child", []dbx.OrgRiskCategory{
			{ID: pgUUID(privacy), Slug: "privacy", Archived: true},
		}, []dbx.Risk{child(privacy)}, privacy, false},
		{"nothing to take a category from", nil, nil, uuid.Nil, true},
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
			if err != nil || !got.Valid || uuid.UUID(got.Bytes) != tc.want {
				t.Fatalf("pick = %v, %v; want %s", got, err, tc.want)
			}
		})
	}
}
