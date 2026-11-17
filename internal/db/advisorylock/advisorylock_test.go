package advisorylock

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestRegistry proves no two registered namespaces share a value or a name,
// and no two integration-test keys share a value (#2273).
func TestRegistry(t *testing.T) {
	t.Parallel()
	byNS := map[Namespace]string{}
	byName := map[string]Namespace{}
	for _, e := range All() {
		if e.Namespace <= 0 {
			t.Errorf("%s: namespace %d must be positive", e.Name, e.Namespace)
		}
		if prev, dup := byNS[e.Namespace]; dup {
			t.Errorf("namespace %d registered twice: %s and %s", e.Namespace, prev, e.Name)
		}
		if prev, dup := byName[e.Name]; dup {
			t.Errorf("name %q registered twice: %d and %d", e.Name, prev, e.Namespace)
		}
		byNS[e.Namespace] = e.Name
		byName[e.Name] = e.Namespace
	}
	keys := map[int32]bool{}
	for _, k := range TestKeys() {
		if keys[k] {
			t.Errorf("integration-test key %d used twice", k)
		}
		keys[k] = true
	}
}

// lockCall matches any advisory lock function call: pg_advisory_lock,
// pg_advisory_xact_lock, pg_try_advisory_xact_lock_shared, pg_advisory_unlock
// and the rest. Built from parts so this file does not match itself.
var lockCall = regexp.MustCompile(`pg_(?:try_)?` + `advisory_(?:xact_)?(?:un)?lock(?:_shared)?\s*\(`)

// lockSite is one advisory lock call found in the tree.
type lockSite struct {
	file string
	line int
	args []string // top-level arguments, trimmed
}

// sqlSites are the SQL queries that take a registered lock with a literal
// namespace, and the namespace each must use.
var sqlSites = map[string]Namespace{
	"internal/db/queries/org_themes.sql":         ThemeVocabulary,
	"internal/db/queries/risk_aggregations.sql":  RiskHierarchy,
	"internal/db/queries/aggregation_rules.sql":  AggregationKey,
	"internal/db/queries/tenant_llm_routing.sql": TenantLLMRouting,
}

// TestNoUnregisteredAdvisoryLocks scans the tree for advisory lock calls.
// Every call must use the two-argument form; a literal first argument must
// be a registered namespace; only this package may pass the namespace as a
// parameter. A one-argument (BIGINT) lock fails the test, so no lock can sit
// outside the registry (#2273).
func TestNoUnregisteredAdvisoryLocks(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	registered := map[int64]bool{}
	for _, e := range All() {
		registered[int64(e.Namespace)] = true
	}
	sites := scanLockSites(t, root)
	if len(sites) == 0 {
		t.Fatal("found no advisory lock calls; the scan is broken")
	}
	seenSQL := map[string]int{}
	for _, s := range sites {
		where := s.file + ":" + strconv.Itoa(s.line)
		if len(s.args) != 2 {
			t.Errorf("%s: advisory lock with %d argument(s); use the two-int form from internal/db/advisorylock", where, len(s.args))
			continue
		}
		ns, err := strconv.ParseInt(s.args[0], 10, 32)
		if err != nil {
			if filepath.Dir(s.file) != "internal/db/advisorylock" {
				t.Errorf("%s: namespace %q is not a literal; call advisorylock.Lock / LockText instead", where, s.args[0])
			}
			continue
		}
		if !registered[ns] {
			t.Errorf("%s: namespace %d is not registered in internal/db/advisorylock", where, ns)
		}
		if want, ok := sqlSites[s.file]; ok {
			seenSQL[s.file]++
			if Namespace(ns) != want {
				t.Errorf("%s: namespace %d, want %d", where, ns, want)
			}
		}
	}
	for f := range sqlSites {
		if seenSQL[f] == 0 {
			t.Errorf("%s: expected an advisory lock call, found none", f)
		}
	}
}

// TestSplitArgs pins the argument splitter the scan relies on.
func TestSplitArgs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want []string
	}{
		{"5, hashtext(sqlc.arg(tenant_id)::uuid::text));", []string{"5", "hashtext(sqlc.arg(tenant_id)::uuid::text)"}},
		{"$1)`, key", []string{"$1"}},
		{"0x142142142142)", []string{"0x142142142142"}},
		{"$1::int4, $2::int4)`", []string{"$1::int4", "$2::int4"}},
	}
	for _, c := range cases {
		got := splitArgs(c.in)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("splitArgs(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "migrations")); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repo root not found")
		}
		dir = parent
	}
}

func scanLockSites(t *testing.T, root string) []lockSite {
	t.Helper()
	var out []lockSite
	for _, top := range []string{"internal", "cmd", "pkg", "migrations", "scripts", "connectors", "deploy"} {
		base := filepath.Join(root, top)
		if _, err := os.Stat(base); err != nil {
			continue
		}
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				switch d.Name() {
				case "node_modules", ".venv", "testdata":
					return filepath.SkipDir
				}
				return nil
			}
			switch filepath.Ext(path) {
			case ".go", ".sql", ".sh", ".py":
			default:
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			// This package's own tests take raw locks on purpose.
			if filepath.Dir(rel) == filepath.Join("internal", "db", "advisorylock") && strings.HasSuffix(rel, "_test.go") {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			src := string(b)
			for _, m := range lockCall.FindAllStringIndex(src, -1) {
				out = append(out, lockSite{
					file: filepath.ToSlash(rel),
					line: strings.Count(src[:m[0]], "\n") + 1,
					args: splitArgs(src[m[1]:]),
				})
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// splitArgs returns the top-level arguments of a call whose opening paren
// has already been consumed, up to the matching close paren.
func splitArgs(s string) []string {
	var args []string
	depth, start := 0, 0
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			if depth == 0 {
				if a := strings.TrimSpace(s[start:i]); a != "" || len(args) > 0 {
					args = append(args, a)
				}
				return args
			}
			depth--
		case ',':
			if depth == 0 {
				args = append(args, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	return args
}
