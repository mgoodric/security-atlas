// helpers_test.go — slice 296 coverage lift.
//
// Load-bearing functions covered by this file:
//
//   - Load (loader.go): nil-evaluator default branch, empty-FS / no-metrics
//     branch, non-yaml file skip, yaml-unmarshal failure, empty-metrics-file
//     failure.
//   - validateMetric (loader.go): every per-row branch absent from
//     loader_test.go's happy-path + sentinel-error coverage — missing-id,
//     uppercase-id (snake-case rejection), missing name/description/unit/category,
//     invalid level, invalid cadence, invalid compute_strategy,
//     empty parent_id, weight out of range.
//   - EmptyRegistry.Has (loader.go): returns false sentinel branch.
//   - MapRegistry.Has (loader.go): negative-lookup branch.
//   - NewSeeder + Apply (seed.go): constructor + nil-pool early-return branch
//     (the only seed.go path reachable without a live DB).
//   - formatWeight (seed.go): pgtype-Numeric encoding shape for the (0,1]
//     domain that the cascade-edge weight column allows.
//
// Pure-Go only — no DB. The integration_test.go suite covers the live-pool
// Apply / SeedFromEmbedded paths under `//go:build integration`.
package metrics

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"
)

// TestLoad_NilEvaluatorRegistryDefaultsToEmpty asserts that passing a nil
// EvaluatorRegistry into Load is accepted and behaves like EmptyRegistry
// (which has no evaluators, so any `computed` metric is rejected). This
// exercises the `if evaluators == nil { evaluators = EmptyRegistry{} }`
// branch at the top of Load that loader_test.go never trips.
func TestLoad_NilEvaluatorRegistryDefaultsToEmpty(t *testing.T) {
	fsys := fstest.MapFS{
		"manual.yaml": &fstest.MapFile{Data: []byte(`metrics:
  - id: only_manual
    level: board
    category: posture
    name: Only Manual
    description: A manual-input metric
    unit: percent
    cadence: daily
    compute_strategy: manual_input
`)},
	}
	c, err := Load(fsys, nil)
	if err != nil {
		t.Fatalf("Load(nil registry, manual metric): %v", err)
	}
	if got, want := len(c.Metrics), 1; got != want {
		t.Errorf("metrics len = %d, want %d", got, want)
	}

	// And the nil-registry path STILL rejects a computed metric, because the
	// fallback EmptyRegistry has no entries.
	fsysComputed := fstest.MapFS{
		"comp.yaml": &fstest.MapFile{Data: []byte(`metrics:
  - id: comp
    level: board
    category: posture
    name: Comp
    description: Computed
    unit: percent
    cadence: daily
    compute_strategy: computed
    compute_evaluator: somefn
`)},
	}
	_, err = Load(fsysComputed, nil)
	if !errors.Is(err, ErrUnknownEvaluator) {
		t.Fatalf("Load(nil registry, computed metric): want ErrUnknownEvaluator, got %v", err)
	}
}

// TestLoad_EmptyFSReturnsNoMetricsError covers the `if len(allMetrics) == 0`
// terminal-error branch — an FS with zero yaml files.
func TestLoad_EmptyFSReturnsNoMetricsError(t *testing.T) {
	fsys := fstest.MapFS{}
	_, err := Load(fsys, EmptyRegistry{})
	if err == nil {
		t.Fatal("Load(empty FS): want error, got nil")
	}
	if !strings.Contains(err.Error(), "no metrics found") {
		t.Errorf("error = %q, want substring 'no metrics found'", err.Error())
	}
}

// TestLoad_NonYamlFilesAreSkipped covers the `!strings.HasSuffix(p, ".yaml")
// && !strings.HasSuffix(p, ".yml")` skip branch — a README or other
// non-yaml entry under catalogs/metrics/ does NOT cause an error.
func TestLoad_NonYamlFilesAreSkipped(t *testing.T) {
	fsys := fstest.MapFS{
		"README.md": &fstest.MapFile{Data: []byte("# Metrics catalog\nThis directory ships the curated catalog.\n")},
		"notes.txt": &fstest.MapFile{Data: []byte("anything")},
		"sub/board.yaml": &fstest.MapFile{Data: []byte(`metrics:
  - id: m1
    level: board
    category: posture
    name: M1
    description: Metric one
    unit: percent
    cadence: daily
    compute_strategy: manual_input
`)},
	}
	c, err := Load(fsys, EmptyRegistry{})
	if err != nil {
		t.Fatalf("Load(mixed FS): %v", err)
	}
	if got, want := len(c.Metrics), 1; got != want {
		t.Errorf("metrics len = %d, want %d (non-yaml entries should be skipped)", got, want)
	}
}

// TestLoad_RejectsMalformedYaml covers the `yaml.Unmarshal` error branch.
// The file shape "metrics: not-a-list" mis-types the Metrics field.
func TestLoad_RejectsMalformedYaml(t *testing.T) {
	fsys := fstest.MapFS{
		"bad.yaml": &fstest.MapFile{Data: []byte("metrics: not-a-list\n")},
	}
	_, err := Load(fsys, EmptyRegistry{})
	if err == nil {
		t.Fatal("Load(bad yaml): want error, got nil")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Errorf("error = %q, want substring 'parse'", err.Error())
	}
}

// TestLoad_RejectsEmptyMetricsFile covers the `len(f.Metrics) == 0`
// per-file branch — a yaml file that parses but contains no metrics.
func TestLoad_RejectsEmptyMetricsFile(t *testing.T) {
	fsys := fstest.MapFS{
		"empty.yaml": &fstest.MapFile{Data: []byte("metrics: []\n")},
	}
	_, err := Load(fsys, EmptyRegistry{})
	if err == nil {
		t.Fatal("Load(empty metrics file): want error, got nil")
	}
	if !strings.Contains(err.Error(), "zero metrics") {
		t.Errorf("error = %q, want substring 'zero metrics'", err.Error())
	}
}

// TestValidateMetric_PerFieldRejections drives every per-field validation
// rejection branch from a single table. Each row builds a minimal, otherwise-
// valid Metric and mutates a single field so the only failure is the field
// under test. Hits the missing-id / non-snake-case-id / missing-name /
// missing-description / missing-unit / missing-category / invalid-level /
// invalid-cadence / invalid-compute-strategy / empty-parent-id /
// weight-out-of-range branches in validateMetric.
//
// Drives the branches via Load (the only public entry point that calls
// validateMetric); the loader_test.go file uses the same pattern.
func TestValidateMetric_PerFieldRejections(t *testing.T) {
	// baseline is a valid manual_input metric. Each test case substitutes
	// the named field into the YAML template.
	type tc struct {
		name      string
		yaml      string
		wantSub   string
		wantErrIs error // optional sentinel-error check
	}
	cases := []tc{
		{
			name: "MissingID",
			yaml: `metrics:
  - level: board
    category: posture
    name: NoID
    description: No id
    unit: percent
    cadence: daily
    compute_strategy: manual_input
`,
			wantSub: "metric missing id",
		},
		{
			name: "NonSnakeCaseID",
			yaml: `metrics:
  - id: "ABC"
    level: board
    category: posture
    name: UpperOnly
    description: ID has no snake_case characters
    unit: percent
    cadence: daily
    compute_strategy: manual_input
`,
			wantSub: "must be a snake_case slug",
		},
		{
			name: "MissingName",
			yaml: `metrics:
  - id: no_name
    level: board
    category: posture
    description: No name
    unit: percent
    cadence: daily
    compute_strategy: manual_input
`,
			wantSub: "name is required",
		},
		{
			name: "MissingDescription",
			yaml: `metrics:
  - id: no_desc
    level: board
    category: posture
    name: Foo
    unit: percent
    cadence: daily
    compute_strategy: manual_input
`,
			wantSub: "description is required",
		},
		{
			name: "MissingUnit",
			yaml: `metrics:
  - id: no_unit
    level: board
    category: posture
    name: Foo
    description: Foo desc
    cadence: daily
    compute_strategy: manual_input
`,
			wantSub: "unit is required",
		},
		{
			name: "MissingCategory",
			yaml: `metrics:
  - id: no_category
    level: board
    name: Foo
    description: Foo desc
    unit: percent
    cadence: daily
    compute_strategy: manual_input
`,
			wantSub: "category is required",
		},
		{
			name: "InvalidLevel",
			yaml: `metrics:
  - id: bad_level
    level: bogus
    category: posture
    name: Foo
    description: Foo desc
    unit: percent
    cadence: daily
    compute_strategy: manual_input
`,
			wantSub: "invalid level",
		},
		{
			name: "InvalidCadence",
			yaml: `metrics:
  - id: bad_cadence
    level: board
    category: posture
    name: Foo
    description: Foo desc
    unit: percent
    cadence: hourly
    compute_strategy: manual_input
`,
			wantSub: "invalid cadence",
		},
		{
			name: "InvalidComputeStrategy",
			yaml: `metrics:
  - id: bad_strategy
    level: board
    category: posture
    name: Foo
    description: Foo desc
    unit: percent
    cadence: daily
    compute_strategy: telepathy
`,
			wantSub: "invalid compute_strategy",
		},
		{
			name: "EmptyParentID",
			yaml: `metrics:
  - id: child_only
    level: program
    category: posture
    name: Child
    description: Child desc
    unit: percent
    cadence: daily
    compute_strategy: manual_input
    parents:
      - parent_id: ""
`,
			wantSub: "empty parent_id",
		},
		{
			name: "WeightOutOfRange",
			yaml: `metrics:
  - id: parent_a
    level: board
    category: posture
    name: Parent
    description: Parent
    unit: percent
    cadence: daily
    compute_strategy: manual_input
  - id: child_a
    level: program
    category: posture
    name: Child
    description: Child
    unit: percent
    cadence: daily
    compute_strategy: manual_input
    parents:
      - parent_id: parent_a
        weight: 2.5
`,
			wantSub: "out of (0,1]",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fsys := fstest.MapFS{
				"case.yaml": &fstest.MapFile{Data: []byte(c.yaml)},
			}
			_, err := Load(fsys, EmptyRegistry{})
			if err == nil {
				t.Fatalf("Load(%s): want error, got nil", c.name)
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Errorf("Load(%s): err = %q, want substring %q", c.name, err.Error(), c.wantSub)
			}
			if c.wantErrIs != nil && !errors.Is(err, c.wantErrIs) {
				t.Errorf("Load(%s): err = %v, want errors.Is = %v", c.name, err, c.wantErrIs)
			}
		})
	}
}

// TestValidateMetric_NegativeWeightIsRejected pairs with the previous table's
// upper-bound case by hitting the lower-bound branch (`p.Weight < 0`). Same
// validateMetric branch, opposite extreme — keeping the assertion explicit
// rather than burying it inside a sub-row.
func TestValidateMetric_NegativeWeightIsRejected(t *testing.T) {
	fsys := fstest.MapFS{
		"negative.yaml": &fstest.MapFile{Data: []byte(`metrics:
  - id: parent_b
    level: board
    category: posture
    name: Parent
    description: Parent
    unit: percent
    cadence: daily
    compute_strategy: manual_input
  - id: child_b
    level: program
    category: posture
    name: Child
    description: Child
    unit: percent
    cadence: daily
    compute_strategy: manual_input
    parents:
      - parent_id: parent_b
        weight: -0.5
`)},
	}
	_, err := Load(fsys, EmptyRegistry{})
	if err == nil {
		t.Fatal("Load(negative weight): want error, got nil")
	}
	if !strings.Contains(err.Error(), "out of (0,1]") {
		t.Errorf("err = %q, want substring 'out of (0,1]'", err.Error())
	}
}

// TestRegistry_HasSemantics exercises the EmptyRegistry.Has (always-false)
// and MapRegistry.Has (positive + negative) branches. These three methods
// are the public surface every `compute_strategy: computed` validation
// path hits — covering them explicitly removes the only seam in loader.go
// that the YAML-driven Load tests above can't reach by themselves
// (EmptyRegistry.Has is called from the nil-registry default branch but
// `go test`'s coverage sees a synthetic Has call as a distinct branch).
func TestRegistry_HasSemantics(t *testing.T) {
	var empty EmptyRegistry
	if empty.Has("anything") {
		t.Error("EmptyRegistry.Has = true, want false")
	}
	if empty.Has("") {
		t.Error("EmptyRegistry.Has(empty) = true, want false")
	}

	m := MapRegistry{"alpha": {}, "beta": {}}
	if !m.Has("alpha") {
		t.Error("MapRegistry.Has(alpha) = false, want true")
	}
	if m.Has("gamma") {
		t.Error("MapRegistry.Has(gamma) = true, want false")
	}
}

// TestNewSeeder_ConstructsSeederWithPool covers the constructor branch.
// We use nil for the pool — NewSeeder doesn't dereference it, and the only
// observable behavior is that Apply on a Seeder with a nil pool returns
// the documented "nil pool" error (TestApply_NilPoolReturnsError below).
func TestNewSeeder_ConstructsSeederWithPool(t *testing.T) {
	s := NewSeeder(nil)
	if s == nil {
		t.Fatal("NewSeeder returned nil")
	}
	// The struct is opaque; assert via the documented Apply behavior in the
	// next test rather than touching internal fields. The constructor's
	// coverage block is the assignment itself.
}

// TestApply_NilPoolReturnsError covers the early-return branch in Apply
// (`if s.pool == nil { ... }`). All other branches in Apply require a live
// pgxpool — covered by integration_test.go under `//go:build integration`.
func TestApply_NilPoolReturnsError(t *testing.T) {
	s := NewSeeder(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := s.Apply(ctx, &Catalog{})
	if err == nil {
		t.Fatal("Apply(nil pool): want error, got nil")
	}
	if !strings.Contains(err.Error(), "nil pool") {
		t.Errorf("err = %q, want substring 'nil pool'", err.Error())
	}
}

// TestFormatWeight_FixedFourDecimalDigits documents the cascade-edge weight
// encoding contract: formatWeight returns a fixed-four-fractional-digit
// decimal string that pgtype.Numeric.Scan accepts. The DB column is
// Numeric(5,4) — the loader-side validation enforces (0,1] and the
// formatter ALSO emits 0 with .0000 padding for any edge that arrives
// with a default-zero weight (a path Load substitutes for an unset
// `weight:` field by re-defaulting to 1.0, so the zero case here is
// defensive coverage of the formatter itself).
func TestFormatWeight_FixedFourDecimalDigits(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{1.0, "1.0000"},
		{0.5, "0.5000"},
		{0.0001, "0.0001"},
		{0.12345, "0.1235"}, // four-fractional-digit rounding
		{0, "0.0000"},
	}
	for _, c := range cases {
		got := formatWeight(c.in)
		if got != c.want {
			t.Errorf("formatWeight(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
