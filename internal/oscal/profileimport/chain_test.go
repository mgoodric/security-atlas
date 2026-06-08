// Pure-Go unit tests (no Postgres, no bridge, no build tag) for the slice-578
// chained-resolution graph validator. These are the LOAD-BEARING tests: cycle
// detection (P0-578-2), the depth bound (AC-3), and the no-external-dereference
// guard (P0-578-1) are proven here WITHOUT compliance-trestle, exactly as the
// brief requires. The bridge integration test proves the end-to-end resolve;
// the safety properties live here where they are fast + deterministic.

package profileimport

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// profileDoc builds a profile JSON document with a uuid and the given import
// hrefs, for the chain-graph tests.
func profileDoc(uuid string, hrefs ...string) []byte {
	var imps []string
	for _, h := range hrefs {
		imps = append(imps, fmt.Sprintf(`{"href":%q}`, h))
	}
	return []byte(fmt.Sprintf(`{"profile":{"uuid":%q,"metadata":{"title":%q},"imports":[%s]}}`,
		uuid, uuid, strings.Join(imps, ",")))
}

// catalogDoc builds a catalog JSON document with a uuid.
func catalogDoc(uuid string) []byte {
	return []byte(fmt.Sprintf(`{"catalog":{"uuid":%q,"metadata":{"title":%q}}}`, uuid, uuid))
}

// buildAndValidate is the helper under test: build the chain graph then
// validate it, as Import does before the bridge call.
func buildAndValidate(t *testing.T, entry []byte, profiles, catalogs [][]byte) error {
	t.Helper()
	docs, err := buildChainDocs(entry, profiles, catalogs)
	if err != nil {
		return err
	}
	return validateChain(docs)
}

// ===== AC-2: a profile-over-profile chain resolves end-to-end (Go-side safe) =====

func TestChain_SingleLevel_ProfileOverCatalog(t *testing.T) {
	t.Parallel()
	// The slice-511 base case: entry profile -> catalog. Must still pass.
	entry := profileDoc("entry", "#cat")
	err := buildAndValidate(t, entry, nil, [][]byte{catalogDoc("cat")})
	if err != nil {
		t.Fatalf("single-level chain should validate, got %v", err)
	}
}

func TestChain_TwoLevel_AtoBtoCatalog(t *testing.T) {
	t.Parallel()
	// A -> B -> catalog: the headline slice-578 shape.
	entry := profileDoc("A", "#B")
	mid := profileDoc("B", "#cat")
	err := buildAndValidate(t, entry, [][]byte{mid}, [][]byte{catalogDoc("cat")})
	if err != nil {
		t.Fatalf("A->B->catalog should validate, got %v", err)
	}
}

func TestChain_ThreeLevel_AtoBtoCtoCatalog(t *testing.T) {
	t.Parallel()
	entry := profileDoc("A", "#B")
	b := profileDoc("B", "#C")
	c := profileDoc("C", "#cat")
	err := buildAndValidate(t, entry, [][]byte{b, c}, [][]byte{catalogDoc("cat")})
	if err != nil {
		t.Fatalf("A->B->C->catalog should validate, got %v", err)
	}
}

// ===== AC-3 / P0-578-2: cycle detection — A -> B -> A is rejected =====

func TestChain_Cycle_AtoBtoA(t *testing.T) {
	t.Parallel()
	entry := profileDoc("A", "#B")
	b := profileDoc("B", "#A") // imports back to A — a cycle.
	err := buildAndValidate(t, entry, [][]byte{b}, [][]byte{catalogDoc("cat")})
	if !errors.Is(err, ErrChainCycle) {
		t.Fatalf("A->B->A must be rejected as a cycle, got %v", err)
	}
}

func TestChain_Cycle_SelfImport(t *testing.T) {
	t.Parallel()
	// A profile that imports itself directly.
	entry := profileDoc("A", "#A")
	err := buildAndValidate(t, entry, nil, [][]byte{catalogDoc("cat")})
	if !errors.Is(err, ErrChainCycle) {
		t.Fatalf("A->A must be rejected as a cycle, got %v", err)
	}
}

func TestChain_Cycle_ThreeNode(t *testing.T) {
	t.Parallel()
	// A -> B -> C -> A.
	entry := profileDoc("A", "#B")
	b := profileDoc("B", "#C")
	c := profileDoc("C", "#A")
	err := buildAndValidate(t, entry, [][]byte{b, c}, [][]byte{catalogDoc("cat")})
	if !errors.Is(err, ErrChainCycle) {
		t.Fatalf("A->B->C->A must be rejected as a cycle, got %v", err)
	}
}

// ===== AC-3: depth bound — a chain deeper than MaxChainDepth is rejected =====

func TestChain_DepthExceeded(t *testing.T) {
	t.Parallel()
	// Build a linear chain of MaxChainDepth+2 profiles ending at a catalog.
	// p0 (entry) -> p1 -> ... -> p[N+1] -> cat. Depth of the last profile is
	// past MaxChainDepth, so the chain must be rejected.
	n := MaxChainDepth + 2
	entry := profileDoc("p0", "#p1")
	var profiles [][]byte
	for i := 1; i < n; i++ {
		profiles = append(profiles, profileDoc(fmt.Sprintf("p%d", i), fmt.Sprintf("#p%d", i+1)))
	}
	// The deepest profile imports the catalog.
	profiles = append(profiles, profileDoc(fmt.Sprintf("p%d", n), "#cat"))
	err := buildAndValidate(t, entry, profiles, [][]byte{catalogDoc("cat")})
	if !errors.Is(err, ErrChainTooDeep) {
		t.Fatalf("a chain deeper than MaxChainDepth must be rejected, got %v", err)
	}
}

func TestChain_DepthAtBound_OK(t *testing.T) {
	t.Parallel()
	// A chain of exactly MaxChainDepth profiles ending at a catalog validates.
	// p1(entry,depth1) -> p2(depth2) -> ... -> p[MaxChainDepth] -> cat.
	entry := profileDoc("p1", "#p2")
	var profiles [][]byte
	for i := 2; i < MaxChainDepth; i++ {
		profiles = append(profiles, profileDoc(fmt.Sprintf("p%d", i), fmt.Sprintf("#p%d", i+1)))
	}
	profiles = append(profiles, profileDoc(fmt.Sprintf("p%d", MaxChainDepth), "#cat"))
	err := buildAndValidate(t, entry, profiles, [][]byte{catalogDoc("cat")})
	if err != nil {
		t.Fatalf("a chain at exactly MaxChainDepth (%d) should validate, got %v", MaxChainDepth, err)
	}
}

// ===== P0-578-1: no external dereference at ANY chain link =====

func TestChain_ExternalHref_EntryProfile(t *testing.T) {
	t.Parallel()
	for _, ext := range []string{
		"https://attacker.example/c.json",
		"http://attacker.example/c.json",
		"sftp://host/c.json",
		"ftp://host/c.json",
		"file:///etc/passwd",
		"//evil/c.json",
	} {
		entry := profileDoc("A", ext)
		err := buildAndValidate(t, entry, nil, [][]byte{catalogDoc("cat")})
		if !errors.Is(err, ErrExternalImport) {
			t.Errorf("external href %q must be rejected, got %v", ext, err)
		}
	}
}

func TestChain_ExternalHref_DeepLink(t *testing.T) {
	t.Parallel()
	// An external href two levels deep is still rejected (every link checked).
	entry := profileDoc("A", "#B")
	b := profileDoc("B", "https://attacker.example/c.json")
	err := buildAndValidate(t, entry, [][]byte{b}, [][]byte{catalogDoc("cat")})
	if !errors.Is(err, ErrExternalImport) {
		t.Fatalf("a deep external href must be rejected, got %v", err)
	}
}

// ===== carry-forward P0-511-1: an unresolvable href is rejected, no fetch =====

func TestChain_UnresolvableHref(t *testing.T) {
	t.Parallel()
	// Entry imports "#missing" which matches no supplied document.
	entry := profileDoc("A", "#missing")
	err := buildAndValidate(t, entry, nil, [][]byte{catalogDoc("cat")})
	if !errors.Is(err, ErrUnresolvableImport) {
		t.Fatalf("an unresolvable href must be rejected, got %v", err)
	}
}

func TestChain_ProfileWithNoImports(t *testing.T) {
	t.Parallel()
	// A profile with no imports cannot reach a catalog — unresolvable.
	entry := []byte(`{"profile":{"uuid":"A","imports":[]}}`)
	err := buildAndValidate(t, entry, nil, [][]byte{catalogDoc("cat")})
	if !errors.Is(err, ErrUnresolvableImport) {
		t.Fatalf("a profile with no imports must be rejected, got %v", err)
	}
}

func TestChain_EmptyHref(t *testing.T) {
	t.Parallel()
	entry := profileDoc("A", "")
	err := buildAndValidate(t, entry, nil, [][]byte{catalogDoc("cat")})
	if !errors.Is(err, ErrUnresolvableImport) {
		t.Fatalf("an empty href must be rejected, got %v", err)
	}
}

// ===== malformed supplied documents =====

func TestChain_MalformedEntryProfile(t *testing.T) {
	t.Parallel()
	err := buildAndValidate(t, []byte(`{"catalog":{}}`), nil, [][]byte{catalogDoc("cat")})
	if !errors.Is(err, ErrMalformedChainDoc) {
		t.Fatalf("a non-profile entry must be ErrMalformedChainDoc, got %v", err)
	}
}

func TestChain_MalformedSuppliedCatalog(t *testing.T) {
	t.Parallel()
	entry := profileDoc("A", "#cat")
	err := buildAndValidate(t, entry, nil, [][]byte{[]byte(`{"profile":{}}`)})
	if !errors.Is(err, ErrMalformedChainDoc) {
		t.Fatalf("a non-catalog supplied catalog must be ErrMalformedChainDoc, got %v", err)
	}
}

// ===== diamond is NOT a cycle (shared dependency on two branches) =====

func TestChain_Diamond_NotACycle(t *testing.T) {
	t.Parallel()
	// A imports B and C; both B and C import the same catalog. A diamond over
	// a shared catalog terminal is legal — the onPath set must be popped on
	// exit so the second branch's reach of the catalog is not a false cycle.
	entry := profileDoc("A", "#B", "#C")
	b := profileDoc("B", "#cat")
	c := profileDoc("C", "#cat")
	err := buildAndValidate(t, entry, [][]byte{b, c}, [][]byte{catalogDoc("cat")})
	if err != nil {
		t.Fatalf("a diamond over a shared catalog should validate, got %v", err)
	}
}

// ===== href matching: by title-slug when no uuid =====

func TestChain_MatchByTitleSlug(t *testing.T) {
	t.Parallel()
	// A catalog with no uuid but a title; the import matches by title slug.
	entry := []byte(`{"profile":{"uuid":"A","imports":[{"href":"nist-base.json"}]}}`)
	cat := []byte(`{"catalog":{"metadata":{"title":"NIST Base"}}}`)
	err := buildAndValidate(t, entry, nil, [][]byte{cat})
	if err != nil {
		t.Fatalf("title-slug match should validate, got %v", err)
	}
}

// ===== provenance helpers =====

func TestChainProvenance_RecordsAllDocsWithHashes(t *testing.T) {
	t.Parallel()
	req := Request{
		ProfileJSON: profileDoc("A", "#B"),
		Profiles:    [][]byte{profileDoc("B", "#cat")},
		Catalogs:    [][]byte{catalogDoc("cat")},
	}
	links := chainProvenance(req)
	if len(links) != 3 {
		t.Fatalf("expected 3 chain links (entry + 1 profile + 1 catalog), got %d", len(links))
	}
	if links[0].Role != "entry-profile" || links[1].Role != "profile" || links[2].Role != "catalog" {
		t.Errorf("unexpected roles: %+v", links)
	}
	for _, l := range links {
		if len(l.Sha256) != 64 {
			t.Errorf("link %q sha256 is not a 64-hex hash: %q", l.Role, l.Sha256)
		}
		if l.Bytes == 0 {
			t.Errorf("link %q has zero bytes", l.Role)
		}
	}
	if d := chainProfileDepth(links); d != 2 {
		t.Errorf("chainProfileDepth = %d, want 2 (entry + 1 intermediate)", d)
	}
}

func TestSlugify(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"NIST Base":     "nist-base",
		"  Hello!! ":    "hello",
		"FedRAMP_High":  "fedramp-high",
		"a---b":         "a-b",
		"":              "",
		"UPPER lower 9": "upper-lower-9",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsExternalHref(t *testing.T) {
	t.Parallel()
	ext := []string{"https://x", "HTTP://x", "  sftp://x", "ftp://x", "file:/x", "//x"}
	for _, h := range ext {
		if !isExternalHref(h) {
			t.Errorf("isExternalHref(%q) = false, want true", h)
		}
	}
	internal := []string{"#uuid", "catalog.json", "trestle://x", "./local"}
	for _, h := range internal {
		if isExternalHref(h) {
			t.Errorf("isExternalHref(%q) = true, want false", h)
		}
	}
}
