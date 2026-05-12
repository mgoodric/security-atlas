// Package soc2import loads the SOC 2 v2017 Trust Services Criteria
// crosswalk from a YAML file into Postgres. Each row in the YAML produces
// (a) one framework_requirements row for the TSC criterion and (b) one
// fw_to_scf_edges row from that requirement to one SCF anchor with a
// STRM relationship_type, strength, source_attribution, and rationale.
//
// The slice's HITL guardrail lives in three places:
//
//  1. The YAML schema REQUIRES relationship_type + strength on every row
//     (loader rejects rows that omit either). There is no silent default
//     to "equal/1.0" — every mapping is explicit.
//  2. source_attribution distinguishes scf_official from community_draft
//     so an auditor scanning the DB knows which rows still need human
//     spot-check vs which came from SCF's published crosswalk.
//  3. The importer leaves rows that match content as Unchanged; once a
//     row is HITL-approved in a later slice, re-running the importer with
//     the same crosswalk does not flip approval back to draft.
package soc2import

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// SchemaVersion is the YAML format version the loader accepts. Bumped
// when the shape changes incompatibly so converters can refuse to import.
const SchemaVersion = "1.0"

// Crosswalk is the YAML shape the loader parses. Documented for HITL
// reviewers; treat as a stable v1 contract.
type Crosswalk struct {
	SchemaVersion string `yaml:"schema_version"`
	// FrameworkSlug + FrameworkVersion identify the FrameworkVersion this
	// crosswalk pins to. Importer resolves them via the slice-006 frameworks
	// + framework_versions tables.
	FrameworkSlug    string `yaml:"framework_slug"`
	FrameworkName    string `yaml:"framework_name"`
	FrameworkIssuer  string `yaml:"framework_issuer"`
	FrameworkVersion string `yaml:"framework_version"`
	ReleaseDate      string `yaml:"release_date"`
	// SourceAttribution applies to every Mapping unless the row overrides
	// it. The canonical use is "community_draft" for the agent-authored
	// initial mapping set; "scf_official" when an SCF-published crosswalk
	// is ingested later.
	SourceAttribution string        `yaml:"source_attribution"`
	Requirements      []Requirement `yaml:"requirements"`
	Mappings          []Mapping     `yaml:"mappings"`
}

// Requirement is one TSC criterion clause inside the crosswalk.
type Requirement struct {
	Code  string `yaml:"code"`  // e.g., "CC6.1"
	Title string `yaml:"title"` // short label
	Body  string `yaml:"body"`  // full criterion text
}

// Mapping is one STRM-typed edge from a TSC criterion to an SCF anchor.
type Mapping struct {
	TSCCode           string  `yaml:"tsc_code"`           // FK into Requirements[].Code
	SCFAnchor         string  `yaml:"scf_anchor"`         // FK into the SCF catalog by scf_id
	RelationshipType  string  `yaml:"relationship_type"`  // STRM literal
	Strength          float64 `yaml:"strength"`           // [0.0, 1.0]
	Rationale         string  `yaml:"rationale"`          // short one-line justification
	SourceAttribution string  `yaml:"source_attribution"` // optional override for the crosswalk-level default
}

// Load reads a YAML crosswalk file from disk and validates it.
func Load(path string) (*Crosswalk, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("soc2import: read %s: %w", path, err)
	}
	var cw Crosswalk
	if err := yaml.Unmarshal(data, &cw); err != nil {
		return nil, fmt.Errorf("soc2import: parse %s: %w", path, err)
	}
	if err := validate(&cw); err != nil {
		return nil, err
	}
	return &cw, nil
}

// validate rejects malformed crosswalks. The strict checks (no silent
// defaults, every row explicit) are the in-loader half of the slice's
// AI-assist boundary — schema enforcement, not "trust the file."
func validate(cw *Crosswalk) error {
	if cw.SchemaVersion != SchemaVersion {
		return fmt.Errorf("soc2import: unsupported schema_version %q (loader expects %q)", cw.SchemaVersion, SchemaVersion)
	}
	if cw.FrameworkSlug == "" {
		return errors.New("soc2import: framework_slug is required")
	}
	if cw.FrameworkVersion == "" {
		return errors.New("soc2import: framework_version is required")
	}
	if !isValidSourceAttribution(cw.SourceAttribution) {
		return fmt.Errorf("soc2import: source_attribution %q is not one of {scf_official, community_draft, org_internal}", cw.SourceAttribution)
	}
	if len(cw.Requirements) == 0 {
		return errors.New("soc2import: crosswalk has zero requirements")
	}
	codes := make(map[string]struct{}, len(cw.Requirements))
	for i, r := range cw.Requirements {
		if r.Code == "" {
			return fmt.Errorf("soc2import: requirement[%d] missing code", i)
		}
		if r.Title == "" {
			return fmt.Errorf("soc2import: requirement[%d] (%s) missing title", i, r.Code)
		}
		if _, dup := codes[r.Code]; dup {
			return fmt.Errorf("soc2import: duplicate requirement code %q", r.Code)
		}
		codes[r.Code] = struct{}{}
	}
	if len(cw.Mappings) == 0 {
		return errors.New("soc2import: crosswalk has zero mappings")
	}
	for i, m := range cw.Mappings {
		if m.TSCCode == "" {
			return fmt.Errorf("soc2import: mapping[%d] missing tsc_code", i)
		}
		if _, ok := codes[m.TSCCode]; !ok {
			return fmt.Errorf("soc2import: mapping[%d] references unknown tsc_code %q", i, m.TSCCode)
		}
		if m.SCFAnchor == "" {
			return fmt.Errorf("soc2import: mapping[%d] (%s) missing scf_anchor", i, m.TSCCode)
		}
		if !isValidRelationshipType(m.RelationshipType) {
			return fmt.Errorf("soc2import: mapping[%d] (%s → %s) relationship_type %q is not one of {equal, subset_of, superset_of, intersects_with, no_relationship}",
				i, m.TSCCode, m.SCFAnchor, m.RelationshipType)
		}
		// AC-2: strength is bounded [0.0, 1.0]. The loader enforces it
		// before the DB CHECK constraint so the error is precise.
		if m.Strength < 0.0 || m.Strength > 1.0 {
			return fmt.Errorf("soc2import: mapping[%d] (%s → %s) strength %v out of range [0.0, 1.0]",
				i, m.TSCCode, m.SCFAnchor, m.Strength)
		}
		attribution := m.SourceAttribution
		if attribution == "" {
			attribution = cw.SourceAttribution
		}
		if !isValidSourceAttribution(attribution) {
			return fmt.Errorf("soc2import: mapping[%d] (%s → %s) source_attribution %q is not one of {scf_official, community_draft, org_internal}",
				i, m.TSCCode, m.SCFAnchor, attribution)
		}
	}
	return nil
}

func isValidRelationshipType(s string) bool {
	switch s {
	case "equal", "subset_of", "superset_of", "intersects_with", "no_relationship":
		return true
	}
	return false
}

func isValidSourceAttribution(s string) bool {
	switch s {
	case "scf_official", "community_draft", "org_internal":
		return true
	}
	return false
}
