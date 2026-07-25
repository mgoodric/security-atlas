// policy.go — slice 608: the per-tenant control-bundle upload gate policy.
//
// Slice 574 shipped a single GLOBAL gate policy (hard-block red tests;
// allow-with-warning a bundle with no tests). This file makes that policy
// per-tenant: a tenant admin can opt into a different posture via the
// `tenants.bundle_gate_mode` column (slice-608 migration). The gate
// (gate.go) takes a resolved GateMode as an input; the handler resolves it
// from the tenant row before calling runGate. Keeping the mode an explicit
// parameter (not a DB read inside the gate) keeps the gate logic pure and
// unit-testable without a database.
package controls

// GateMode is the per-tenant control-bundle upload gate policy. The three
// values cover the two opt-in dimensions slice 574 surfaced (decisions log
// 574 "Revisit once in use") with the minimum shape (one enum, not two
// orthogonal flags — see docs/audit-log/608-*-decisions.md D1):
//
//   - GateModeStrict (default): preserves the slice-574 global behaviour
//     exactly — a bundle with red tests is hard-blocked (400); a bundle with
//     no tests/ uploads with a warning.
//   - GateModeAdvisory: a bundle with red tests is ACCEPTED with the per-case
//     report attached as a warning (not a 400). For tenants authoring
//     iteratively who want the gate's feedback without it blocking.
//   - GateModeMandatoryTests: a bundle with NO tests/ is REJECTED. For tenants
//     who want every control test-backed. Red tests still hard-block (a
//     stricter posture than strict, never a looser one).
type GateMode string

const (
	// GateModeStrict is the default — and the value a tenant row carries via
	// the column DEFAULT — so an unset/absent tenant keeps the slice-574
	// behaviour with no backfill.
	GateModeStrict GateMode = "strict"

	// GateModeAdvisory accepts a red bundle with the report as a warning.
	GateModeAdvisory GateMode = "advisory"

	// GateModeMandatoryTests rejects a bundle that ships no tests/.
	GateModeMandatoryTests GateMode = "mandatory_tests"
)

// DefaultGateMode is the policy applied when a tenant has no explicit value
// (a tenants row that predates the slice-608 column, or no tenants row at
// all). It is GateModeStrict so a tenant that does nothing keeps the safe
// slice-574 default.
const DefaultGateMode = GateModeStrict

// ParseGateMode validates an inbound string (settings API input or a DB
// value) and returns the canonical GateMode. An empty string resolves to the
// default; any other unrecognised value returns ok=false so the caller can
// reject the input (the DB CHECK is the second leg).
func ParseGateMode(s string) (GateMode, bool) {
	switch GateMode(s) {
	case GateModeStrict:
		return GateModeStrict, true
	case GateModeAdvisory:
		return GateModeAdvisory, true
	case GateModeMandatoryTests:
		return GateModeMandatoryTests, true
	case "":
		return DefaultGateMode, true
	default:
		return "", false
	}
}

// Valid reports whether m is a recognised gate mode.
func (m GateMode) Valid() bool {
	switch m {
	case GateModeStrict, GateModeAdvisory, GateModeMandatoryTests:
		return true
	default:
		return false
	}
}
