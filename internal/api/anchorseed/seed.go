// Package anchorseed holds the in-memory SCF anchor + framework-requirement
// seed used by the HTTP /v1/anchors API in slice 005. Slice 008 replaces
// the seed with DB-backed UCF graph queries — the JSON wire shape stays
// the same.
package anchorseed

// Anchor is one SCF control anchor in the canonical catalog. The full SCF
// has ~1,400 anchors; slice 005 seeds a curated subset covering the major
// families so the frontend has real data to render against.
type Anchor struct {
	ID          string `json:"id"`
	SCFID       string `json:"scf_id"`
	Family      string `json:"family"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// FrameworkVersion is one published version of a compliance framework.
type FrameworkVersion struct {
	ID        string `json:"id"`
	Framework string `json:"framework"`
	Version   string `json:"version"`
}

// Requirement is one clause inside a FrameworkVersion. Requirements map to
// SCF anchors via Mapping edges.
type Requirement struct {
	ID                 string `json:"id"`
	FrameworkVersionID string `json:"framework_version_id"`
	Code               string `json:"code"`
	Text               string `json:"text"`
}

// Mapping is one STRM-typed edge between a Requirement and an Anchor.
// STRMType is one of "equal" | "subset_of" | "intersects" per NIST IR 8477.
type Mapping struct {
	RequirementID string  `json:"requirement_id"`
	AnchorID      string  `json:"anchor_id"`
	STRMType      string  `json:"strm_type"`
	Strength      float64 `json:"strength"`
}

// Store is the read-only surface the HTTP handlers consume. The in-memory
// implementation returns slices owned by the seed (caller must not mutate).
type Store interface {
	ListAnchors() []Anchor
	Anchor(id string) (Anchor, bool)
	RequirementsForAnchor(anchorID string) []RequirementWithMapping
}

// RequirementWithMapping is the join the SCF detail page renders: a
// requirement plus the STRM edge that linked it to the anchor.
type RequirementWithMapping struct {
	Requirement      Requirement      `json:"requirement"`
	FrameworkVersion FrameworkVersion `json:"framework_version"`
	STRMType         string           `json:"strm_type"`
	Strength         float64          `json:"strength"`
}

// Seed is the in-memory Store.
type Seed struct {
	anchors         []Anchor
	frameworkByID   map[string]FrameworkVersion
	requirements    []Requirement
	mappingsByAnch  map[string][]Mapping
	requirementByID map[string]Requirement
}

// New returns a Seed populated with the slice-005 sample data.
func New() *Seed {
	anchors := defaultAnchors()
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
	mappingsByAnch := make(map[string][]Mapping)
	for _, m := range mappings {
		mappingsByAnch[m.AnchorID] = append(mappingsByAnch[m.AnchorID], m)
	}

	return &Seed{
		anchors:         anchors,
		frameworkByID:   frameworkByID,
		requirements:    requirements,
		mappingsByAnch:  mappingsByAnch,
		requirementByID: requirementByID,
	}
}

// ListAnchors returns the anchor catalog ordered by SCF id.
func (s *Seed) ListAnchors() []Anchor {
	out := make([]Anchor, len(s.anchors))
	copy(out, s.anchors)
	return out
}

// Anchor looks up a single anchor by its id. Returns (zero, false) when
// the id is unknown.
func (s *Seed) Anchor(id string) (Anchor, bool) {
	for _, a := range s.anchors {
		if a.ID == id {
			return a, true
		}
	}
	return Anchor{}, false
}

// RequirementsForAnchor returns every requirement that maps to anchorID,
// joined with its framework-version metadata and the STRM edge details.
func (s *Seed) RequirementsForAnchor(anchorID string) []RequirementWithMapping {
	mappings := s.mappingsByAnch[anchorID]
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
