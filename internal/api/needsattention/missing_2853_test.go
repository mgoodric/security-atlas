package needsattention

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// #2853 — a synced policy whose source page is missing is a
// source_missing item, owned by its owner role, dated from when the page
// was first found missing, and ranked after approvals and before overdue.
func TestMissingSourceItems(t *testing.T) {
	owned, unowned := uuid.New(), uuid.New()
	rows := []dbx.ListMissingSyncedPoliciesRow{
		{ID: pgtype.UUID{Bytes: owned, Valid: true}, Title: "Access Control", OwnerRole: "security_lead",
			SyncMissingSince: pgtype.Timestamptz{Time: *days(-3), Valid: true}},
		{ID: pgtype.UUID{Bytes: unowned, Valid: true}, Title: "Backup",
			SyncMissingSince: pgtype.Timestamptz{Time: *days(-1), Valid: true}},
	}
	items := MissingSourceItems(rows)
	if len(items) != 2 {
		t.Fatalf("items = %+v", items)
	}
	it := items[0]
	if it.Band != BandSourceMissing || it.Type != "policy" || it.ID != owned.String() || it.Title != "Access Control" ||
		it.Link != "/policies/"+owned.String() || it.Action == "" || it.Since == nil || !it.Since.Equal(*days(-3)) ||
		it.Owner == nil || it.Owner.Role != "security_lead" {
		t.Errorf("owned item = %+v", it)
	}
	if items[1].Owner != nil {
		t.Errorf("item for a policy with no owner role has owner %+v", items[1].Owner)
	}

	ranked := Rank(append([]Item{
		{Band: BandOverdue, Type: "risk", ID: "r", DueAt: days(-30)},
		{Band: BandApproval, Type: "exception", ID: "a", Since: days(0)},
	}, items...), ForAll, func(Item) bool { return true })
	want := []string{"approval/a", "source_missing/" + owned.String(), "source_missing/" + unowned.String(), "overdue/r"}
	if got := ids(ranked); !equal(got, want) {
		t.Errorf("rank = %v, want %v", got, want)
	}
	if c := Page(ranked, ForAll, 10, 0).Counts[BandSourceMissing]; c != 2 {
		t.Errorf("counts[source_missing] = %d, want 2", c)
	}
}
