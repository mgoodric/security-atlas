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
	})
	want := "controls rebound=1 needs_reanchoring=1\n" +
		"control needs re-anchoring tenant=11111111-1111-4111-8111-111111111111 bundle=tenant-pam anchor=IAC-21@test-2026.1: IAC-21 -> IAC-10 in test-2026.3 (concept)\n"
	if got := buf.String(); got != want {
		t.Fatalf("output =\n%s\nwant\n%s", got, want)
	}
}
