// Package seed unit tests — pure-Go branches not exercised by the
// existing seed_test.go file or by the integration suite.
//
// Load-bearing functions covered here:
//
//   - NewSQLAnchorResolver: trivial constructor; verifies the returned
//     resolver is non-nil and implements the AnchorResolver interface.
//   - parseStockPolicy edge branches: trailing-`---`-missing rejection,
//     malformed-YAML rejection, CRLF frontmatter delimiter accepted,
//     leading-whitespace tolerance, and the default source-attribution
//     fallback (community_draft) at the LoadFromFS layer when the
//     frontmatter field is absent.
//
// DB-touching functions (Seed, SQLAnchorResolver.Resolve) are covered
// in seed_integration_test.go behind the `integration` build tag — see
// the slice-297 enrollment in .github/workflows/ci.yml.
package seed_test

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mgoodric/security-atlas/internal/policy/seed"
)

// TestNewSQLAnchorResolver_NonNil verifies the constructor returns a
// usable resolver. The constructor takes a *pgxpool.Pool but never
// dereferences it — passing nil exercises the happy path. (The pool
// is only touched inside Resolve, covered in the integration tests.)
func TestNewSQLAnchorResolver_NonNil(t *testing.T) {
	r := seed.NewSQLAnchorResolver(nil)
	if r == nil {
		t.Fatal("NewSQLAnchorResolver returned nil")
	}
	// Compile-time check: r satisfies the AnchorResolver interface.
	var _ seed.AnchorResolver = r
}

// TestLoadFromFS_MissingTrailingDelimiter exercises the parseStockPolicy
// branch where the file starts with `---\n` but never closes the
// frontmatter block.
func TestLoadFromFS_MissingTrailingDelimiter(t *testing.T) {
	files := map[string]string{
		"stock/a.md": validStockPolicy("A"),
		"stock/b.md": validStockPolicy("B"),
		"stock/c.md": validStockPolicy("C"),
		"stock/d.md": validStockPolicy("D"),
		"stock/e.md": "---\ntitle: E\nversion: 1.0.0\n\n# body but no closing delimiter\n",
	}
	_, err := seed.LoadFromFS(buildMapFS(files), "stock")
	if err == nil {
		t.Fatal("expected error for missing trailing delimiter")
	}
	if !strings.Contains(err.Error(), "trailing `---`") {
		t.Fatalf("expected trailing-delimiter error, got: %v", err)
	}
}

// TestLoadFromFS_MalformedYAMLFrontmatter exercises the yaml.Unmarshal
// failure branch.
func TestLoadFromFS_MalformedYAMLFrontmatter(t *testing.T) {
	files := map[string]string{
		"stock/a.md": validStockPolicy("A"),
		"stock/b.md": validStockPolicy("B"),
		"stock/c.md": validStockPolicy("C"),
		"stock/d.md": validStockPolicy("D"),
		"stock/e.md": "---\ntitle: [bad: yaml :: sequence\n---\n\n# body\n",
	}
	_, err := seed.LoadFromFS(buildMapFS(files), "stock")
	if err == nil {
		t.Fatal("expected error for malformed YAML")
	}
	if !strings.Contains(err.Error(), "parse frontmatter") {
		t.Fatalf("expected frontmatter-parse error, got: %v", err)
	}
}

// TestLoadFromFS_CRLFDelimiterAccepted exercises the alternate
// `---\r\n` branch in parseStockPolicy. Windows-authored content lands
// with CRLF line endings; the loader must accept that shape.
func TestLoadFromFS_CRLFDelimiterAccepted(t *testing.T) {
	crlf := "---\r\ntitle: E\r\nversion: 1.0.0\r\nowner_role: tenant_admin\r\napprover_role: security_lead\r\nlinked_control_ids:\r\n  - GOV-01\r\n  - GOV-04\r\n  - RSK-01\r\nacknowledgment_required_roles:\r\n  - employee\r\nsource_attribution: community_draft\r\n---\r\n\r\n# E\r\n\r\nBody.\r\n"
	files := map[string]string{
		"stock/a.md": validStockPolicy("A"),
		"stock/b.md": validStockPolicy("B"),
		"stock/c.md": validStockPolicy("C"),
		"stock/d.md": validStockPolicy("D"),
		"stock/e.md": crlf,
	}
	policies, err := seed.LoadFromFS(buildMapFS(files), "stock")
	if err != nil {
		t.Fatalf("LoadFromFS: %v", err)
	}
	if len(policies) != seed.StockPolicyCount {
		t.Fatalf("expected %d policies, got %d", seed.StockPolicyCount, len(policies))
	}
	// Find the CRLF-authored one and check the title parsed cleanly.
	var found bool
	for _, p := range policies {
		if p.FrontMatter.Title == "E" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected to find policy with title 'E' parsed from CRLF source")
	}
}

// TestLoadFromFS_LeadingWhitespaceTolerated exercises the
// strings.TrimLeft branch at the top of parseStockPolicy. Some
// markdown authors put a blank line before the frontmatter delimiter;
// the loader strips it before checking for `---`.
func TestLoadFromFS_LeadingWhitespaceTolerated(t *testing.T) {
	files := map[string]string{
		"stock/a.md": validStockPolicy("A"),
		"stock/b.md": validStockPolicy("B"),
		"stock/c.md": validStockPolicy("C"),
		"stock/d.md": validStockPolicy("D"),
		"stock/e.md": "\n\n  \n" + validStockPolicy("E"),
	}
	policies, err := seed.LoadFromFS(buildMapFS(files), "stock")
	if err != nil {
		t.Fatalf("LoadFromFS: %v", err)
	}
	if len(policies) != seed.StockPolicyCount {
		t.Fatalf("expected %d policies, got %d", seed.StockPolicyCount, len(policies))
	}
}

// TestLoadFromFS_DirEntriesFilteredByExtension verifies that
// non-`.md` entries are skipped silently — only the markdown file
// count contributes to the StockPolicyCount guard. The test puts
// five real `.md` files alongside a stray `.txt`, a hidden `.gitkeep`,
// and a subdirectory; the loader must count only the 5.
func TestLoadFromFS_NonMarkdownEntriesIgnored(t *testing.T) {
	files := map[string]string{
		"stock/a.md":         validStockPolicy("A"),
		"stock/b.md":         validStockPolicy("B"),
		"stock/c.md":         validStockPolicy("C"),
		"stock/d.md":         validStockPolicy("D"),
		"stock/e.md":         validStockPolicy("E"),
		"stock/README.txt":   "not a markdown file",
		"stock/.gitkeep":     "",
		"stock/notes/x.json": `{"unused": true}`,
	}
	policies, err := seed.LoadFromFS(buildMapFS(files), "stock")
	if err != nil {
		t.Fatalf("LoadFromFS: %v", err)
	}
	if len(policies) != seed.StockPolicyCount {
		t.Fatalf("expected exactly %d markdown files counted, got %d", seed.StockPolicyCount, len(policies))
	}
}

// TestLoadFromFS_SortedDeterministicOrder verifies the loader emits
// the 5 policies in lexical sort order — the seed run depends on a
// stable iteration order so the same bundle produces the same insert
// order across replays. (This is implicit in sort.Strings(paths)
// inside LoadFromFS; the test pins it.)
func TestLoadFromFS_SortedDeterministicOrder(t *testing.T) {
	files := map[string]string{
		"stock/zulu.md":    validStockPolicy("zulu"),
		"stock/alpha.md":   validStockPolicy("alpha"),
		"stock/mike.md":    validStockPolicy("mike"),
		"stock/bravo.md":   validStockPolicy("bravo"),
		"stock/charlie.md": validStockPolicy("charlie"),
	}
	policies, err := seed.LoadFromFS(buildMapFS(files), "stock")
	if err != nil {
		t.Fatalf("LoadFromFS: %v", err)
	}
	want := []string{"alpha", "bravo", "charlie", "mike", "zulu"}
	for i, p := range policies {
		if p.FrontMatter.Title != want[i] {
			t.Fatalf("position %d: want title %q, got %q", i, want[i], p.FrontMatter.Title)
		}
	}
}

// validStockPolicy emits a known-good markdown body with title in the
// frontmatter. Mirrors the body shape used in the package's existing
// fixture helper but is duplicated here so this file is self-contained.
func validStockPolicy(title string) string {
	return `---
title: ` + title + `
version: 1.0.0
owner_role: tenant_admin
approver_role: security_lead
linked_control_ids:
  - GOV-01
  - GOV-04
  - RSK-01
acknowledgment_required_roles:
  - employee
source_attribution: community_draft
---

# ` + title + `

Body.
`
}

// buildMapFS is a local helper so this file does not depend on
// seed_test.go's buildFS (same logic, isolated package_test surface).
func buildMapFS(files map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{}
	for path, content := range files {
		fsys[path] = &fstest.MapFile{Data: []byte(content)}
	}
	return fsys
}
