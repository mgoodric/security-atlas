package frameworkscope

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

func TestPriorFrameworkVersions(t *testing.T) {
	t.Parallel()
	a, b, c, d, e := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	pair := func(to, from uuid.UUID) dbx.ListFrameworkVersionMigrationSourcesRow {
		return dbx.ListFrameworkVersionMigrationSourcesRow{ToVersionID: pgUUID(to), FromVersionID: pgUUID(from)}
	}
	rows := []dbx.ListFrameworkVersionMigrationSourcesRow{
		pair(b, a), pair(b, a), // one source, repeated
		pair(d, c), pair(d, a), pair(d, b), // more than one source: ambiguous
		{ToVersionID: pgUUID(e), FromVersionID: pgtype.UUID{}}, // invalid source
	}
	got := PriorFrameworkVersions(rows)
	if len(got) != 1 || got[b] != a {
		t.Fatalf("PriorFrameworkVersions = %v; want only %s -> %s", got, b, a)
	}
	if len(PriorFrameworkVersions(nil)) != 0 {
		t.Fatal("PriorFrameworkVersions(nil) is not empty")
	}
}
