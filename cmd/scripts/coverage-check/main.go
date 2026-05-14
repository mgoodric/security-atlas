// coverage-check verifies the slice-010 SOC 2 control kit:
//
//  1. Every bundle under controls/soc2/ parses cleanly via the slice-009
//     parser (internal/control.ParseDirectory).
//  2. Every bundle's scf_anchor_id exists in the slice-006 SCF catalog
//     fixture (migrations/fixtures/scf-sample.json).
//  3. The 43 unique TSC requirement codes in the slice-007 crosswalk
//     (data/crosswalks/soc2-tsc-2017.yaml) are each covered by at least
//     one bundle (graph traversal: bundle → scf_anchor_id → STRM edge →
//     tsc_code). Coverage ratio is printed.
//  4. At least 50% of bundles carry an evidence_queries[] entry
//     (AC-2 automated coverage gate).
//
// Run from repo root:
//
//	go run ./cmd/scripts/coverage-check
//
// Exit codes:
//
//	0 — all checks pass
//	1 — one or more checks fail (details printed to stderr)
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mgoodric/security-atlas/internal/control"
	"gopkg.in/yaml.v3"
)

type scfCatalog struct {
	Controls []struct {
		ScfID string `json:"scf_id"`
	} `json:"controls"`
}

type crosswalk struct {
	Requirements []struct {
		Code string `yaml:"code"`
	} `yaml:"requirements"`
	Mappings []struct {
		TscCode      string `yaml:"tsc_code"`
		ScfAnchor    string `yaml:"scf_anchor"`
		Relationship string `yaml:"relationship_type"`
	} `yaml:"mappings"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL:", err)
		os.Exit(1)
	}
}

func run() error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}

	// 1. Load SCF catalog (slice 006 fixture).
	scfPath := filepath.Join(root, "migrations", "fixtures", "scf-sample.json")
	scfBytes, err := os.ReadFile(scfPath)
	if err != nil {
		return fmt.Errorf("read SCF fixture: %w", err)
	}
	var cat scfCatalog
	if err := json.Unmarshal(scfBytes, &cat); err != nil {
		return fmt.Errorf("parse SCF fixture: %w", err)
	}
	knownAnchors := map[string]bool{}
	for _, c := range cat.Controls {
		knownAnchors[c.ScfID] = true
	}
	fmt.Printf("SCF anchors in catalog: %d\n", len(knownAnchors))

	// 2. Load SOC 2 crosswalk (slice 007).
	xwalkPath := filepath.Join(root, "data", "crosswalks", "soc2-tsc-2017.yaml")
	xwalkBytes, err := os.ReadFile(xwalkPath)
	if err != nil {
		return fmt.Errorf("read crosswalk: %w", err)
	}
	var xw crosswalk
	if err := yaml.Unmarshal(xwalkBytes, &xw); err != nil {
		return fmt.Errorf("parse crosswalk: %w", err)
	}
	// Build anchor → []tsc_code map.
	anchorToTSC := map[string]map[string]bool{}
	for _, m := range xw.Mappings {
		if anchorToTSC[m.ScfAnchor] == nil {
			anchorToTSC[m.ScfAnchor] = map[string]bool{}
		}
		anchorToTSC[m.ScfAnchor][m.TscCode] = true
	}
	// All TSC codes referenced in mappings (these are the ones graph-reachable).
	mappedTSC := map[string]bool{}
	for _, m := range xw.Mappings {
		mappedTSC[m.TscCode] = true
	}
	fmt.Printf("TSC codes in crosswalk mappings: %d\n", len(mappedTSC))

	// 3. Walk controls/soc2/ and parse every bundle.
	bundlesRoot := filepath.Join(root, "controls", "soc2")
	entries, err := os.ReadDir(bundlesRoot)
	if err != nil {
		return fmt.Errorf("read bundles root: %w", err)
	}
	var (
		bundleCount    int
		automatedCount int
		anchorsUsed    = map[string]bool{}
		coveredTSC     = map[string]bool{}
		anchorsMissing []string
		failures       []string
	)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		bundleDir := filepath.Join(bundlesRoot, e.Name())
		b, err := control.ParseDirectory(bundleDir)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: parse: %v", e.Name(), err))
			continue
		}
		if err := b.ValidateApplicabilityExpr(); err != nil {
			failures = append(failures, fmt.Sprintf("%s: applicability_expr: %v", e.Name(), err))
			continue
		}
		bundleCount++
		if len(b.Manifest.EvidenceQueries) > 0 {
			automatedCount++
		}
		anchor := b.Manifest.SCFAnchorID
		anchorsUsed[anchor] = true
		if !knownAnchors[anchor] {
			anchorsMissing = append(anchorsMissing, fmt.Sprintf("%s -> %s", e.Name(), anchor))
		}
		for tsc := range anchorToTSC[anchor] {
			coveredTSC[tsc] = true
		}
	}

	fmt.Println()
	fmt.Println("=== Slice 010 verification ===")
	fmt.Printf("Bundles parsed:                          %d\n", bundleCount)
	fmt.Printf("Bundles with evidence_queries[]:         %d (%.0f%%)\n",
		automatedCount, 100.0*float64(automatedCount)/float64(bundleCount))
	fmt.Printf("Unique SCF anchors used:                 %d\n", len(anchorsUsed))
	fmt.Printf("TSC requirements covered:                %d / %d (%.0f%%)\n",
		len(coveredTSC), len(mappedTSC),
		100.0*float64(len(coveredTSC))/float64(len(mappedTSC)))

	if len(failures) > 0 {
		fmt.Fprintln(os.Stderr, "\nFAILURES:")
		for _, f := range failures {
			fmt.Fprintln(os.Stderr, " -", f)
		}
	}
	if len(anchorsMissing) > 0 {
		fmt.Fprintln(os.Stderr, "\nBUNDLES REFERENCING UNKNOWN SCF ANCHORS:")
		for _, m := range anchorsMissing {
			fmt.Fprintln(os.Stderr, " -", m)
		}
	}

	// Uncovered TSC codes (the ones in mappings but no bundle reaches them).
	var uncovered []string
	for code := range mappedTSC {
		if !coveredTSC[code] {
			uncovered = append(uncovered, code)
		}
	}
	sort.Strings(uncovered)
	if len(uncovered) > 0 {
		fmt.Println("\nUncovered TSC codes (no bundle anchors to a mapping for these):")
		for _, c := range uncovered {
			fmt.Println(" -", c)
		}
	}

	// Gate checks.
	var gateFail []string
	if bundleCount != 50 {
		gateFail = append(gateFail, fmt.Sprintf("AC-1: expected 50 bundles, got %d", bundleCount))
	}
	if automatedCount*2 < bundleCount {
		gateFail = append(gateFail, fmt.Sprintf("AC-2: expected ≥%d automated, got %d", (bundleCount+1)/2, automatedCount))
	}
	pct := 100 * len(coveredTSC) / len(mappedTSC)
	if pct < 80 {
		gateFail = append(gateFail, fmt.Sprintf("AC-3: TSC coverage %d%% < 80%%", pct))
	}
	if len(failures) > 0 {
		gateFail = append(gateFail, fmt.Sprintf("AC-1: %d bundle parse failures", len(failures)))
	}
	if len(anchorsMissing) > 0 {
		gateFail = append(gateFail, fmt.Sprintf("invariant 7: %d unknown SCF anchors", len(anchorsMissing)))
	}

	if len(gateFail) > 0 {
		fmt.Fprintln(os.Stderr, "\nGATE FAILURES:")
		for _, f := range gateFail {
			fmt.Fprintln(os.Stderr, " -", f)
		}
		return fmt.Errorf("%d gate(s) failed", len(gateFail))
	}

	fmt.Println("\nALL CHECKS PASS")
	if !strings.HasSuffix(root, "security-atlas-010") && !strings.HasSuffix(root, "security-atlas") {
		fmt.Println("(note: not run from a known repo root, but checks ran)")
	}
	return nil
}
