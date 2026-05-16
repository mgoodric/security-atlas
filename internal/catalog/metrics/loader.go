// Package metrics loads the curated platform metrics catalog from
// `catalogs/metrics/*.yaml` and seeds it into the singleton-tenant-agnostic
// metrics_catalog + metric_cascade_edges tables (slice 076).
//
// The loader is the source-of-truth gate for the catalog: it parses every
// YAML file under catalogs/metrics/, validates the per-metric shape,
// verifies that every cascade parent reference resolves, detects cycles
// via app-layer topological sort, and confirms every `compute_strategy:
// computed` metric names a registered Go evaluator function. Failure at
// any of these checkpoints aborts the seeder loudly at boot — a content
// bug is detected before runtime queries can read malformed cascade rows.
package metrics

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Level is one of "board", "program", "team". Matches the DB CHECK.
type Level string

const (
	LevelBoard   Level = "board"
	LevelProgram Level = "program"
	LevelTeam    Level = "team"
)

// Cadence is the user-facing cadence string. Matches the DB CHECK.
type Cadence string

const (
	CadenceRealtime  Cadence = "realtime"
	CadenceDaily     Cadence = "daily"
	CadenceWeekly    Cadence = "weekly"
	CadenceMonthly   Cadence = "monthly"
	CadenceQuarterly Cadence = "quarterly"
)

// ComputeStrategy describes how the platform produces a numeric_value
// for the metric. Matches the DB CHECK.
type ComputeStrategy string

const (
	StrategyComputed            ComputeStrategy = "computed"
	StrategyManualInput         ComputeStrategy = "manual_input"
	StrategyExternalIntegration ComputeStrategy = "external_integration"
)

// ParentEdge is one cascade edge as authored in YAML.
type ParentEdge struct {
	ParentID string  `yaml:"parent_id"`
	Weight   float64 `yaml:"weight,omitempty"`
	Notes    string  `yaml:"notes,omitempty"`
}

// Metric is one catalog row as authored in YAML. `Parents` is empty for
// top-level (board) metrics; populated for descendants.
type Metric struct {
	ID               string          `yaml:"id"`
	Level            Level           `yaml:"level"`
	Category         string          `yaml:"category"`
	Name             string          `yaml:"name"`
	Description      string          `yaml:"description"`
	Unit             string          `yaml:"unit"`
	Cadence          Cadence         `yaml:"cadence"`
	ComputeStrategy  ComputeStrategy `yaml:"compute_strategy"`
	ComputeEvaluator string          `yaml:"compute_evaluator,omitempty"`
	SourceSlices     []string        `yaml:"source_slices,omitempty"`
	Notes            string          `yaml:"notes,omitempty"`
	Parents          []ParentEdge    `yaml:"parents,omitempty"`
}

// fileShape is the YAML root.
type fileShape struct {
	Metrics []Metric `yaml:"metrics"`
}

// Catalog is the parsed, validated catalog. Metrics are sorted by id for
// determinism so re-running the loader against the same files produces
// identical Catalog instances (idempotency precondition).
type Catalog struct {
	Metrics []Metric
	// Edges is the flat parent → child edge list extracted from each
	// metric's Parents block. Sorted by (parent_id, child_id) for
	// determinism.
	Edges []Edge
}

// Edge is one parent → child cascade edge.
type Edge struct {
	ParentID string
	ChildID  string
	Weight   float64
	Notes    string
}

// Load reads every *.yaml file under root (recursively), parses, and
// validates. Returns ErrCycle when a cycle is detected. Returns
// ErrUnknownParent when a child references a parent that doesn't exist.
// Returns ErrUnknownEvaluator when a computed metric names an evaluator
// not in the supplied registry.
func Load(root fs.FS, evaluators EvaluatorRegistry) (*Catalog, error) {
	if evaluators == nil {
		evaluators = EmptyRegistry{}
	}
	var allMetrics []Metric
	err := fs.WalkDir(root, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(p, ".yaml") && !strings.HasSuffix(p, ".yml") {
			return nil
		}
		body, err := fs.ReadFile(root, p)
		if err != nil {
			return fmt.Errorf("catalog/metrics: read %s: %w", p, err)
		}
		var f fileShape
		if err := yaml.Unmarshal(body, &f); err != nil {
			return fmt.Errorf("catalog/metrics: parse %s: %w", p, err)
		}
		if len(f.Metrics) == 0 {
			return fmt.Errorf("catalog/metrics: %s has zero metrics", p)
		}
		for i := range f.Metrics {
			// Attach the source filename for error reporting; not
			// persisted into the DB.
			if err := validateMetric(&f.Metrics[i], path.Base(p), evaluators); err != nil {
				return err
			}
			allMetrics = append(allMetrics, f.Metrics[i])
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(allMetrics) == 0 {
		return nil, errors.New("catalog/metrics: no metrics found under catalogs/metrics/")
	}

	// Deduplicate by id (a duplicate id across files is a content bug).
	seen := make(map[string]string, len(allMetrics))
	for _, m := range allMetrics {
		if prior, ok := seen[m.ID]; ok {
			return nil, fmt.Errorf("catalog/metrics: duplicate metric id %q (previous occurrence in %s)", m.ID, prior)
		}
		seen[m.ID] = m.ID
	}

	// Resolve cascade edges + verify every parent reference exists.
	idIndex := make(map[string]struct{}, len(allMetrics))
	for _, m := range allMetrics {
		idIndex[m.ID] = struct{}{}
	}
	var edges []Edge
	for _, m := range allMetrics {
		for _, p := range m.Parents {
			if _, ok := idIndex[p.ParentID]; !ok {
				return nil, fmt.Errorf("catalog/metrics: metric %q references unknown parent %q: %w", m.ID, p.ParentID, ErrUnknownParent)
			}
			w := p.Weight
			if w == 0 {
				w = 1.0
			}
			edges = append(edges, Edge{
				ParentID: p.ParentID,
				ChildID:  m.ID,
				Weight:   w,
				Notes:    p.Notes,
			})
		}
	}

	// Sort outputs for determinism.
	sort.Slice(allMetrics, func(i, j int) bool { return allMetrics[i].ID < allMetrics[j].ID })
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].ParentID != edges[j].ParentID {
			return edges[i].ParentID < edges[j].ParentID
		}
		return edges[i].ChildID < edges[j].ChildID
	})

	// Cycle detection: a topological sort that walks parent → child.
	// If the sort can't drain (some node always has an incoming edge from
	// the unvisited set), the remaining nodes form a cycle.
	if err := detectCycles(allMetrics, edges); err != nil {
		return nil, err
	}

	return &Catalog{Metrics: allMetrics, Edges: edges}, nil
}

// validateMetric enforces the per-row contract: required fields present,
// enum-valued fields within their vocabulary, the computed-iff-evaluator
// constraint, and the cycle-free self-loop check (a redundancy with the
// DB CHECK that lets the loader report a richer error).
func validateMetric(m *Metric, source string, evaluators EvaluatorRegistry) error {
	if m.ID == "" {
		return fmt.Errorf("catalog/metrics: %s: metric missing id", source)
	}
	if !strings.ContainsAny(m.ID, "abcdefghijklmnopqrstuvwxyz0123456789_") {
		return fmt.Errorf("catalog/metrics: %s: metric id %q must be a snake_case slug", source, m.ID)
	}
	if m.Name == "" {
		return fmt.Errorf("catalog/metrics: %s (%s): name is required", source, m.ID)
	}
	if m.Description == "" {
		return fmt.Errorf("catalog/metrics: %s (%s): description is required", source, m.ID)
	}
	if m.Unit == "" {
		return fmt.Errorf("catalog/metrics: %s (%s): unit is required", source, m.ID)
	}
	if m.Category == "" {
		return fmt.Errorf("catalog/metrics: %s (%s): category is required", source, m.ID)
	}
	switch m.Level {
	case LevelBoard, LevelProgram, LevelTeam:
	default:
		return fmt.Errorf("catalog/metrics: %s (%s): invalid level %q", source, m.ID, m.Level)
	}
	switch m.Cadence {
	case CadenceRealtime, CadenceDaily, CadenceWeekly, CadenceMonthly, CadenceQuarterly:
	default:
		return fmt.Errorf("catalog/metrics: %s (%s): invalid cadence %q", source, m.ID, m.Cadence)
	}
	switch m.ComputeStrategy {
	case StrategyComputed:
		if m.ComputeEvaluator == "" {
			return fmt.Errorf("catalog/metrics: %s (%s): compute_strategy=computed requires compute_evaluator", source, m.ID)
		}
		if !evaluators.Has(m.ComputeEvaluator) {
			return fmt.Errorf("catalog/metrics: %s (%s): compute_evaluator %q not registered: %w", source, m.ID, m.ComputeEvaluator, ErrUnknownEvaluator)
		}
	case StrategyManualInput, StrategyExternalIntegration:
		if m.ComputeEvaluator != "" {
			return fmt.Errorf("catalog/metrics: %s (%s): compute_evaluator must be empty when compute_strategy is %q", source, m.ID, m.ComputeStrategy)
		}
	default:
		return fmt.Errorf("catalog/metrics: %s (%s): invalid compute_strategy %q", source, m.ID, m.ComputeStrategy)
	}
	for _, p := range m.Parents {
		if p.ParentID == "" {
			return fmt.Errorf("catalog/metrics: %s (%s): empty parent_id in parents block", source, m.ID)
		}
		if p.ParentID == m.ID {
			return fmt.Errorf("catalog/metrics: %s (%s): self-parent edge rejected: %w", source, m.ID, ErrSelfLoop)
		}
		if p.Weight < 0 || p.Weight > 1 {
			return fmt.Errorf("catalog/metrics: %s (%s): parent %q weight %v out of (0,1]", source, m.ID, p.ParentID, p.Weight)
		}
	}
	return nil
}

// detectCycles runs an iterative DFS from every node, tracking the
// recursion stack. A revisit of a node already on the stack is a cycle.
// Returns ErrCycle wrapping a human-readable parent → child path.
func detectCycles(metrics []Metric, edges []Edge) error {
	children := make(map[string][]string, len(metrics))
	for _, e := range edges {
		children[e.ParentID] = append(children[e.ParentID], e.ChildID)
	}
	// Sort children lists for deterministic error messages on cycle.
	for k := range children {
		sort.Strings(children[k])
	}

	state := make(map[string]int8, len(metrics)) // 0=unvisited, 1=onStack, 2=done

	var visit func(id string, stack []string) error
	visit = func(id string, stack []string) error {
		switch state[id] {
		case 1:
			// Found a back-edge into the current stack: cycle.
			idx := -1
			for i, s := range stack {
				if s == id {
					idx = i
					break
				}
			}
			cycle := append([]string(nil), stack[idx:]...)
			cycle = append(cycle, id)
			return fmt.Errorf("catalog/metrics: %w: %s", ErrCycle, strings.Join(cycle, " -> "))
		case 2:
			return nil
		}
		state[id] = 1
		stack = append(stack, id)
		for _, c := range children[id] {
			if err := visit(c, stack); err != nil {
				return err
			}
		}
		state[id] = 2
		return nil
	}

	for _, m := range metrics {
		if state[m.ID] == 0 {
			if err := visit(m.ID, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// ----- error sentinels -----

var (
	// ErrUnknownParent is returned when a metric's parents block names a
	// parent_id that doesn't appear elsewhere in the catalog.
	ErrUnknownParent = errors.New("unknown parent")
	// ErrUnknownEvaluator is returned when a `computed` metric names a
	// compute_evaluator that the supplied EvaluatorRegistry doesn't know.
	ErrUnknownEvaluator = errors.New("unknown evaluator")
	// ErrCycle is returned when a cascade-edge cycle is detected.
	ErrCycle = errors.New("cycle detected")
	// ErrSelfLoop is returned when a metric names itself as its own
	// parent. The DB CHECK also blocks this case; the loader rejects it
	// with a richer error.
	ErrSelfLoop = errors.New("self-loop")
)

// ----- evaluator registry interface -----

// EvaluatorRegistry is the minimum surface Load needs to verify every
// `computed` metric has a registered evaluator. The real registry lives
// in `internal/metrics/eval/`; tests can pass an in-memory map.
type EvaluatorRegistry interface {
	Has(name string) bool
}

// EmptyRegistry passes no evaluator names. Useful for syntactic tests
// where the test fixture has zero `computed` metrics.
type EmptyRegistry struct{}

// Has always returns false.
func (EmptyRegistry) Has(string) bool { return false }

// MapRegistry is a simple set-of-names registry, useful in tests.
type MapRegistry map[string]struct{}

// Has reports whether name is in the map.
func (m MapRegistry) Has(name string) bool {
	_, ok := m[name]
	return ok
}
