package metrics_test

import (
	"errors"
	"testing"
	"testing/fstest"

	cat "github.com/mgoodric/security-atlas/internal/catalog/metrics"
)

func TestLoad_HappyPath(t *testing.T) {
	fsys := fstest.MapFS{
		"board.yaml": &fstest.MapFile{Data: []byte(`metrics:
  - id: parent_metric
    level: board
    category: posture
    name: Parent
    description: A parent
    unit: percent
    cadence: daily
    compute_strategy: computed
    compute_evaluator: parent_evaluator
  - id: child_metric
    level: program
    category: posture
    name: Child
    description: A child
    unit: percent
    cadence: weekly
    compute_strategy: manual_input
    parents:
      - parent_id: parent_metric
        weight: 0.5
`)},
	}
	c, err := cat.Load(fsys, cat.MapRegistry{"parent_evaluator": {}})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := len(c.Metrics), 2; got != want {
		t.Errorf("metrics len = %d, want %d", got, want)
	}
	if got, want := len(c.Edges), 1; got != want {
		t.Fatalf("edges len = %d, want %d", got, want)
	}
	if got, want := c.Edges[0].ParentID, "parent_metric"; got != want {
		t.Errorf("edges[0].ParentID = %q, want %q", got, want)
	}
	if got, want := c.Edges[0].ChildID, "child_metric"; got != want {
		t.Errorf("edges[0].ChildID = %q, want %q", got, want)
	}
	if got, want := c.Edges[0].Weight, 0.5; got != want {
		t.Errorf("edges[0].Weight = %v, want %v", got, want)
	}
}

func TestLoad_RejectsCycle(t *testing.T) {
	fsys := fstest.MapFS{
		"cycle.yaml": &fstest.MapFile{Data: []byte(`metrics:
  - id: a
    level: board
    category: posture
    name: A
    description: A
    unit: percent
    cadence: daily
    compute_strategy: manual_input
    parents:
      - parent_id: b
  - id: b
    level: program
    category: posture
    name: B
    description: B
    unit: percent
    cadence: daily
    compute_strategy: manual_input
    parents:
      - parent_id: a
`)},
	}
	_, err := cat.Load(fsys, cat.MapRegistry{})
	if !errors.Is(err, cat.ErrCycle) {
		t.Fatalf("Load: want ErrCycle, got %v", err)
	}
}

func TestLoad_RejectsSelfLoop(t *testing.T) {
	fsys := fstest.MapFS{
		"selfloop.yaml": &fstest.MapFile{Data: []byte(`metrics:
  - id: x
    level: board
    category: posture
    name: X
    description: X
    unit: percent
    cadence: daily
    compute_strategy: manual_input
    parents:
      - parent_id: x
`)},
	}
	_, err := cat.Load(fsys, cat.MapRegistry{})
	if !errors.Is(err, cat.ErrSelfLoop) {
		t.Fatalf("Load: want ErrSelfLoop, got %v", err)
	}
}

func TestLoad_RejectsUnknownParent(t *testing.T) {
	fsys := fstest.MapFS{
		"unknownparent.yaml": &fstest.MapFile{Data: []byte(`metrics:
  - id: only
    level: program
    category: posture
    name: Only
    description: Only
    unit: percent
    cadence: daily
    compute_strategy: manual_input
    parents:
      - parent_id: nope
`)},
	}
	_, err := cat.Load(fsys, cat.MapRegistry{})
	if !errors.Is(err, cat.ErrUnknownParent) {
		t.Fatalf("Load: want ErrUnknownParent, got %v", err)
	}
}

func TestLoad_RejectsUnknownEvaluator(t *testing.T) {
	fsys := fstest.MapFS{
		"unknowneval.yaml": &fstest.MapFile{Data: []byte(`metrics:
  - id: comp
    level: board
    category: posture
    name: Comp
    description: Computed
    unit: percent
    cadence: daily
    compute_strategy: computed
    compute_evaluator: nonexistent
`)},
	}
	_, err := cat.Load(fsys, cat.MapRegistry{})
	if !errors.Is(err, cat.ErrUnknownEvaluator) {
		t.Fatalf("Load: want ErrUnknownEvaluator, got %v", err)
	}
}

func TestLoad_RejectsDuplicateID(t *testing.T) {
	fsys := fstest.MapFS{
		"a.yaml": &fstest.MapFile{Data: []byte(`metrics:
  - id: dup
    level: board
    category: posture
    name: A
    description: A
    unit: percent
    cadence: daily
    compute_strategy: manual_input
`)},
		"b.yaml": &fstest.MapFile{Data: []byte(`metrics:
  - id: dup
    level: program
    category: posture
    name: B
    description: B
    unit: percent
    cadence: daily
    compute_strategy: manual_input
`)},
	}
	_, err := cat.Load(fsys, cat.MapRegistry{})
	if err == nil || !contains(err.Error(), "duplicate metric id") {
		t.Fatalf("Load: want duplicate-metric-id error, got %v", err)
	}
}

func TestLoad_ComputedWithoutEvaluatorIsRejected(t *testing.T) {
	fsys := fstest.MapFS{
		"missingeval.yaml": &fstest.MapFile{Data: []byte(`metrics:
  - id: nope
    level: board
    category: posture
    name: Nope
    description: Nope
    unit: percent
    cadence: daily
    compute_strategy: computed
`)},
	}
	_, err := cat.Load(fsys, cat.MapRegistry{})
	if err == nil || !contains(err.Error(), "compute_evaluator") {
		t.Fatalf("Load: want compute_evaluator-required error, got %v", err)
	}
}

func TestLoad_ManualWithEvaluatorIsRejected(t *testing.T) {
	fsys := fstest.MapFS{
		"manualeval.yaml": &fstest.MapFile{Data: []byte(`metrics:
  - id: bogus
    level: board
    category: posture
    name: Bogus
    description: Bogus
    unit: percent
    cadence: daily
    compute_strategy: manual_input
    compute_evaluator: should_not_be_here
`)},
	}
	_, err := cat.Load(fsys, cat.MapRegistry{"should_not_be_here": {}})
	if err == nil || !contains(err.Error(), "compute_evaluator must be empty") {
		t.Fatalf("Load: want compute_evaluator-must-be-empty error, got %v", err)
	}
}

func TestLoad_AcceptsRealCatalog(t *testing.T) {
	// Smoke-test the actual catalogs/metrics/ directory by mirroring the
	// same load logic used at boot. Registers exactly the 8 evaluators
	// the catalog references.
	fsys := realCatalogFS(t)
	registered := cat.MapRegistry{
		"program_effectiveness":        {},
		"audit_readiness_score":        {},
		"evidence_freshness_pct":       {},
		"open_risk_financial_exposure": {},
		"policy_attestation_rate":      {},
		"vendor_risk_concentration":    {},
		"exception_expiration_runway":  {},
		"critical_findings_sla":        {},
	}
	c, err := cat.Load(fsys, registered)
	if err != nil {
		t.Fatalf("Load real catalog: %v", err)
	}
	if got := len(c.Metrics); got < 35 || got > 45 {
		t.Errorf("Load real catalog: got %d metrics, want 35-45", got)
	}
	// Every parent reference resolved (Load already guarantees this) and
	// every computed metric is one of the eight registered evaluators.
	for _, m := range c.Metrics {
		if m.ComputeStrategy == cat.StrategyComputed {
			if !registered.Has(m.ComputeEvaluator) {
				t.Errorf("metric %s names unregistered evaluator %q", m.ID, m.ComputeEvaluator)
			}
		}
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
