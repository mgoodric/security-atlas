// exception.go — the exception overlay on an evaluation (#2112).
//
// An active exception waives a control's normal evaluation in the scope cells
// its scope_cell_predicate covers. The engine records `excepted` for such a
// cell instead of the result the evidence produced. `excepted` is never a
// pass: it is its own value on control_evaluations.result.
//
// The logic here is pure. The engine reads the control's active exceptions
// inside its tenant-GUC transaction (so RLS binds the read) and hands them to
// exceptedIn together with the cell and the evaluation's wall clock.
package eval

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mgoodric/security-atlas/internal/scope"
)

// activeException is the slice of an exceptions row the overlay needs.
type activeException struct {
	predicate     []byte
	effectiveFrom *time.Time
	expiresAt     time.Time
}

// inForce reports whether the exception waives evaluation at `now`. The
// window is [effective_from, expires_at): an exception past expires_at no
// longer applies even before the auto-expiry cron flips its status, and one
// scheduled to start later does not apply yet. A NULL effective_from (never
// written for an active row today) is treated as already in force.
func (x activeException) inForce(now time.Time) bool {
	if x.effectiveFrom != nil && now.Before(*x.effectiveFrom) {
		return false
	}
	return now.Before(x.expiresAt)
}

// exceptedIn reports whether any exception in force at `now` covers the cell.
// A nil cell is the whole-tenant evaluation row (the control resolved to no
// scope cells); only a predicate that matches every cell covers it, because a
// narrower predicate cannot say which part of the tenant it means.
//
// A malformed predicate is an error rather than a silent "not covered": the
// write path canonicalizes predicates, so a bad one is a defect to surface.
func exceptedIn(exceptions []activeException, cell *scope.Cell, now time.Time) (bool, error) {
	for _, x := range exceptions {
		if !x.inForce(now) {
			continue
		}
		if cell == nil {
			all, err := matchesEveryCell(x.predicate)
			if err != nil {
				return false, err
			}
			if all {
				return true, nil
			}
			continue
		}
		hits, err := scope.Evaluate(x.predicate, []scope.Cell{*cell})
		if err != nil {
			return false, fmt.Errorf("exception scope predicate: %w", err)
		}
		if len(hits) == 1 {
			return true, nil
		}
	}
	return false, nil
}

// matchesEveryCell reports whether a predicate is the always-true form: empty,
// null, {} or {"op":"true"} (the exceptions table's documented "applies to
// every cell" spellings).
func matchesEveryCell(predicate []byte) (bool, error) {
	s := strings.TrimSpace(string(predicate))
	if s == "" || s == "null" || s == "{}" {
		return true, nil
	}
	var n struct {
		Op string `json:"op"`
	}
	if err := json.Unmarshal(predicate, &n); err != nil {
		return false, fmt.Errorf("exception scope predicate: %w", err)
	}
	return n.Op == "true", nil
}
