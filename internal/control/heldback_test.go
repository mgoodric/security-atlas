package control

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// TestHeldBackFromRow: shipped_update is true only when the shipped manifest
// differs from the stock manifest the approval replaced (#2358).
func TestHeldBackFromRow(t *testing.T) {
	t.Parallel()
	active, approved := uuid.New(), uuid.New()
	at := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	code, rel := "CHG-21", "2025.2"
	row := dbx.ListStockControlHoldbacksRow{
		BundleID:             "soc2_cc8_1_change_management",
		ActiveControlID:      pgUUID(active),
		ActiveVersion:        3,
		ApprovedControlID:    pgUUID(approved),
		ApprovedVersion:      2,
		ApprovedScfID:        &code,
		ApprovedRelease:      &rel,
		ApprovedAt:           pgtype.Timestamptz{Time: at, Valid: true},
		ShippedManifestHash:  "aa",
		ReplacedManifestHash: "aa",
		FirstHeldAt:          pgtype.Timestamptz{Time: at, Valid: true},
	}
	got := heldBackFromRow(row)
	if got.ActiveControlID != active.String() || got.ApprovedControlID != approved.String() ||
		got.ApprovedSCFID != code || got.ApprovedRelease != rel || got.ShippedUpdate ||
		got.ApprovedAt != "2026-11-17T10:00:00Z" || got.LastHeldAt != "" || got.ActiveUploadedAt != "" {
		t.Fatalf("same shipped manifest: %+v", got)
	}
	row.ShippedManifestHash = "bb"
	row.ApprovedScfID, row.ApprovedRelease = nil, nil
	got = heldBackFromRow(row)
	if !got.ShippedUpdate || got.ApprovedSCFID != "" || got.ApprovedRelease != "" {
		t.Fatalf("newer shipped manifest: %+v", got)
	}
}
