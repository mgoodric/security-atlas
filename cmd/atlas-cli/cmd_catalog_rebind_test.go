package main

import (
	"bytes"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/scfimport"
)

// #1971 D3: the SCF import names every control it left needing
// re-anchoring, so the bootstrap log shows what lost framework coverage.
func TestPrintRebindReport(t *testing.T) {
	t.Parallel()
	tenant := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	var buf bytes.Buffer
	printRebindReport(&buf, scfimport.RebindReport{
		Rebound: []scfimport.ControlOnLegacyAnchor{{BundleID: "a"}},
		NeedsReanchoring: []scfimport.ControlOnLegacyAnchor{{
			TenantID:      tenant,
			BundleID:      "tenant-pam",
			LegacySCFID:   "IAC-21",
			LegacyRelease: "test-2026.1",
			Note:          "IAC-21 -> IAC-10 in test-2026.3 (concept)",
		}},
		Proposals: map[scfimport.RemapClass]int{scfimport.RemapUnchanged: 1, scfimport.RemapSplit: 1},
	})
	want := "controls rebound=1 needs_reanchoring=1 pinned=0 superseded_mid_run=0\n" +
		"remap_proposals unchanged=1 renamed=0 split=1 reused_id=0 no_successor=0\n" +
		"control needs re-anchoring tenant=11111111-1111-4111-8111-111111111111 bundle=tenant-pam anchor=IAC-21@test-2026.1: IAC-21 -> IAC-10 in test-2026.3 (concept)\n"
	if got := buf.String(); got != want {
		t.Fatalf("output =\n%s\nwant\n%s", got, want)
	}
}

// #1777: the SCF import reports the crosswalk-edge remap queue by class.
func TestPrintEdgeRemapCounts(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	printEdgeRemapCounts(&buf, map[scfimport.RemapClass]int{scfimport.RemapRenamed: 2, scfimport.RemapNoSuccessor: 1})
	want := "edge_remap_proposals unchanged=0 renamed=2 split=0 reused_id=0 no_successor=1\n"
	if got := buf.String(); got != want {
		t.Fatalf("output = %q; want %q", got, want)
	}
}

// #1804: the SCF import reports the overlay-edge remap queue by class.
func TestPrintOverlayRemapCounts(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	printOverlayRemapCounts(&buf, map[scfimport.RemapClass]int{scfimport.RemapSplit: 3})
	want := "overlay_remap_proposals unchanged=0 renamed=0 split=3 reused_id=0 no_successor=0\n"
	if got := buf.String(); got != want {
		t.Fatalf("output = %q; want %q", got, want)
	}
}
