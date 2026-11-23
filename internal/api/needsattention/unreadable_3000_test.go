package needsattention

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// #3000 — a synced policy whose source page the latest run carried as
// unreadable is a source_unreadable item, owned by its owner role, dated
// from when a run first carried it so, and ranked after source_missing
// and before inconclusive.
func TestUnreadableSourceItems(t *testing.T) {
	owned, unowned, gone := uuid.New(), uuid.New(), uuid.New()
	items := UnreadableSourceItems([]dbx.ListUnreadableSyncedPoliciesRow{
		{ID: pgtype.UUID{Bytes: owned, Valid: true}, Title: "Access Control", OwnerRole: "security_lead",
			SyncUnreadableSince: pgtype.Timestamptz{Time: *days(-3), Valid: true}},
		{ID: pgtype.UUID{Bytes: unowned, Valid: true}, Title: "Backup",
			SyncUnreadableSince: pgtype.Timestamptz{Time: *days(-1), Valid: true}},
	})
	if len(items) != 2 {
		t.Fatalf("items = %+v", items)
	}
	it := items[0]
	if it.Band != BandSourceUnreadable || it.Type != "policy" || it.ID != owned.String() || it.Title != "Access Control" ||
		it.Link != "/policies/"+owned.String() || it.Action == "" || it.Since == nil || !it.Since.Equal(*days(-3)) ||
		it.Owner == nil || it.Owner.Role != "security_lead" {
		t.Errorf("owned item = %+v", it)
	}
	if items[1].Owner != nil {
		t.Errorf("item for a policy with no owner role has owner %+v", items[1].Owner)
	}

	ranked := Rank(append([]Item{
		{Band: BandInconclusive, Type: "control", ID: "c", Since: days(-90)},
		{Band: BandSourceMissing, Type: "policy", ID: gone.String(), Since: days(0)},
	}, items...), ForAll, func(Item) bool { return true })
	want := []string{"source_missing/" + gone.String(), "source_unreadable/" + owned.String(), "source_unreadable/" + unowned.String(), "inconclusive/c"}
	if got := ids(ranked); !equal(got, want) {
		t.Errorf("rank = %v, want %v", got, want)
	}
	if c := Page(ranked, ForAll, 10, 0).Counts[BandSourceUnreadable]; c != 2 {
		t.Errorf("counts[source_unreadable] = %d, want 2", c)
	}
}
