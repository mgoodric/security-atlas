package soc2import

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
)

// PointOfFocus is one SOC 2 point of focus rolled up onto its parent
// criterion (#1731): its code and the SCF anchors the source mapped it to.
// It is metadata on the criterion, never a requirement of its own, so it
// never counts toward requirement_count or coverage.
type PointOfFocus struct {
	Code       string   `yaml:"code" json:"code"`
	SCFAnchors []string `yaml:"scf_anchors" json:"scf_anchors"`
}

// PointsOfFocusRollUp summarizes what the workbook roll-up did, for the
// import summary. Zero for a crosswalk with no points of focus.
type PointsOfFocusRollUp struct {
	// PointsOfFocus is how many distinct point-of-focus codes were folded.
	PointsOfFocus int
	// Criteria is how many parent criteria received them.
	Criteria int
	// CreatedParents lists criteria the column named only through their
	// points of focus; the roll-up created them. Sorted.
	CreatedParents []string
}

// Summary renders the roll-up as import-summary lines; empty when nothing
// was rolled up.
func (r PointsOfFocusRollUp) Summary() []string {
	if r.PointsOfFocus == 0 {
		return nil
	}
	lines := []string{fmt.Sprintf("points of focus rolled up=%d into criteria=%d", r.PointsOfFocus, r.Criteria)}
	for _, c := range r.CreatedParents {
		lines = append(lines, fmt.Sprintf("criterion %s created from its points of focus (the column has no bare %s)", c, c))
	}
	return lines
}

// pointOfFocusRe matches a SOC 2 point-of-focus code such as "CC1.1-POF3",
// capturing the parent criterion and the ordinal.
var pointOfFocusRe = regexp.MustCompile(`^(.+)-POF(\d+)$`)

// parsePointOfFocus returns the parent criterion and ordinal of a
// point-of-focus code, or ok=false for any other code.
func parsePointOfFocus(code string) (parent string, n int, ok bool) {
	m := pointOfFocusRe.FindStringSubmatch(code)
	if m == nil {
		return "", 0, false
	}
	n, err := strconv.Atoi(m[2])
	if err != nil {
		return "", 0, false
	}
	return m[1], n, true
}

// rollUpPointsOfFocus folds point-of-focus codes into their parent criterion
// (#1731). The SCF workbook's SOC 2 column lists the criteria alongside their
// points of focus; an auditor tests against criteria, so:
//
//   - a point of focus never becomes a requirement; its parent records it as
//     metadata (code + SCF anchors), and a parent the column never names
//     bare is created and reported;
//   - its mapping becomes an edge from the parent to the same anchor. The
//     criterion's own edge to that anchor wins; otherwise the strongest
//     point-of-focus edge does (the first one on a tie). Edges are never
//     duplicated.
//
// reqs is keyed by code and is modified in place; the returned mappings
// replace the input. Crosswalks without points of focus pass through
// unchanged.
func rollUpPointsOfFocus(reqs map[string]Requirement, mappings []Mapping) ([]Mapping, PointsOfFocusRollUp) {
	type pof struct {
		code    string
		n       int
		anchors map[string]struct{}
	}
	byParent := map[string]map[string]*pof{}
	ownEdge := map[string]struct{}{}
	for _, m := range mappings {
		if _, _, ok := parsePointOfFocus(m.RequirementCode); !ok {
			ownEdge[m.RequirementCode+"\x00"+m.SCFAnchor] = struct{}{}
		}
	}

	out := make([]Mapping, 0, len(mappings))
	rolledIdx := map[string]int{}
	for _, m := range mappings {
		parent, n, ok := parsePointOfFocus(m.RequirementCode)
		if !ok {
			out = append(out, m)
			continue
		}
		if byParent[parent] == nil {
			byParent[parent] = map[string]*pof{}
		}
		p := byParent[parent][m.RequirementCode]
		if p == nil {
			p = &pof{code: m.RequirementCode, n: n, anchors: map[string]struct{}{}}
			byParent[parent][m.RequirementCode] = p
		}
		p.anchors[m.SCFAnchor] = struct{}{}

		key := parent + "\x00" + m.SCFAnchor
		if _, ok := ownEdge[key]; ok {
			continue
		}
		rolled := m
		rolled.RequirementCode = parent
		rolled.Rationale = fmt.Sprintf("Rolled up from SOC 2 point of focus %s. %s", m.RequirementCode, m.Rationale)
		if i, seen := rolledIdx[key]; seen {
			if m.Strength > out[i].Strength {
				out[i] = rolled
			}
			continue
		}
		rolledIdx[key] = len(out)
		out = append(out, rolled)
	}

	var summary PointsOfFocusRollUp
	if len(byParent) == 0 {
		return mappings, summary
	}
	for code := range reqs {
		if _, _, ok := parsePointOfFocus(code); ok {
			delete(reqs, code)
		}
	}
	for parent, pofs := range byParent {
		r, ok := reqs[parent]
		if !ok {
			r = Requirement{Code: parent, Title: parent}
			summary.CreatedParents = append(summary.CreatedParents, parent)
		}
		list := make([]*pof, 0, len(pofs))
		for _, p := range pofs {
			list = append(list, p)
		}
		sort.Slice(list, func(i, j int) bool {
			if list[i].n != list[j].n {
				return list[i].n < list[j].n
			}
			return list[i].code < list[j].code
		})
		for _, p := range list {
			anchors := make([]string, 0, len(p.anchors))
			for a := range p.anchors {
				anchors = append(anchors, a)
			}
			sort.Strings(anchors)
			r.PointsOfFocus = append(r.PointsOfFocus, PointOfFocus{Code: p.code, SCFAnchors: anchors})
		}
		reqs[parent] = r
		summary.PointsOfFocus += len(list)
		summary.Criteria++
	}
	sort.Strings(summary.CreatedParents)
	return out, summary
}
