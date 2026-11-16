// Package posturetargets serves an organisation's posture target per
// framework version (#2136):
//
//	GET /v1/framework-scopes/posture-targets/{framework_version_id}  read
//	PUT /v1/framework-scopes/posture-targets/{framework_version_id}  set
//
// A target is the coverage the organisation aims for on a framework
// version, with a warning and a critical threshold and the direction that
// counts as better: the four fields metric_targets carries. The dashboard's
// posture tiles colour against it (GET /v1/frameworks/posture returns it on
// each row) and stay neutral without one.
//
// Every signed-in role may read a target. Only admin and grc_engineer may
// set one: OPA admits the write on the framework-scopes resource to those
// two roles, and the handler checks it again (hasTargetAdmin), as the
// risk-taxonomy writes do. There is no "GRC lead" role; the ticket's GRC
// lead is grc_engineer.
//
// A set is refused unless the framework version is one the tenant can see
// (catalog or its own) and the tenant has an activated scope for it: a
// version with no activated scope shows "Scope not set" and has no
// coverage to aim at. Each set writes one framework_posture_target_audit_log
// row (before -> after) in the same transaction.
package posturetargets

import (
	"errors"
	"fmt"
	"math"
)

// The three directions metric_targets uses; the table CHECK mirrors them.
const (
	DirectionHigherIsBetter = "higher_is_better"
	DirectionLowerIsBetter  = "lower_is_better"
	DirectionTargetIsBetter = "target_is_better"
)

// Target is one framework version's posture target. Values are coverage
// percentages, 0..100.
type Target struct {
	FrameworkVersionID string  `json:"framework_version_id"`
	TargetValue        float64 `json:"target_value"`
	WarningThreshold   float64 `json:"warning_threshold"`
	CriticalThreshold  float64 `json:"critical_threshold"`
	Direction          string  `json:"direction"`
	UpdatedBy          string  `json:"updated_by,omitempty"`
	UpdatedAt          string  `json:"updated_at,omitempty"`
}

// Input is the body of a set.
type Input struct {
	TargetValue       *float64 `json:"target_value"`
	WarningThreshold  *float64 `json:"warning_threshold"`
	CriticalThreshold *float64 `json:"critical_threshold"`
	Direction         string   `json:"direction"`
}

// ErrInvalid wraps every validation failure; the handler answers 400 with
// its message.
var ErrInvalid = errors.New("invalid posture target")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Validate checks a set body and returns the values to store. All four
// fields are required; values are 0..100 with at most two decimals; and
// the thresholds are ordered the way the direction says:
//
//	higher_is_better   critical <= warning <= target
//	lower_is_better    target <= warning <= critical
//	target_is_better   |warning - target| <= |critical - target|
//
// The table carries the same rules as CHECK constraints.
//
// The checks run on whole hundredths, the NUMERIC(5,2) the table stores,
// not on the float64s: 70.1 * 100 is 7009.999999999999 in binary floating
// point, so comparing floats would refuse values a person typed with two
// decimals, and could order two thresholds differently from the CHECK.
func (in Input) Validate() (target, warning, critical float64, err error) {
	if in.TargetValue == nil || in.WarningThreshold == nil || in.CriticalThreshold == nil {
		return 0, 0, 0, invalid("target_value, warning_threshold and critical_threshold are required")
	}
	t, err := hundredths("target_value", *in.TargetValue)
	if err != nil {
		return 0, 0, 0, err
	}
	w, err := hundredths("warning_threshold", *in.WarningThreshold)
	if err != nil {
		return 0, 0, 0, err
	}
	c, err := hundredths("critical_threshold", *in.CriticalThreshold)
	if err != nil {
		return 0, 0, 0, err
	}
	switch in.Direction {
	case DirectionHigherIsBetter:
		if c > w || w > t {
			return 0, 0, 0, invalid("higher_is_better needs critical_threshold <= warning_threshold <= target_value")
		}
	case DirectionLowerIsBetter:
		if t > w || w > c {
			return 0, 0, 0, invalid("lower_is_better needs target_value <= warning_threshold <= critical_threshold")
		}
	case DirectionTargetIsBetter:
		if abs(w-t) > abs(c-t) {
			return 0, 0, 0, invalid("target_is_better needs warning_threshold no further from target_value than critical_threshold")
		}
	default:
		return 0, 0, 0, invalid("direction must be higher_is_better, lower_is_better or target_is_better")
	}
	return float64(t) / 100, float64(w) / 100, float64(c) / 100, nil
}

// decimalTolerance is how far v*100 may sit from a whole number and still
// be read as a two-decimal value. Float error on a 0..100 value with two
// decimals is around 1e-12; a third decimal is at least 1e-1 away.
const decimalTolerance = 1e-6

// hundredths reads v as a whole number of hundredths, refusing a value
// outside 0..100 or with a third decimal.
func hundredths(name string, v float64) (int64, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 100 {
		return 0, invalid("%s must be between 0 and 100", name)
	}
	scaled := v * 100
	h := math.Round(scaled)
	if math.Abs(scaled-h) > decimalTolerance {
		return 0, invalid("%s has more than two decimal places", name)
	}
	return int64(h), nil
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
