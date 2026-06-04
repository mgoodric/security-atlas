package catalog

import (
	"reflect"
	"testing"
)

// Slice 226 — framework display-abbreviation authority. These tests pin
// the canonical map per AC-2 / AC-7 and the empty-set / unknown-slug
// fallback contract. A future framework addition (e.g. CCM, FedRAMP)
// MUST land alongside an additional test case here.

func TestFrameworkDisplayCode_CanonicalSlugs(t *testing.T) {
	// AC-2 + AC-7 — the six v1 frameworks have stable abbreviations
	// matching `Plans/_archive/mockups/controls.html` line 217.
	cases := map[string]string{
		"soc2":     "SOC2",
		"iso27001": "ISO",
		"nist_csf": "CSF",
		"pci_dss":  "PCI",
		"hipaa":    "HIPAA",
		"gdpr":     "GDPR",
	}
	for slug, want := range cases {
		t.Run(slug, func(t *testing.T) {
			if got := FrameworkDisplayCode(slug); got != want {
				t.Errorf("FrameworkDisplayCode(%q) = %q; want %q", slug, got, want)
			}
		})
	}
}

func TestFrameworkDisplayCode_UnknownSlugFallsBackToUpperCase(t *testing.T) {
	// Catalog-drift fallback: a slug not in the canonical map renders as
	// its upper-cased self. Preserves honesty (we don't erase the
	// framework from the UI) without forcing a code update for every
	// imported framework.
	cases := map[string]string{
		"ccm":          "CCM",
		"fedramp":      "FEDRAMP",
		"new_standard": "NEW_STANDARD",
	}
	for slug, want := range cases {
		t.Run(slug, func(t *testing.T) {
			if got := FrameworkDisplayCode(slug); got != want {
				t.Errorf("FrameworkDisplayCode(%q) = %q; want %q", slug, got, want)
			}
		})
	}
}

func TestFrameworkDisplayCode_EmptyStringReturnsEmpty(t *testing.T) {
	if got := FrameworkDisplayCode(""); got != "" {
		t.Errorf("FrameworkDisplayCode(\"\") = %q; want empty", got)
	}
}

func TestFrameworkDisplayCodes_PreservesOrderAndMapsEachSlug(t *testing.T) {
	// The raw map function preserves input order so the caller can
	// decide on its own sort.
	in := []string{"soc2", "iso27001", "nist_csf", "gdpr"}
	want := []string{"SOC2", "ISO", "CSF", "GDPR"}
	got := FrameworkDisplayCodes(in)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FrameworkDisplayCodes(%v) = %v; want %v", in, got, want)
	}
}

func TestFrameworkDisplayCodes_NilInputReturnsNil(t *testing.T) {
	// AC-6: anchors with no satisfaction edges produce nil slugs at the
	// DB layer. The map function must pass nil through so the wire layer
	// can render `—` rather than `[]`-shaped UI noise.
	if got := FrameworkDisplayCodes(nil); got != nil {
		t.Errorf("FrameworkDisplayCodes(nil) = %v; want nil", got)
	}
}

func TestFrameworkDisplayCodes_EmptySliceReturnsEmptySlice(t *testing.T) {
	// Defensive: an empty (non-nil) slice round-trips as an empty
	// non-nil slice. JSON renders both as `[]` so the wire shape is
	// identical, but in Go the nil vs empty distinction matters for
	// the AC-6 dash fallback (nil → no edges; empty → edges all
	// filtered out — same UI outcome).
	got := FrameworkDisplayCodes([]string{})
	if got == nil {
		t.Errorf("FrameworkDisplayCodes([]) = nil; want empty slice")
	}
	if len(got) != 0 {
		t.Errorf("FrameworkDisplayCodes([]) len = %d; want 0", len(got))
	}
}

func TestSortedFrameworkDisplayCodes_SortsAfterMapping(t *testing.T) {
	// The sort is on the DISPLAY value, not the slug — so `nist_csf`
	// (slug starts with N) sorts as `CSF` (starts with C). This
	// matches the mockup's left-to-right ordering of the chip strip.
	in := []string{"soc2", "iso27001", "nist_csf", "gdpr"}
	want := []string{"CSF", "GDPR", "ISO", "SOC2"}
	got := SortedFrameworkDisplayCodes(in)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SortedFrameworkDisplayCodes(%v) = %v; want %v", in, got, want)
	}
}

func TestSortedFrameworkDisplayCodes_NilInputReturnsNil(t *testing.T) {
	if got := SortedFrameworkDisplayCodes(nil); got != nil {
		t.Errorf("SortedFrameworkDisplayCodes(nil) = %v; want nil", got)
	}
}

func TestDefaultDisplayCode_IsASCIIUpperCase(t *testing.T) {
	// Defensive: the inline uppercase helper handles ASCII slug chars
	// only (the SCF importer enforces `[a-z0-9_]+` slugs). Non-ASCII
	// would pass through unchanged; this test pins the ASCII-only
	// contract so a future change doesn't silently start UTF-folding.
	if got := defaultDisplayCode("abc_123"); got != "ABC_123" {
		t.Errorf("defaultDisplayCode(abc_123) = %q; want ABC_123", got)
	}
}
