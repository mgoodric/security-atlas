// Package catalog hosts shared, read-only mappings over the global SCF /
// framework catalog. The first inhabitant is the framework-slug → display-
// abbreviation map introduced by slice 226 to render the /controls list's
// per-row Frameworks column.
//
// Constitutional positioning:
//   - Invariant 1 (one control, N framework satisfactions) — the
//     abbreviation map is keyed on `frameworks.slug`, which is the stable
//     identifier of a framework across versions. There is no per-version
//     branching; SOC 2 v2017 and SOC 2 v2025 both render as `SOC2`.
//   - Invariant 7 (SCF is the canonical control catalog) — the SCF spine
//     itself is intentionally NOT in this map. The /controls list shows
//     frameworks an SCF anchor SATISFIES; the anchor's own framework
//     (always SCF) is implicit in its presence on the page.
//
// Source of truth: this file. The frontend renders abbreviations the BFF
// emits verbatim; the frontend MUST NOT carry its own copy of the map
// (slice 226 P0-226-2). Adding a framework here is the single place to
// add a new short-code.
package catalog

import "sort"

// FrameworkDisplayCode returns the mockup-style short abbreviation for a
// framework slug (e.g. `soc2` → `SOC2`). Unknown slugs round-trip the
// uppercased slug unchanged — the column is informational and falling
// back to the upper-cased slug is honest about catalog drift (a new
// framework imported before the map is updated still renders, just not
// in the polished form).
//
// The handful of slugs that today have a polished abbreviation:
//
//	soc2      → SOC2
//	iso27001  → ISO
//	nist_csf  → CSF
//	pci_dss   → PCI
//	hipaa     → HIPAA
//	gdpr      → GDPR
//
// These align with the AC-2 list in `docs/issues/226-*.md` and the
// FRAMEWORK_OPTIONS pill values in `web/app/(authed)/controls/page.tsx`.
func FrameworkDisplayCode(slug string) string {
	if v, ok := frameworkDisplayCodes[slug]; ok {
		return v
	}
	return defaultDisplayCode(slug)
}

// FrameworkDisplayCodes maps every input slug to its display abbreviation
// in one pass. Order-preserving: the output slice mirrors the input
// slice's order so the column shape stays stable across requests. NIL
// input → nil output (the BFF / wire layer treats nil as the empty-set
// branch per AC-6).
func FrameworkDisplayCodes(slugs []string) []string {
	if slugs == nil {
		return nil
	}
	out := make([]string, len(slugs))
	for i, s := range slugs {
		out[i] = FrameworkDisplayCode(s)
	}
	return out
}

// SortedFrameworkDisplayCodes is the AC-7 helper the slice 226 anchor
// list handler calls: convert each slug to its display abbreviation and
// sort the result by the display value so wire output is deterministic
// across runs / Postgres planner orderings. Duplicates are de-duped on
// the slug side already (array_agg DISTINCT in the SQL), so the output
// has the same length as the input.
func SortedFrameworkDisplayCodes(slugs []string) []string {
	out := FrameworkDisplayCodes(slugs)
	if out == nil {
		return nil
	}
	sort.Strings(out)
	return out
}

// frameworkDisplayCodes is the authoritative map. Keep aligned with the
// slice 226 spec AC-2 list. New framework imports MUST add an entry here
// (or accept the upper-cased-slug fallback).
var frameworkDisplayCodes = map[string]string{
	"soc2":     "SOC2",
	"iso27001": "ISO",
	"nist_csf": "CSF",
	"pci_dss":  "PCI",
	"hipaa":    "HIPAA",
	"gdpr":     "GDPR",
}

// defaultDisplayCode is the fallback for slugs not in the canonical map.
// Returns the upper-cased slug verbatim — preserves catalog drift
// transparency rather than erasing unknown frameworks from the UI. An
// empty input returns the empty string; the caller decides whether that
// makes it through the wire layer (it normally won't — array_agg never
// emits an empty string row).
func defaultDisplayCode(slug string) string {
	if slug == "" {
		return ""
	}
	// Inline uppercase to avoid pulling in strings.ToUpper for one call
	// site that only handles ASCII slugs (framework slugs are
	// `[a-z0-9_]+` per the SCF importer's convention).
	out := make([]byte, len(slug))
	for i := 0; i < len(slug); i++ {
		c := slug[i]
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}
