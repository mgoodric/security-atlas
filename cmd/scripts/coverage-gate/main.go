// coverage-gate enforces per-package Go line-coverage floors against a
// `go test -coverprofile=` output and the slice-069 thresholds file at
// cmd/scripts/coverage-thresholds.json.
//
// Run from the repo root with a SINGLE profile (slice 069 shape):
//
//	go test -coverprofile=coverage.out ./...
//	go run ./cmd/scripts/coverage-gate -profile=coverage.out
//
// Run from the repo root with MERGED unit + integration profiles
// (slice 279 — recommended for full repo audit):
//
//	go test -coverpkg=./... -coverprofile=unit.cov ./...
//	go test -tags=integration -p 1 -coverpkg=./... \
//	  -coverprofile=integration.cov <ci-integration-pkg-list>
//	go run ./cmd/scripts/coverage-gate \
//	  -profile=unit.cov -extra-profile=integration.cov
//
// Optional flags:
//
//	-profile         path to the primary coverage profile (default coverage.out)
//	-extra-profile   optional extra profile merged into -profile in-memory
//	                 (slice 279 — used to add integration coverage to a
//	                 unit-only gate run). Repeatable via comma-separated list.
//	-thresholds      path to the thresholds json (default
//	                 cmd/scripts/coverage-thresholds.json)
//
// Exit codes:
//
//	0 — every covered package meets or exceeds its floor
//	1 — one or more packages fall under their floor (details to stderr)
//	2 — invocation / input error (profile not found, thresholds malformed)
//
// Design notes (slice 069 D1 + D2; slice 279 extension):
//
//   - The gate aggregates per-package statement counts directly from
//     the raw `-coverprofile=` output (10-line format spec at
//     https://pkg.go.dev/golang.org/x/tools/cmd/cover). It does NOT
//     shell out to `go tool cover -func=`.
//   - The gate accepts EITHER a single profile (slice 069 mode) OR a
//     primary + extra profile that are merged in-memory before
//     threshold check (slice 279 mode). The in-memory merge follows
//     the same semantics as `gocovmerge`: union the line specs; for
//     a line present in both, take the maximum count. We use a single
//     binary so CI doesn't need a separate `gocovmerge` install step
//     for the threshold check itself.
//   - All profiles MUST use `set` (default) covermode. Mixing `set`
//     with `atomic` or `count` is rejected — the math is incompatible.
//   - Packages listed in `excludes[]` are skipped (sqlc-generated,
//     protoc-generated, integration-only). A package with NO coverage
//     data (e.g. an integration-only package the unit run never touches)
//     is treated as excluded — not a failure.
//   - A package present in `thresholds` with measured coverage below its
//     floor is the failure case.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// thresholdsFile mirrors cmd/scripts/coverage-thresholds.json.
type thresholdsFile struct {
	// Allow arbitrary $-prefixed comment fields without complaint.
	Thresholds map[string]float64 `json:"thresholds"`
	Excludes   []string           `json:"excludes"`
}

// pkgCoverage is the aggregated result for one package.
type pkgCoverage struct {
	pkg         string
	covered     int
	total       int
	coveragePct float64
}

func main() {
	profilePath := flag.String("profile", "coverage.out", "path to the coverage profile produced by `go test -coverprofile=`")
	extraProfile := flag.String("extra-profile", "", "optional extra profile(s) merged into -profile in-memory; comma-separated for multiple")
	thresholdsPath := flag.String("thresholds", "cmd/scripts/coverage-thresholds.json", "path to the thresholds json")
	flag.Parse()

	extras := splitNonEmpty(*extraProfile, ",")
	if err := run(*profilePath, extras, *thresholdsPath); err != nil {
		fmt.Fprintln(os.Stderr, "coverage-gate: ", err)
		if exitErr, ok := err.(exitCodeErr); ok {
			os.Exit(exitErr.code)
		}
		os.Exit(2)
	}
}

// splitNonEmpty is a strings.Split that drops empty fields. Avoids the
// `strings.Split("", ",")` → `[""]` corner that would attempt to open
// a profile at path "".
func splitNonEmpty(s, sep string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, sep)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

type exitCodeErr struct {
	code int
	msg  string
}

func (e exitCodeErr) Error() string { return e.msg }

func run(profilePath string, extraProfiles []string, thresholdsPath string) error {
	if _, err := os.Stat(profilePath); err != nil {
		return exitCodeErr{2, fmt.Sprintf("profile not readable at %s: %v", profilePath, err)}
	}
	for _, ep := range extraProfiles {
		if _, err := os.Stat(ep); err != nil {
			return exitCodeErr{2, fmt.Sprintf("extra-profile not readable at %s: %v", ep, err)}
		}
	}

	// Load thresholds + excludes.
	tBytes, err := os.ReadFile(thresholdsPath)
	if err != nil {
		return exitCodeErr{2, fmt.Sprintf("thresholds not readable at %s: %v", thresholdsPath, err)}
	}
	var t thresholdsFile
	if err := json.Unmarshal(tBytes, &t); err != nil {
		return exitCodeErr{2, fmt.Sprintf("thresholds malformed: %v", err)}
	}
	if len(t.Thresholds) == 0 {
		return exitCodeErr{2, "thresholds.thresholds is empty"}
	}

	// Parse the raw coverage profile directly. We aggregate
	// per-package by summing `numStmt` and (numStmt where count > 0) per
	// file, then collapsing to package paths. We do NOT shell out to
	// `go tool cover -func=` — the -func output is per-FUNCTION
	// percentages, which we'd need to weight back to statements anyway,
	// and the raw profile format is small (10-line spec, see
	// https://pkg.go.dev/golang.org/x/tools/cmd/cover).
	//
	// Slice 279: if extra profiles are provided, merge their per-line
	// counts in-memory before aggregating per-package totals. The merge
	// follows `gocovmerge` semantics for `set` mode: union the line
	// specs, and a line is covered if covered in ANY profile.
	pkgs, err := parseAndMergeProfiles(profilePath, extraProfiles)
	if err != nil {
		return exitCodeErr{2, fmt.Sprintf("parsing coverage profile: %v", err)}
	}

	// Build the exclude prefix list. A package is excluded if its path
	// starts with ANY exclude prefix.
	excludePrefixes := make([]string, 0, len(t.Excludes))
	for _, e := range t.Excludes {
		excludePrefixes = append(excludePrefixes, strings.TrimSuffix(e, "/"))
	}

	isExcluded := func(pkg string) bool {
		for _, p := range excludePrefixes {
			if pkg == p || strings.HasPrefix(pkg, p+"/") {
				return true
			}
		}
		return false
	}

	type failure struct {
		pkg   string
		got   float64
		floor float64
	}
	type warning struct {
		pkg string
		got float64
	}

	var (
		failures   []failure
		warnings   []warning
		checked    int
		skippedNoF int
	)

	// For each threshold entry, look up the matching aggregated coverage.
	keys := make([]string, 0, len(t.Thresholds))
	for k := range t.Thresholds {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		floor := t.Thresholds[key]
		if isExcluded(key) {
			continue
		}
		cov, ok := pkgs[key]
		if !ok {
			// Threshold for a package the profile never touched. Either
			// the package has no Go test files at all (uncovered) or the
			// CI test target didn't include it. Surface as a warning so
			// drift between thresholds.json and the codebase is visible,
			// but do NOT fail the gate — the slice's policy is "ratchet
			// what exists, never demand tests this PR doesn't write".
			warnings = append(warnings, warning{pkg: key, got: -1})
			skippedNoF++
			continue
		}
		checked++
		if cov.coveragePct+1e-9 < floor {
			failures = append(failures, failure{pkg: key, got: cov.coveragePct, floor: floor})
		}
	}

	// Report.
	fmt.Printf("coverage-gate: checked %d packages, %d failed, %d warnings (no profile data)\n",
		checked, len(failures), skippedNoF)

	if len(warnings) > 0 {
		fmt.Fprintln(os.Stderr, "\ncoverage-gate WARNINGS (threshold present, no profile data):")
		for _, w := range warnings {
			fmt.Fprintf(os.Stderr, "  %s\n", w.pkg)
		}
	}

	if len(failures) > 0 {
		fmt.Fprintln(os.Stderr, "\ncoverage-gate FAILURES:")
		for _, f := range failures {
			fmt.Fprintf(os.Stderr, "  %s: got %.1f%% < floor %.1f%%\n", f.pkg, f.got, f.floor)
		}
		return exitCodeErr{1, fmt.Sprintf("%d package(s) below floor", len(failures))}
	}

	fmt.Println("ALL CHECKS PASS")
	return nil
}

// lineKey uniquely identifies one coverage block (one line in the
// raw profile). We use the full `<file>:<positions>` head string
// (the part before numStmt + count) because that's what gocovmerge
// uses for dedupe.
type lineKey string

// lineEntry captures the per-block numStmt and the merged count
// (max over all profiles for `set` mode — a block is "hit" if hit in
// any profile).
type lineEntry struct {
	numStmt int
	count   int
	pkg     string
}

// parseAndMergeProfiles loads the primary profile + any extra profiles
// and returns per-package aggregated coverage. When extras are empty,
// this is equivalent to parseCoverageProfile.
//
// The merge semantics match gocovmerge for `set` mode: union all line
// blocks; a block is "covered" if any profile has count > 0.
//
// The numStmt field must match for the same line key across profiles
// (the cover tool emits a stable numStmt for the same source block) —
// if it differs, we keep the larger value and continue, treating it as
// a profile-skew warning rather than a hard error. This mirrors what
// gocovmerge does in practice when the same package is built with
// slightly different `-coverpkg` flags across runs.
func parseAndMergeProfiles(primary string, extras []string) (map[string]pkgCoverage, error) {
	merged := map[lineKey]*lineEntry{}
	mode := ""

	addProfile := func(path string) error {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()

		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 1024*1024), 8*1024*1024)
		lineNo := 0
		for scanner.Scan() {
			lineNo++
			line := scanner.Text()
			if lineNo == 1 && strings.HasPrefix(line, "mode:") {
				m := strings.TrimSpace(strings.TrimPrefix(line, "mode:"))
				if mode == "" {
					mode = m
				} else if mode != m {
					return fmt.Errorf("profile %s: mode %q does not match prior %q (re-run all profiles with the same -covermode)", path, m, mode)
				}
				continue
			}
			if line == "" {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 3 {
				return fmt.Errorf("%s line %d: unexpected field count: %q", path, lineNo, line)
			}
			count, err := strconv.Atoi(fields[len(fields)-1])
			if err != nil {
				return fmt.Errorf("%s line %d: parse count: %v", path, lineNo, err)
			}
			numStmt, err := strconv.Atoi(fields[len(fields)-2])
			if err != nil {
				return fmt.Errorf("%s line %d: parse numStmt: %v", path, lineNo, err)
			}
			head := strings.Join(fields[:len(fields)-2], " ")
			colon := strings.Index(head, ":")
			if colon < 0 {
				return fmt.Errorf("%s line %d: no `:` separator in path: %q", path, lineNo, head)
			}
			fullPath := head[:colon]
			pkg := pkgFromGoFile(fullPath)
			key := lineKey(head)
			existing, ok := merged[key]
			if !ok {
				merged[key] = &lineEntry{numStmt: numStmt, count: count, pkg: pkg}
				continue
			}
			// Keep larger numStmt if they diverge (rare; profile skew).
			if numStmt > existing.numStmt {
				existing.numStmt = numStmt
			}
			// `set` mode merge: a line is covered if any profile hit it.
			if count > 0 {
				existing.count = 1
			}
		}
		return scanner.Err()
	}

	if err := addProfile(primary); err != nil {
		return nil, err
	}
	for _, ep := range extras {
		if err := addProfile(ep); err != nil {
			return nil, err
		}
	}

	type bucket struct{ covered, total int }
	buckets := map[string]*bucket{}
	for _, e := range merged {
		b, ok := buckets[e.pkg]
		if !ok {
			b = &bucket{}
			buckets[e.pkg] = b
		}
		b.total += e.numStmt
		if e.count > 0 {
			b.covered += e.numStmt
		}
	}

	out := make(map[string]pkgCoverage, len(buckets))
	for k, v := range buckets {
		var pct float64
		if v.total > 0 {
			pct = 100.0 * float64(v.covered) / float64(v.total)
		}
		out[k] = pkgCoverage{pkg: k, covered: v.covered, total: v.total, coveragePct: pct}
	}
	return out, nil
}

// pkgFromGoFile strips the module prefix and the trailing .go filename
// to yield a package path like `internal/api/tenancymw`.
//
// Input examples:
//
//	github.com/mgoodric/security-atlas/internal/api/tenancymw/middleware.go
//	github.com/mgoodric/security-atlas/cmd/atlas/main.go
//
// Outputs:
//
//	internal/api/tenancymw
//	cmd/atlas
const modulePrefix = "github.com/mgoodric/security-atlas/"

func pkgFromGoFile(p string) string {
	p = strings.TrimPrefix(p, modulePrefix)
	return filepath.ToSlash(filepath.Dir(p))
}
