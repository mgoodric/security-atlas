// Package anchorseed holds the in-memory framework + requirement seed
// used by the slice-005 HTTP API for the requirement-mapping join. Slice
// 006 moves anchor metadata into Postgres (DB-backed); the mappings stay
// here, keyed on scf_id, until slice 008 builds the framework_requirements
// and STRM-edge tables.
package anchorseed

// FrameworkVersion is one published version of a compliance framework.
type FrameworkVersion struct {
	ID        string `json:"id"`
	Framework string `json:"framework"`
	Version   string `json:"version"`
}

// Requirement is one clause inside a FrameworkVersion.
type Requirement struct {
	ID                 string `json:"id"`
	FrameworkVersionID string `json:"framework_version_id"`
	Code               string `json:"code"`
	Text               string `json:"text"`
}

// Mapping is one STRM-typed edge between a Requirement and an SCF anchor.
// Keyed on `scf_id` (e.g., "IAC-06") so the lookup survives slice 006's
// switch from in-memory anchor records to DB-backed ones.
type Mapping struct {
	RequirementID string  `json:"requirement_id"`
	AnchorSCFID   string  `json:"anchor_scf_id"`
	STRMType      string  `json:"strm_type"`
	Strength      float64 `json:"strength"`
}

// RequirementWithMapping is the join a requirements endpoint emits.
type RequirementWithMapping struct {
	Requirement      Requirement      `json:"requirement"`
	FrameworkVersion FrameworkVersion `json:"framework_version"`
	STRMType         string           `json:"strm_type"`
	Strength         float64          `json:"strength"`
}

// Store exposes the in-memory mapping lookup.
type Store interface {
	RequirementsForSCFID(scfID string) []RequirementWithMapping
}

// Seed is the in-memory Store.
type Seed struct {
	frameworkByID   map[string]FrameworkVersion
	requirementByID map[string]Requirement
	mappingsBySCFID map[string][]Mapping
}

// New returns a Seed populated with the slice-005 sample data, re-keyed on
// scf_id for the slice-006 DB-backed anchors handover.
func New() *Seed {
	frameworks := defaultFrameworks()
	requirements := defaultRequirements()
	mappings := defaultMappings()

	frameworkByID := make(map[string]FrameworkVersion, len(frameworks))
	for _, f := range frameworks {
		frameworkByID[f.ID] = f
	}
	requirementByID := make(map[string]Requirement, len(requirements))
	for _, r := range requirements {
		requirementByID[r.ID] = r
	}
	mappingsBySCFID := make(map[string][]Mapping)
	for _, m := range mappings {
		mappingsBySCFID[m.AnchorSCFID] = append(mappingsBySCFID[m.AnchorSCFID], m)
	}

	return &Seed{
		frameworkByID:   frameworkByID,
		requirementByID: requirementByID,
		mappingsBySCFID: mappingsBySCFID,
	}
}

// RequirementsForSCFID returns every requirement that maps to scfID, joined
// with its framework-version metadata and the STRM edge details.
func (s *Seed) RequirementsForSCFID(scfID string) []RequirementWithMapping {
	mappings := s.mappingsBySCFID[scfID]
	out := make([]RequirementWithMapping, 0, len(mappings))
	for _, m := range mappings {
		req, ok := s.requirementByID[m.RequirementID]
		if !ok {
			continue
		}
		fv, ok := s.frameworkByID[req.FrameworkVersionID]
		if !ok {
			continue
		}
		out = append(out, RequirementWithMapping{
			Requirement:      req,
			FrameworkVersion: fv,
			STRMType:         m.STRMType,
			Strength:         m.Strength,
		})
	}
	return out
}
