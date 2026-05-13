package seed_test

import (
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mgoodric/security-atlas/internal/policy/seed"
)

// TestLoadFromFS_RealStockBundle verifies the 5 bundled markdown files
// load cleanly and produce parseable frontmatter. Reads from the actual
// policies/stock directory in the repo (relative to the test cwd).
func TestLoadFromFS_RealStockBundle(t *testing.T) {
	// Test cwd is internal/policy/seed; the absolute path comes from the
	// known parent traversal. os.DirFS(".") with a "../" path is rejected
	// by io/fs (path must not escape root), so use os.DirFS on the repo
	// root directly.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// wd = .../internal/policy/seed → repo root is three levels up.
	repoRoot := wd + "/../../.."
	if _, err := os.Stat(repoRoot + "/policies/stock"); err != nil {
		t.Skipf("stock dir not reachable: %v", err)
	}
	policies, err := seed.LoadFromFS(os.DirFS(repoRoot), "policies/stock")
	if err != nil {
		t.Fatalf("LoadFromFS: %v", err)
	}
	if len(policies) != seed.StockPolicyCount {
		t.Fatalf("expected %d policies, got %d", seed.StockPolicyCount, len(policies))
	}
	// Every policy must have a non-empty title, version, owner_role,
	// approver_role, body, and >=3 linked_control_ids declared.
	for _, p := range policies {
		fm := p.FrontMatter
		if fm.Title == "" {
			t.Errorf("%s: empty title", p.SourcePath)
		}
		if fm.Version == "" {
			t.Errorf("%s: empty version", p.SourcePath)
		}
		if fm.OwnerRole == "" {
			t.Errorf("%s: empty owner_role", p.SourcePath)
		}
		if fm.ApproverRole == "" {
			t.Errorf("%s: empty approver_role", p.SourcePath)
		}
		if len(fm.LinkedControlIDs) < 3 {
			t.Errorf("%s: expected >=3 linked_control_ids, got %d", p.SourcePath, len(fm.LinkedControlIDs))
		}
		if strings.TrimSpace(p.BodyMd) == "" {
			t.Errorf("%s: empty body", p.SourcePath)
		}
		if fm.SourceAttribution != "community_draft" {
			t.Errorf("%s: expected community_draft attribution, got %q", p.SourcePath, fm.SourceAttribution)
		}
	}
}

// TestLoadFromFS_WrongCount_Rejects exercises the anti-criterion P0
// guard: the loader MUST reject any stock directory that does not
// contain exactly 5 markdown files.
func TestLoadFromFS_WrongCount_Rejects(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
	}{
		{
			name: "four files",
			files: map[string]string{
				"stock/a.md": stockFrontMatter("A"),
				"stock/b.md": stockFrontMatter("B"),
				"stock/c.md": stockFrontMatter("C"),
				"stock/d.md": stockFrontMatter("D"),
			},
		},
		{
			name: "six files",
			files: map[string]string{
				"stock/a.md": stockFrontMatter("A"),
				"stock/b.md": stockFrontMatter("B"),
				"stock/c.md": stockFrontMatter("C"),
				"stock/d.md": stockFrontMatter("D"),
				"stock/e.md": stockFrontMatter("E"),
				"stock/f.md": stockFrontMatter("F"),
			},
		},
		{
			name: "zero markdown files (placeholder readme present)",
			files: map[string]string{
				"stock/README.txt": "no markdown here",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fsys := buildFS(tc.files)
			_, err := seed.LoadFromFS(fsys, "stock")
			if err == nil {
				t.Fatalf("expected error for %d files, got nil", len(tc.files))
			}
			if !strings.Contains(err.Error(), "exactly 5 markdown files") {
				t.Fatalf("expected anti-criterion guard message, got: %v", err)
			}
		})
	}
}

// TestParseStockPolicy_NoFrontMatter rejects files without YAML
// frontmatter.
func TestParseStockPolicy_NoFrontMatter(t *testing.T) {
	files := map[string]string{
		"stock/a.md": stockFrontMatter("A"),
		"stock/b.md": stockFrontMatter("B"),
		"stock/c.md": stockFrontMatter("C"),
		"stock/d.md": stockFrontMatter("D"),
		"stock/e.md": "# bare body, no frontmatter\n",
	}
	fsys := buildFS(files)
	_, err := seed.LoadFromFS(fsys, "stock")
	if err == nil {
		t.Fatal("expected error for missing frontmatter")
	}
	if !strings.Contains(err.Error(), "frontmatter") {
		t.Fatalf("expected frontmatter error, got: %v", err)
	}
}

// TestNoopAnchorResolver returns every code as missing. Used when no
// controls table is populated.
func TestNoopAnchorResolver_AllMissing(t *testing.T) {
	resolver := seed.NoopAnchorResolver{}
	codes := []string{"GOV-01", "IAC-07", "CHG-02"}
	resolved, missing, err := resolver.Resolve(nil, codes)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(resolved) != 0 {
		t.Fatalf("expected 0 resolved, got %d", len(resolved))
	}
	if len(missing) != len(codes) {
		t.Fatalf("expected %d missing, got %d", len(codes), len(missing))
	}
}

// stockFrontMatter is a minimal-valid stock-policy markdown body for
// fixture generation in tests.
func stockFrontMatter(title string) string {
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

// buildFS builds an in-memory fs.FS from a path->content map.
func buildFS(files map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{}
	for path, content := range files {
		fsys[path] = &fstest.MapFile{Data: []byte(content)}
	}
	return fsys
}

// Sanity check: errors.Is propagates correctly through the loader.
func TestLoad_NonexistentDir_Errors(t *testing.T) {
	_, err := seed.LoadFromFS(os.DirFS("."), "does-not-exist")
	if err == nil {
		t.Fatal("expected error for nonexistent dir")
	}
	if !strings.Contains(err.Error(), "read does-not-exist") {
		t.Fatalf("expected 'read does-not-exist' in error, got: %v", err)
	}
}
