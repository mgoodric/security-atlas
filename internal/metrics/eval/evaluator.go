// Package eval ships the 8 starter computed-metric evaluators for the
// slice-076 metrics catalog. Each evaluator is a small Go function over
// existing primitives (control_evaluations, risks, policy_acknowledgments,
// audit_periods, exceptions, vendors, audit_notes). The 15-minute cron
// in internal/metrics/scheduler iterates each tenant × each registered
// evaluator and persists the result to metric_observations.
//
// All evaluators share the same signature so the scheduler can call them
// uniformly. RLS context is injected via the supplied ctx (the scheduler
// applies tenancy.WithTenant before invoking); evaluators do NOT take
// tenant ids in their parameters (P0-A6 in the slice doc).
package eval

import (
	"context"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Result is one evaluator's per-tenant output. Dimensions carry any
// per-framework / per-severity / per-category breakdown the evaluator
// wishes to expose. A nil/empty Dimensions map means "no breakdown".
type Result struct {
	Value      float64
	Dimensions map[string]string
}

// Evaluator is the uniform interface every starter evaluator implements.
// The interface stays narrow (Name + Compute) to keep the registry small.
type Evaluator interface {
	Name() string
	Compute(ctx context.Context) (Result, error)
}

// Registry is a set of registered evaluators, keyed by Name(). The catalog
// loader's EvaluatorRegistry interface (`Has(name) bool`) is satisfied via
// the Has method below — Registry doubles as the loader-validation source.
type Registry struct {
	pool       *pgxpool.Pool
	evaluators map[string]Evaluator
}

// NewRegistry constructs a Registry with the supplied app-role pool.
// Every starter evaluator gets the same pool — the scheduler applies
// tenant context per-call before invoking Compute.
func NewRegistry(pool *pgxpool.Pool) *Registry {
	r := &Registry{
		pool:       pool,
		evaluators: make(map[string]Evaluator),
	}
	r.registerStarters()
	return r
}

// registerStarters wires the 8 slice-076 starter evaluators. Adding a
// new evaluator requires (a) a new file in this package implementing the
// interface, (b) a r.register(...) call here, AND (c) a corresponding
// catalog YAML entry with compute_evaluator matching Name().
func (r *Registry) registerStarters() {
	r.register(&programEffectivenessEvaluator{pool: r.pool})
	r.register(&evidenceFreshnessPctEvaluator{pool: r.pool})
	r.register(&auditReadinessScoreEvaluator{pool: r.pool})
	r.register(&openRiskFinancialExposureEvaluator{pool: r.pool})
	r.register(&policyAttestationRateEvaluator{pool: r.pool})
	r.register(&vendorRiskConcentrationEvaluator{pool: r.pool})
	r.register(&exceptionExpirationRunwayEvaluator{pool: r.pool})
	r.register(&criticalFindingsSLAEvaluator{pool: r.pool})
}

func (r *Registry) register(e Evaluator) {
	r.evaluators[e.Name()] = e
}

// Has reports whether name is registered. Satisfies the catalog
// loader's EvaluatorRegistry interface.
func (r *Registry) Has(name string) bool {
	_, ok := r.evaluators[name]
	return ok
}

// Get returns the evaluator by name, or nil + false if unregistered.
func (r *Registry) Get(name string) (Evaluator, bool) {
	e, ok := r.evaluators[name]
	return e, ok
}

// Names returns the registered evaluator names in deterministic order
// (sorted) so logs and tests are reproducible.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.evaluators))
	for n := range r.evaluators {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
