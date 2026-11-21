package control

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"

	"gopkg.in/yaml.v3"
)

// inPlaceManifestKeys are the top-level control.yaml keys an upload may
// change without versioning the control: prose (issue #1908), the trace
// to outside identifiers (issue #1958) and the anchor-fit note (issue
// #2023). None feeds evaluation. `anchor_fit` itself is not here: a fit
// change alters what the SCF mapping means, so it supersedes.
var inPlaceManifestKeys = []string{"description", "external_refs", "anchor_fit_note"}

// inPlaceQueryKeys are the keys inside each evidence_queries[] entry an
// upload may change in place (issue #2891). A query's description is prose
// the evaluator never reads. Its id, language, expression, evidence_kind and
// any other key still supersede. Entries compare position by position, so a
// renamed, added, removed or reordered query supersedes too.
var inPlaceQueryKeys = []string{"description"}

// manifestsEqualIgnoringMetadata reports whether an upload of nextYAML over
// an active control built from priorYAML updates in place: SupersedingFields
// is empty.
func manifestsEqualIgnoringMetadata(priorYAML, nextYAML []byte) (bool, error) {
	fields, err := SupersedingFields(priorYAML, nextYAML)
	if err != nil {
		return false, err
	}
	return len(fields) == 0, nil
}

// SupersedingFields lists, sorted, the control.yaml fields whose change
// makes an upload of nextYAML over an active control built from priorYAML
// mint a new control version. Empty means the upload updates in place.
//
// Both sides decode to generic maps, so key order, comments and formatting
// are not differences, and the in-place keys (inPlaceManifestKeys at the
// top level, inPlaceQueryKeys in each evidence_queries[] entry) are
// ignored. A change inside evidence_queries is named
// `evidence_queries[<id>].<key>` when both sides hold the same number of
// entries, and `evidence_queries` otherwise.
//
// Store.Upload and the stock-bundle CI tripwire
// (cmd/scripts/stock-supersede-check) both call this, so the tripwire
// cannot drift from what an upload does.
func SupersedingFields(priorYAML, nextYAML []byte) ([]string, error) {
	var prior, next map[string]any
	if err := yaml.Unmarshal(priorYAML, &prior); err != nil {
		return nil, fmt.Errorf("parse prior control manifest: %w", err)
	}
	if err := yaml.Unmarshal(nextYAML, &next); err != nil {
		return nil, fmt.Errorf("parse control manifest: %w", err)
	}
	for _, m := range []map[string]any{prior, next} {
		for _, k := range inPlaceManifestKeys {
			delete(m, k)
		}
		stripQueryKeys(m)
	}
	var out []string
	for _, k := range unionKeys(prior, next) {
		if reflect.DeepEqual(prior[k], next[k]) {
			continue
		}
		if k == "evidence_queries" {
			out = append(out, queryDiffFields(prior[k], next[k])...)
			continue
		}
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

// stripQueryKeys deletes inPlaceQueryKeys from every mapping entry of m's
// evidence_queries list. A value of any other shape is left as written.
func stripQueryKeys(m map[string]any) {
	list, ok := m["evidence_queries"].([]any)
	if !ok {
		return
	}
	for _, e := range list {
		if q, ok := e.(map[string]any); ok {
			for _, k := range inPlaceQueryKeys {
				delete(q, k)
			}
		}
	}
}

// queryDiffFields names what differs between two unequal evidence_queries
// values, entry by entry, labelling each entry by its prior id (its index
// when it has none).
func queryDiffFields(prior, next any) []string {
	pl, pok := prior.([]any)
	nl, nok := next.([]any)
	if !pok || !nok || len(pl) != len(nl) {
		return []string{"evidence_queries"}
	}
	var out []string
	for i := range pl {
		if reflect.DeepEqual(pl[i], nl[i]) {
			continue
		}
		pq, pok := pl[i].(map[string]any)
		nq, nok := nl[i].(map[string]any)
		if !pok || !nok {
			out = append(out, "evidence_queries["+strconv.Itoa(i)+"]")
			continue
		}
		label := strconv.Itoa(i)
		if id, ok := pq["id"].(string); ok && id != "" {
			label = id
		}
		for _, k := range unionKeys(pq, nq) {
			if !reflect.DeepEqual(pq[k], nq[k]) {
				out = append(out, "evidence_queries["+label+"]."+k)
			}
		}
	}
	return out
}

func unionKeys(a, b map[string]any) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	var out []string
	for _, m := range []map[string]any{a, b} {
		for k := range m {
			if _, ok := seen[k]; !ok {
				seen[k] = struct{}{}
				out = append(out, k)
			}
		}
	}
	return out
}
