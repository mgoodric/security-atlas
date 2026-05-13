// Severity functions for slice 053 manual aggregation (canvas §6.6).
//
// A child risk's severity scalar is `likelihood × impact` on the 5×5 grid
// (canvas §2.2). Slice 053 restricts aggregation to children whose methodology
// is in {nist_800_30, qualitative_5x5} — these share the (likelihood, impact)
// 1..5 shape so the scalar is comparable. Other methodologies (FAIR LEF/LM,
// permissive cis_ram/iso_27005) are rejected at validation time so the
// arithmetic stays meaningful.
//
// The scale max is 25 (5 × 5). All severity functions cap at 25 — a parent
// pattern can't be more severe than the worst single-cell risk.
//
// Severity functions:
//
//   - max          — severity of the highest child. Default (canvas §6.6).
//   - weighted_max — max × (1 + log10(child_count)), capped at 25.
//   - sum          — sum of all child severities, capped at 25.
//
// `custom_rego` (canvas §6.6) is deferred to slice 054+ (rule engine + OPA).
//
// The parent risk stores the result inside its `inherent_score` JSONB as:
//
//	{
//	  "likelihood": L,
//	  "impact":     I,
//	  "severity":   S,
//	  "severity_function": "max" | "weighted_max" | "sum",
//	  "child_count": N,
//	  "aggregation_key": "sha256-hex"
//	}
//
// `likelihood` and `impact` are derived from `severity` so the slice-019
// heatmap query (which groups by (likelihood, impact)) still groups parent
// risks correctly. Derivation: L = min(5, ceil(sqrt(S))); I = min(5, ceil(S/L)).
// The raw `severity` field is the load-bearing value; (L, I) is for display.
package risk

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// SeverityFunction is the closed set of aggregation functions slice 053 supports.
type SeverityFunction string

const (
	SeverityFunctionMax         SeverityFunction = "max"
	SeverityFunctionWeightedMax SeverityFunction = "weighted_max"
	SeverityFunctionSum         SeverityFunction = "sum"
)

// SeverityScaleMax is the 5×5 grid maximum (canvas §2.2 / slice 019).
const SeverityScaleMax = 25

// ErrUnknownSeverityFunction is returned when the caller supplies a function
// name outside the closed set above.
var ErrUnknownSeverityFunction = errors.New("risk: unknown severity_function")

// ErrIncompatibleMethodology is returned when a child risk's methodology is
// not in {nist_800_30, qualitative_5x5}. Aggregation across incompatible
// scales (FAIR LEF/LM vs the 5×5 grid) is meaningless without a per-scale
// normaliser — deferred to slice 054.
var ErrIncompatibleMethodology = errors.New("risk: child methodology not eligible for aggregation")

// ErrEmptyChildren is returned when no child risks were supplied.
var ErrEmptyChildren = errors.New("risk: aggregate requires at least one child")

// ChildScore is the (likelihood, impact) pair extracted from a child risk's
// inherent_score. Severity = likelihood × impact (canvas §6.6 — "severity of
// the highest child risk" on the 5×5 grid).
type ChildScore struct {
	Likelihood int
	Impact     int
}

// Severity returns likelihood × impact.
func (c ChildScore) Severity() int { return c.Likelihood * c.Impact }

// ComputeSeverity returns the aggregated severity for the given child scores
// under the chosen function. Errors when `fn` is unknown or `scores` is empty.
//
// `max`:          max(c.Severity() for c in scores)
// `weighted_max`: max * (1 + log10(N)) capped at SeverityScaleMax
// `sum`:          sum(c.Severity() for c in scores) capped at SeverityScaleMax
func ComputeSeverity(fn SeverityFunction, scores []ChildScore) (int, error) {
	if len(scores) == 0 {
		return 0, ErrEmptyChildren
	}
	switch fn {
	case SeverityFunctionMax:
		m := 0
		for _, c := range scores {
			if s := c.Severity(); s > m {
				m = s
			}
		}
		return capScale(m), nil
	case SeverityFunctionWeightedMax:
		m := 0
		for _, c := range scores {
			if s := c.Severity(); s > m {
				m = s
			}
		}
		raw := float64(m) * (1 + math.Log10(float64(len(scores))))
		// math.Ceil tracks the canvas spec literally ("max × (1 + log10(N))"
		// rounded up to the next integer point on the grid). When N=1 the
		// log term is 0 so the result is exactly the max.
		return capScale(int(math.Ceil(raw))), nil
	case SeverityFunctionSum:
		total := 0
		for _, c := range scores {
			total += c.Severity()
		}
		return capScale(total), nil
	default:
		return 0, fmt.Errorf("%w: %q", ErrUnknownSeverityFunction, fn)
	}
}

// DeriveGridCell returns (likelihood, impact) on the 5×5 grid that
// approximately reproduces the given severity. Used so a parent risk
// can satisfy the qualitative_5x5 schema and the slice-019 heatmap
// query keeps grouping it sensibly.
//
//   - likelihood = min(5, ceil(sqrt(severity)))
//   - impact     = min(5, ceil(severity / likelihood))
//
// Examples:
//
//	severity 25 -> (5, 5)
//	severity 18 -> (5, 4)  // ceil(sqrt(18))=5; ceil(18/5)=4
//	severity 12 -> (4, 3)  // ceil(sqrt(12))=4; ceil(12/4)=3
//	severity  9 -> (3, 3)  // ceil(sqrt(9))=3; ceil(9/3)=3
//	severity  1 -> (1, 1)
func DeriveGridCell(severity int) (likelihood, impact int) {
	if severity <= 0 {
		return 1, 1
	}
	if severity > SeverityScaleMax {
		severity = SeverityScaleMax
	}
	l := int(math.Ceil(math.Sqrt(float64(severity))))
	if l < 1 {
		l = 1
	}
	if l > 5 {
		l = 5
	}
	i := int(math.Ceil(float64(severity) / float64(l)))
	if i < 1 {
		i = 1
	}
	if i > 5 {
		i = 5
	}
	return l, i
}

// AggregationKey is a deterministic identifier for the (parent_title,
// sorted_child_uuid_set) pair used by AC-7 idempotency. Calling
// /v1/risks/aggregate twice with the same title + same set returns the
// same parent — never duplicates.
//
// Format: sha256_hex(parent_title + "|" + sorted_child_uuids.join("|")).
// UUIDs are normalised via uuid.String() (lowercase canonical form) before
// sorting.
func AggregationKey(parentTitle string, children []uuid.UUID) string {
	ids := make([]string, len(children))
	for i, c := range children {
		ids[i] = c.String()
	}
	sort.Strings(ids)
	h := sha256.New()
	h.Write([]byte(parentTitle))
	h.Write([]byte("|"))
	for i, id := range ids {
		if i > 0 {
			h.Write([]byte("|"))
		}
		h.Write([]byte(id))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// IsAggregableMethodology returns true when a child risk's methodology shares
// the 5×5 (likelihood, impact 1..5) shape required for slice 053 aggregation.
func IsAggregableMethodology(m dbx.RiskMethodology) bool {
	return m == dbx.RiskMethodologyNist80030 || m == dbx.RiskMethodologyQualitative5x5
}

// AllowedSeverityFunctions returns the closed set of severity functions the
// platform supports in slice 053. Slice 054 adds `custom_rego`.
func AllowedSeverityFunctions() []SeverityFunction {
	return []SeverityFunction{SeverityFunctionMax, SeverityFunctionWeightedMax, SeverityFunctionSum}
}

func capScale(v int) int {
	if v > SeverityScaleMax {
		return SeverityScaleMax
	}
	if v < 0 {
		return 0
	}
	return v
}
