// White-box unit tests for unexported helpers in internal/artifact.
//
// Load-bearing functions exercised:
//
//   - fromRow — the dbx.Artifact → artifact.Artifact mapper used by
//     every read path (Put dedup return, Get, Presign). Branches:
//       * ContentHash *string  — nil pointer vs non-nil pointer
//       * UploadedAt pgtype.Timestamptz — Valid=true vs Valid=false
//     The integration tests always observe Valid=true and ContentHash
//     non-nil (the migration enforces NOT NULL on both), so the
//     defence-in-depth branches need direct white-box coverage.
//   - pgUUID — wraps uuid.UUID into pgtype.UUID with Valid=true. The
//     adapter is one line but the Valid=true semantic is load-bearing
//     (pgtype.UUID{} with Valid=false silently NULLs the column).
//
// These tests sit in `package artifact` (not `artifact_test`) so we can
// reach the unexported symbols. The black-box tests in store_test.go
// stay in `artifact_test` to mirror the integration-test pattern and
// avoid accidentally testing private internals from a consumer's
// perspective.

package artifact

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

func TestFromRow_FullySetRow(t *testing.T) {
	t.Parallel()
	id := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	tenant := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	hash := "abcdef0123456789"
	uploaded := time.Date(2026, 5, 25, 12, 34, 56, 0, time.UTC)

	row := dbx.Artifact{
		ID:          pgtype.UUID{Bytes: id, Valid: true},
		TenantID:    pgtype.UUID{Bytes: tenant, Valid: true},
		StorageKey:  "tenant-x/artifact-y",
		ContentHash: &hash,
		SizeBytes:   1024,
		ContentType: "application/pdf",
		UploadedBy:  "cred_test_abc",
		UploadedAt:  pgtype.Timestamptz{Time: uploaded, Valid: true},
	}

	got := fromRow(row)

	if got.ID != id {
		t.Errorf("ID: got %v want %v", got.ID, id)
	}
	if got.TenantID != tenant {
		t.Errorf("TenantID: got %v want %v", got.TenantID, tenant)
	}
	if got.StorageKey != "tenant-x/artifact-y" {
		t.Errorf("StorageKey: got %q", got.StorageKey)
	}
	if got.ContentHash != hash {
		t.Errorf("ContentHash: got %q want %q", got.ContentHash, hash)
	}
	if got.SizeBytes != 1024 {
		t.Errorf("SizeBytes: got %d", got.SizeBytes)
	}
	if got.ContentType != "application/pdf" {
		t.Errorf("ContentType: got %q", got.ContentType)
	}
	if got.UploadedBy != "cred_test_abc" {
		t.Errorf("UploadedBy: got %q", got.UploadedBy)
	}
	if !got.UploadedAt.Equal(uploaded) {
		t.Errorf("UploadedAt: got %v want %v", got.UploadedAt, uploaded)
	}
}

func TestFromRow_NilContentHashYieldsEmptyString(t *testing.T) {
	t.Parallel()
	// Defence-in-depth: rows reading from a pre-NOT-NULL migration era
	// could surface ContentHash == nil. The mapper must handle that
	// without panic, yielding the zero-value empty string.
	row := dbx.Artifact{
		ID:          pgtype.UUID{Bytes: uuid.New(), Valid: true},
		TenantID:    pgtype.UUID{Bytes: uuid.New(), Valid: true},
		StorageKey:  "tenant-x/artifact-y",
		ContentHash: nil, // ← the branch under test
		SizeBytes:   42,
		ContentType: "text/plain",
		UploadedBy:  "cred_test",
		UploadedAt:  pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}

	got := fromRow(row)

	if got.ContentHash != "" {
		t.Errorf("nil ContentHash must map to empty string; got %q", got.ContentHash)
	}
	// Other fields must still round-trip.
	if got.StorageKey != "tenant-x/artifact-y" {
		t.Errorf("StorageKey clobbered: %q", got.StorageKey)
	}
}

func TestFromRow_InvalidUploadedAtYieldsZeroTime(t *testing.T) {
	t.Parallel()
	// Defence-in-depth: a not-yet-set Timestamptz (Valid=false) must
	// map to time.Time's zero value, not panic and not pull in
	// Timestamptz.Time directly (which is uninitialised).
	hash := "abc"
	row := dbx.Artifact{
		ID:          pgtype.UUID{Bytes: uuid.New(), Valid: true},
		TenantID:    pgtype.UUID{Bytes: uuid.New(), Valid: true},
		StorageKey:  "tenant-x/artifact-y",
		ContentHash: &hash,
		SizeBytes:   7,
		ContentType: "text/plain",
		UploadedBy:  "cred_test",
		UploadedAt:  pgtype.Timestamptz{Valid: false}, // ← the branch under test
	}

	got := fromRow(row)

	if !got.UploadedAt.IsZero() {
		t.Errorf("invalid UploadedAt must map to time.Time zero; got %v", got.UploadedAt)
	}
}

func TestPgUUID_SetsValidTrue(t *testing.T) {
	t.Parallel()
	id := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	wrapped := pgUUID(id)

	if !wrapped.Valid {
		t.Fatal("pgUUID must set Valid=true; got false (would NULL the column on insert)")
	}
	if wrapped.Bytes != id {
		t.Errorf("pgUUID Bytes: got %v want %v", wrapped.Bytes, id)
	}
}

func TestPgUUID_RoundTripUUIDZero(t *testing.T) {
	t.Parallel()
	// Even the all-zeros UUID must wrap with Valid=true. (Postgres
	// stores 00000000-0000-0000-0000-000000000000 as a real value, not
	// NULL — the application code's contract is the same.)
	wrapped := pgUUID(uuid.UUID{})

	if !wrapped.Valid {
		t.Fatal("pgUUID(zero) must still set Valid=true")
	}
	if wrapped.Bytes != (uuid.UUID{}) {
		t.Errorf("pgUUID(zero) Bytes: got %v want zero", wrapped.Bytes)
	}
}
