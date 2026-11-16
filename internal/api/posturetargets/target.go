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
func (in Input) Validate() (target, warning, critical float64, err error) {
	if in.TargetValue == nil || in.WarningThreshold == nil || in.CriticalThreshold == nil {
		return 0, 0, 0, invalid("target_value, warning_threshold and critical_threshold are required")
	}
	target, warning, critical = *in.TargetValue, *in.WarningThreshold, *in.CriticalThreshold
	for name, v := range map[string]float64{
		"target_value":       target,
		"warning_threshold":  warning,
		"critical_threshold": critical,
	} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 100 {
			return 0, 0, 0, invalid("%s must be between 0 and 100", name)
		}
		if math.Round(v*100) != v*100 {
			return 0, 0, 0, invalid("%s has more than two decimal places", name)
		}
	}
	switch in.Direction {
	case DirectionHigherIsBetter:
		if !(critical <= warning && warning <= target) {
			return 0, 0, 0, invalid("higher_is_better needs critical_threshold <= warning_threshold <= target_value")
		}
	case DirectionLowerIsBetter:
		if !(target <= warning && warning <= critical) {
			return 0, 0, 0, invalid("lower_is_better needs target_value <= warning_threshold <= critical_threshold")
		}
	case DirectionTargetIsBetter:
		if math.Abs(warning-target) > math.Abs(critical-target) {
			return 0, 0, 0, invalid("target_is_better needs warning_threshold no further from target_value than critical_threshold")
		}
	default:
		return 0, 0, 0, invalid("direction must be higher_is_better, lower_is_better or target_is_better")
	}
	return target, warning, critical, nil
}
