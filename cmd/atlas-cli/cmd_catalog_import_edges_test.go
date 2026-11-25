package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/soc2import"
)

// import-crosswalk prints why the carried sync held each edge back, and
// lists a carried re-seed with the owned ones (#3079, #3083).
func TestPrintImportEdges(t *testing.T) {
	t.Parallel()
	report := soc2import.Report{
		EdgesCarriedSynced: []soc2import.CatalogEdge{
			{RequirementCode: "R-1", SCFAnchor: "GOV-04", Source: "scf-remap:p1", Attribution: "community_draft", Tier: "draft"},
		},
		EdgesCarriedHeldBack: []soc2import.HeldBackEdge{
			{
				CatalogEdge: soc2import.CatalogEdge{RequirementCode: "R-2", SCFAnchor: "GOV-05", Source: "scf-remap:p2", Attribution: "scf_official", Tier: "verified"},
				Reason:      soc2import.HeldBackOutranked,
			},
			{
				CatalogEdge: soc2import.CatalogEdge{RequirementCode: "R-3", SCFAnchor: "GOV-06", Source: "scf-remap:p3", Attribution: "community_draft", Tier: "verified"},
				Reason:      soc2import.HeldBackHumanReviewed,
			},
		},
		EdgesTierReseeded: []soc2import.ReseededEdge{
			{RequirementCode: "R-1", SCFAnchor: "GOV-04", PreviousTier: "verified", NewTier: "draft"},
		},
	}
	var out bytes.Buffer
	printImportEdges(&out, report)
	for _, want := range []string{
		"edge kept R-2->GOV-05: carried by an SCF edge remap, content differs from the crosswalk but is held back (reason=outranked source=scf-remap:p2 source_attribution=scf_official mapping_tier=verified)\n",
		"edge kept R-3->GOV-06: carried by an SCF edge remap, content differs from the crosswalk but is held back (reason=human_reviewed source=scf-remap:p3 source_attribution=community_draft mapping_tier=verified)\n",
		"edge tier re-seeded R-1->GOV-04: content changed; mapping_tier verified -> draft\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("stdout missing %q; got:\n%s", want, out.String())
		}
	}
}
