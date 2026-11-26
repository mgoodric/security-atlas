package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/soc2import"
)

func TestPrintConvergeResults(t *testing.T) {
	t.Parallel()
	results := []soc2import.ConvergeResult{
		{File: "broken.yaml", Err: errors.New("crosswalk: bad yaml")},
		{File: "iso27001-2022.yaml", FrameworkSlug: "iso27001", Report: soc2import.Report{
			FrameworkVersion: "2022",
			EdgesCreated:     1,
			EdgesUnchanged:   40,
			EdgesRetired: []soc2import.CatalogEdge{
				{RequirementCode: "A.5.1", SCFAnchor: "GOV-01", Source: soc2import.LegacySource, Attribution: "community_draft", Tier: "draft"},
			},
			EdgesUnlisted: []soc2import.CatalogEdge{{RequirementCode: "A.5.1", SCFAnchor: "GOV-04"}},
			EdgesCarriedHeldBack: []soc2import.HeldBackEdge{{
				CatalogEdge: soc2import.CatalogEdge{RequirementCode: "A.5.2", SCFAnchor: "GOV-05", Source: "scf-remap:p1", Attribution: "scf_official", Tier: "verified"},
				Reason:      soc2import.HeldBackOutranked,
			}},
			Warnings: []string{"scf_release not declared"},
		}},
		{File: "pci-dss-4.0.yaml", FrameworkSlug: "pcidss", Skipped: true},
	}
	var out, errOut bytes.Buffer
	err := printConvergeResults(&out, &errOut, results)
	if err == nil || !strings.Contains(err.Error(), "broken.yaml") {
		t.Fatalf("err = %v; want one naming broken.yaml", err)
	}
	for _, want := range []string{
		"converge framework=iso27001:2022 file=iso27001-2022.yaml edges created=1 updated=0 unchanged=40 retired=1 restored=0 kept=1 requirements kept=0\n",
		"converge framework=iso27001 edge retired A.5.1->GOV-01: no longer in iso27001-2022.yaml (source=(none) source_attribution=community_draft mapping_tier=draft)\n",
		"converge framework=iso27001 edge kept A.5.2->GOV-05: carried by an SCF edge remap, content differs from iso27001-2022.yaml but is held back (reason=outranked source=scf-remap:p1 source_attribution=scf_official mapping_tier=verified)\n",
		"converge framework=pcidss file=pci-dss-4.0.yaml skipped: not imported on this install\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("stdout missing %q; got:\n%s", want, out.String())
		}
	}
	for _, want := range []string{
		"warning: converge broken.yaml: crosswalk: bad yaml\n",
		"warning: converge framework=iso27001: scf_release not declared\n",
	} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("stderr missing %q; got:\n%s", want, errOut.String())
		}
	}
}

func TestPrintConvergeResults_AllSkippedIsNoError(t *testing.T) {
	t.Parallel()
	var out, errOut bytes.Buffer
	if err := printConvergeResults(&out, &errOut, []soc2import.ConvergeResult{{File: "a.yaml", Skipped: true}}); err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q; want empty", errOut.String())
	}
}
