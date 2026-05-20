// Slice 137 — unit tests for the controls-export projection helpers.
// The integration suite (`export_integration_test.go`, build-tag
// `integration`) exercises the full wire surface against Postgres
// + RLS; this file covers the pure functions that need no DB.

package controls

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/export"
)

// controlsToRowIter must emit rows in canonical column order. Guards
// against a future contributor reshuffling controlsExportHeader
// without updating controlsToRowIter (or vice versa).
func TestSlice137_ControlsToRowIter_ColumnOrderMatchesHeader(t *testing.T) {
	header := controlsExportHeader()
	now := time.Now().UTC()
	id := uuid.New()
	anchor := uuid.New()

	row := controlExportRow{
		ID:                 id,
		BundleID:           "bundle-aws-iam",
		Version:            3,
		SCFID:              "IAC-06",
		SCFAnchorID:        anchor,
		Title:              "AWS IAM least-privilege",
		ControlFamily:      "identity-access-management",
		ImplementationType: "automated",
		OwnerRole:          "platform-eng",
		LifecycleState:     "active",
		ApplicabilityExpr:  "BU=eng AND env=prod",
		FreshnessClass:     "fresh",
		BundleManifestHash: "sha256:abcdef0123456789",
		CreatedAt:          now,
		UpdatedAt:          now,
	}

	it := controlsToRowIter([]controlExportRow{row})
	var cells []string
	for r := range it {
		cells = r
		break
	}
	if len(cells) != len(header) {
		t.Fatalf("row cell count = %d; want %d (header)", len(cells), len(header))
	}

	// Spot-check the cells at known positions.
	checks := map[string]string{
		"id":                   id.String(),
		"bundle_id":            "bundle-aws-iam",
		"version":              "3",
		"title":                "AWS IAM least-privilege",
		"control_family":       "identity-access-management",
		"scf_id":               "IAC-06",
		"scf_anchor_id":        anchor.String(),
		"implementation_type":  "automated",
		"owner_role":           "platform-eng",
		"lifecycle_state":      "active",
		"applicability_expr":   "BU=eng AND env=prod",
		"freshness_class":      "fresh",
		"bundle_manifest_hash": "sha256:abcdef0123456789",
	}
	for col, want := range checks {
		idx := -1
		for i, h := range header {
			if h == col {
				idx = i
				break
			}
		}
		if idx < 0 {
			t.Errorf("column %q missing from header", col)
			continue
		}
		if cells[idx] != want {
			t.Errorf("column %q = %q; want %q", col, cells[idx], want)
		}
	}

	// Timestamp columns render as RFC3339.
	createdIdx := -1
	updatedIdx := -1
	for i, h := range header {
		switch h {
		case "created_at":
			createdIdx = i
		case "updated_at":
			updatedIdx = i
		}
	}
	if !strings.Contains(cells[createdIdx], "T") {
		t.Errorf("created_at = %q; want RFC3339-shaped", cells[createdIdx])
	}
	if !strings.Contains(cells[updatedIdx], "T") {
		t.Errorf("updated_at = %q; want RFC3339-shaped", cells[updatedIdx])
	}
}

// Header positions are stable. Lock the canonical order so a
// downstream consumer keying off a column position cannot be silently
// broken by a header reorder.
func TestSlice137_ControlsExportHeader_StableOrder(t *testing.T) {
	want := []string{
		"id",
		"bundle_id",
		"version",
		"title",
		"control_family",
		"scf_id",
		"scf_anchor_id",
		"implementation_type",
		"owner_role",
		"lifecycle_state",
		"applicability_expr",
		"freshness_class",
		"bundle_manifest_hash",
		"created_at",
		"updated_at",
	}
	got := controlsExportHeader()
	if len(got) != len(want) {
		t.Fatalf("column count = %d; want %d", len(got), len(want))
	}
	for i, c := range want {
		if got[i] != c {
			t.Errorf("column[%d] = %q; want %q", i, got[i], c)
		}
	}
}

// parseControlsExportFormat resolves the query string. Default is CSV;
// unknown values 400; the three valid formats round-trip.
func TestSlice137_ParseControlsExportFormat(t *testing.T) {
	cases := []struct {
		query     string
		want      export.Format
		expectErr bool
	}{
		{"", export.FormatCSV, false},
		{"format=csv", export.FormatCSV, false},
		{"format=json", export.FormatJSON, false},
		{"format=xlsx", export.FormatXLSX, false},
		{"format=CSV", export.FormatCSV, false},   // case-insensitive
		{"format=JSON", export.FormatJSON, false}, // case-insensitive
		{"format=pdf", "pdf", true},               // unsupported
		{"format=html", "html", true},             // unsupported
		{"format=", export.FormatCSV, false},      // empty == default
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/v1/controls/export?"+tc.query, nil)
			got, err := parseControlsExportFormat(req)
			if tc.expectErr {
				if err == nil {
					t.Errorf("want error; got format=%q err=nil", got)
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected err: %v", err)
			}
			if got != tc.want {
				t.Errorf("format = %q; want %q", got, tc.want)
			}
		})
	}
}

// Role-gate parity: the controlsHasProgramRead predicate must match
// the slice 067 risk read endpoints' helper. A bare push credential
// (no flags) does NOT carry program-read access; admin / approver /
// owner-roles do.
func TestSlice137_ControlsHasProgramRead(t *testing.T) {
	cases := []struct {
		name string
		c    credstore.Credential
		want bool
	}{
		{"bare", credstore.Credential{}, false},
		{"admin", credstore.Credential{IsAdmin: true}, true},
		{"approver", credstore.Credential{IsApprover: true}, true},
		{"owner", credstore.Credential{OwnerRoles: []string{"control-owner"}}, true},
		{"admin+owner", credstore.Credential{IsAdmin: true, OwnerRoles: []string{"x"}}, true},
		{"empty owner roles", credstore.Credential{OwnerRoles: []string{}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := controlsHasProgramRead(tc.c)
			if got != tc.want {
				t.Errorf("controlsHasProgramRead = %v; want %v", got, tc.want)
			}
		})
	}
}

// The meta-audit action constant is the slice 137 D6 plural value.
// Locks the spelling: a contributor who accidentally types
// `control_export` (singular) would surface this test failure rather
// than a CI round-trip failure (which slice 136 cost three times).
func TestSlice137_MetaAuditActionConstant(t *testing.T) {
	if metaAuditActionControlsExport != "controls_export" {
		t.Errorf("metaAuditActionControlsExport = %q; want %q (slice 137 D6 plural convention)",
			metaAuditActionControlsExport, "controls_export")
	}
}

// Default row cap is 500K (slice 137 D3 / P0-A-UCF-1). Locks the
// constant against drift.
func TestSlice137_DefaultRowCap(t *testing.T) {
	if defaultControlsExportRowCap != 500_000 {
		t.Errorf("defaultControlsExportRowCap = %d; want 500000 (slice 137 D3 lifted cap)",
			defaultControlsExportRowCap)
	}
}
