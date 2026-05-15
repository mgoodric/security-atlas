// coverage-gate enforces per-package Go line-coverage floors against a
// `go test -coverprofile=` output and the slice-069 thresholds file at
// cmd/scripts/coverage-thresholds.json.
//
// Run from the repo root:
//
//	go test -coverprofile=coverage.out ./...
//	go run ./cmd/scripts/coverage-gate -profile=coverage.out
//
// Optional flags:
//
//	-profile        path to the coverage profile (default coverage.out)
//	-thresholds     path to the thresholds json (default
//	                cmd/scripts/coverage-thresholds.json)
//
// Exit codes:
//
//	0 — every covered package meets or exceeds its floor
//	1 — one or more packages fall under their floor (details to stderr)
//	2 — invocation / input error (profile not found, thresholds malformed)
//
// Design notes (slice 069 D1 + D2):
//
//   - The gate parses `go tool cover -func=<profile>` output (the
//     canonical per-statement aggregator shipped with the Go toolchain)
//     and aggregates per-package totals from it. This avoids re-implementing
//     coverage math.
//   - The gate operates on the UNIT-ONLY profile produced by the CI job
//     `Go · build + test` (the integration job collects a separate
//     profile with `-coverpkg=./...`; mixing them is a follow-up).
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
	thresholdsPath := flag.String("thresholds", "cmd/scripts/coverage-thresholds.json", "path to the thresholds json")
	flag.Parse()

	if err := run(*profilePath, *thresholdsPath); err != nil {
		fmt.Fprintln(os.Stderr, "coverage-gate: ", err)
		if exitErr, ok := err.(exitCodeErr); ok {
			os.Exit(exitErr.code)
		}
		os.Exit(2)
	}
}

type exitCodeErr struct {
	code int
	msg  string
}

func (e exitCodeErr) Error() string { return e.msg }

func run(profilePath, thresholdsPath string) error {
	if _, err := os.Stat(profilePath); err != nil {
		return exitCodeErr{2, fmt.Sprintf("profile not readable at %s: %v", profilePath, err)}
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
	pkgs, err := parseCoverageProfile(profilePath)
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

// parseCoverageProfile reads a `go test -coverprofile=` output and
// aggregates per-package statement counts.
//
// Profile format (per `go tool cover` docs):
//
//	mode: atomic
//	<file>:<startLine>.<startCol>,<endLine>.<endCol> <numStmt> <count>
//
// We sum numStmt per package and (numStmt where count > 0) per package
// for the covered fraction.
func parseCoverageProfile(path string) (map[string]pkgCoverage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	// Aggregate per package.
	type bucket struct{ covered, total int }
	buckets := map[string]*bucket{}

	scanner := bufio.NewScanner(f)
	// Coverage profiles can have very long lines on big monorepos.
	scanner.Buffer(make([]byte, 0, 1024*1024), 8*1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if lineNo == 1 && strings.HasPrefix(line, "mode:") {
			continue
		}
		if line == "" {
			continue
		}
		// Format: <file>:<start>,<end> <numStmt> <count>
		// Split from the right: last 3 whitespace-separated fields are
		// the position range, numStmt, count.
		fields := strings.Fields(line)
		if len(fields) < 3 {
			return nil, fmt.Errorf("line %d: unexpected field count: %q", lineNo, line)
		}
		count, err := strconv.Atoi(fields[len(fields)-1])
		if err != nil {
			return nil, fmt.Errorf("line %d: parse count: %v", lineNo, err)
		}
		numStmt, err := strconv.Atoi(fields[len(fields)-2])
		if err != nil {
			return nil, fmt.Errorf("line %d: parse numStmt: %v", lineNo, err)
		}
		// fields[0..len-3] joined back is `<file>:<positions>`. We need
		// just <file>, which is before the first `:`.
		head := strings.Join(fields[:len(fields)-2], " ")
		colon := strings.Index(head, ":")
		if colon < 0 {
			return nil, fmt.Errorf("line %d: no `:` separator in path: %q", lineNo, head)
		}
		fullPath := head[:colon]
		pkg := pkgFromGoFile(fullPath)
		b, ok := buckets[pkg]
		if !ok {
			b = &bucket{}
			buckets[pkg] = b
		}
		b.total += numStmt
		if count > 0 {
			b.covered += numStmt
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
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
