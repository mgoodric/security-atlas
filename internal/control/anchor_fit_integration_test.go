//go:build integration

// Integration tests for issue #1736: a control bundle's anchor_fit and
// anchor_fit_note are stored on the control row, default to an exact fit,
// allow an unanchored control when the fit is none, and filter the
// paged controls list. Real Postgres only.

package control_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func TestUpload_AnchorFit(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)

	_, code := seedSCFAnchor(t, admin, "GOV-09", "GOV")
	tenant := freshTenant(t, admin)
	store := control.NewStore(app)
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}

	upload := func(t *testing.T, yaml string) uuid.UUID {
		t.Helper()
		b, err := control.FinalizeBundleForHTTP([]byte(yaml))
		if err != nil {
			t.Fatalf("FinalizeBundleForHTTP: %v", err)
		}
		res, err := store.Upload(ctx, b, "key_admin")
		if err != nil {
			t.Fatalf("Upload: %v", err)
		}
		return res.ControlID
	}
	readFit := func(t *testing.T, id uuid.UUID) (fit string, note *string, anchor *uuid.UUID) {
		t.Helper()
		if err := admin.QueryRow(context.Background(), `
			SELECT anchor_fit, anchor_fit_note, scf_anchor_id FROM controls WHERE id = $1
		`, id).Scan(&fit, &note, &anchor); err != nil {
			t.Fatalf("read control: %v", err)
		}
		return fit, note, anchor
	}

	// An omitted anchor_fit is stored as exact with no note.
	exactID := upload(t, yamlFor("fit_default_exact", code, "automated"))
	if fit, note, anchor := readFit(t, exactID); fit != "exact" || note != nil || anchor == nil {
		t.Fatalf("default: fit=%q note=%v anchor=%v; want exact, nil, set", fit, note, anchor)
	}

	// none + note, anchored to the nearest catch-all.
	noneAnchoredID := upload(t, `bundle_schema_version: "1"
bundle_id: fit_none_anchored
title: "Audit committee independence"
scf_anchor_id: `+code+`
anchor_fit: none
anchor_fit_note: "No SCF control covers audit committee independence; GOV-09 is the nearest catch-all."
implementation_type: manual_attested
`)
	if fit, note, anchor := readFit(t, noneAnchoredID); fit != "none" || note == nil || anchor == nil {
		t.Fatalf("none anchored: fit=%q note=%v anchor=%v; want none, set, set", fit, note, anchor)
	}

	// none + note, no anchor at all: stored with a NULL anchor.
	noneBareID := upload(t, `bundle_schema_version: "1"
bundle_id: fit_none_unanchored
title: "Recruitment and retention"
anchor_fit: none
anchor_fit_note: "No SCF control covers recruitment and retention."
implementation_type: manual_attested
`)
	if fit, note, anchor := readFit(t, noneBareID); fit != "none" || note == nil || anchor != nil {
		t.Fatalf("none unanchored: fit=%q note=%v anchor=%v; want none, set, nil", fit, note, anchor)
	}

	// The paged list filters by fit; the total follows the filter.
	for _, tc := range []struct {
		fit  string
		want []uuid.UUID
	}{
		{"", []uuid.UUID{exactID, noneAnchoredID, noneBareID}},
		{"exact", []uuid.UUID{exactID}},
		{"none", []uuid.UUID{noneAnchoredID, noneBareID}},
		{"partial", nil},
	} {
		rows, total, err := store.ListWithState(ctx, 100, 0, pgtype.UUID{}, tc.fit)
		if err != nil {
			t.Fatalf("ListWithState(%q): %v", tc.fit, err)
		}
		if total != int64(len(tc.want)) || len(rows) != len(tc.want) {
			t.Fatalf("ListWithState(%q): %d rows, total %d; want %d", tc.fit, len(rows), total, len(tc.want))
		}
		got := map[uuid.UUID]bool{}
		for _, r := range rows {
			got[uuid.UUID(r.ID.Bytes)] = true
		}
		for _, id := range tc.want {
			if !got[id] {
				t.Errorf("ListWithState(%q): control %s missing", tc.fit, id)
			}
		}
	}
}
