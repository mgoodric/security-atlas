package schemaregistry

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Semver is a minimal X.Y.Z parser. Pre-release / build metadata are not
// accepted — the schema registry uses semver as a strict three-tuple of
// non-negative integers per AC-5. Callers wanting pre-release semantics
// graduate to github.com/Masterminds/semver in a later slice.
type Semver struct {
	Major int
	Minor int
	Patch int
}

// ParseSemver parses "X.Y.Z" where X, Y, Z are non-negative integers.
// Anything else is a hard error: silent acceptance is the path to AC-5
// regressions (silent major-version auto-bump).
func ParseSemver(s string) (Semver, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Semver{}, fmt.Errorf("semver: expected X.Y.Z, got %q", s)
	}
	out := Semver{}
	for i, raw := range parts {
		if raw == "" {
			return Semver{}, fmt.Errorf("semver: empty component %d in %q", i, s)
		}
		// Reject leading zeros to keep the canonical form unique (1.0.0 vs 1.0.00).
		if len(raw) > 1 && raw[0] == '0' {
			return Semver{}, fmt.Errorf("semver: leading zero in component %d of %q", i, s)
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return Semver{}, fmt.Errorf("semver: component %d of %q is not a non-negative integer", i, s)
		}
		switch i {
		case 0:
			out.Major = n
		case 1:
			out.Minor = n
		case 2:
			out.Patch = n
		}
	}
	return out, nil
}

// String renders the canonical form.
func (s Semver) String() string {
	return fmt.Sprintf("%d.%d.%d", s.Major, s.Minor, s.Patch)
}

// Compare returns -1 if s < other, 0 if equal, +1 if s > other.
func (s Semver) Compare(other Semver) int {
	if s.Major != other.Major {
		if s.Major < other.Major {
			return -1
		}
		return 1
	}
	if s.Minor != other.Minor {
		if s.Minor < other.Minor {
			return -1
		}
		return 1
	}
	if s.Patch != other.Patch {
		if s.Patch < other.Patch {
			return -1
		}
		return 1
	}
	return 0
}

// ErrSemverConflict signals that the proposed (kind, semver) violates
// AC-5: either a duplicate, a non-additive minor bump, or a silent major
// jump. The reason is human-readable for surfacing to the caller.
var ErrSemverConflict = errors.New("schemaregistry: semver conflict")

// SemverConflict carries the structured details. Inspect via errors.As.
type SemverConflict struct {
	Kind     string
	Proposed string
	Existing []string
	Reason   string
}

func (c *SemverConflict) Error() string {
	return fmt.Sprintf("schemaregistry: semver conflict for %s proposed=%s: %s (existing: %v)",
		c.Kind, c.Proposed, c.Reason, c.Existing)
}

func (c *SemverConflict) Unwrap() error { return ErrSemverConflict }

// EnforceSemver checks AC-5 rules:
//
//   - The proposed semver must not already exist for the kind.
//   - A new major version is allowed (caller is explicit about a break).
//   - A new minor version requires its schema to be additive over the
//     highest prior minor on the same major. Additivity is the caller's
//     responsibility to assert via CheckAdditiveOver; this function only
//     enforces ordering and existence.
//   - A new patch version is always permitted as long as it sits above the
//     highest existing patch on the same (major, minor).
//
// existing must be sorted in any order; this function does its own
// comparison. Empty existing is the "first-ever registration" case and
// always passes.
func EnforceSemver(kind string, proposed Semver, existing []Semver) error {
	highestPerMajor := map[int]Semver{}
	highestPerMajorMinor := map[[2]int]Semver{}
	asStrings := make([]string, 0, len(existing))
	for _, e := range existing {
		asStrings = append(asStrings, e.String())
		if cur, ok := highestPerMajor[e.Major]; !ok || e.Compare(cur) > 0 {
			highestPerMajor[e.Major] = e
		}
		key := [2]int{e.Major, e.Minor}
		if cur, ok := highestPerMajorMinor[key]; !ok || e.Compare(cur) > 0 {
			highestPerMajorMinor[key] = e
		}
		if e.Compare(proposed) == 0 {
			return &SemverConflict{
				Kind:     kind,
				Proposed: proposed.String(),
				Existing: asStrings,
				Reason:   "duplicate semver",
			}
		}
	}

	// Major bump: only allowed if it is strictly higher than every existing
	// major. This blocks the AC-5 anti-pattern "silent major auto-bump"
	// (i.e., registering 2.0.0 when 3.0.0 already exists is forbidden, and
	// jumping from 1.x to 3.x without ever shipping 2.x is also forbidden).
	if _, ok := highestPerMajor[proposed.Major]; !ok {
		for m := range highestPerMajor {
			if m >= proposed.Major {
				return &SemverConflict{
					Kind:     kind,
					Proposed: proposed.String(),
					Existing: asStrings,
					Reason:   "new major must be strictly higher than every existing major",
				}
			}
			if m+1 < proposed.Major {
				return &SemverConflict{
					Kind:     kind,
					Proposed: proposed.String(),
					Existing: asStrings,
					Reason:   "new major must be exactly one above the current highest major",
				}
			}
		}
		// First-ever major or a +1 step. Patch/minor must be 0.
		if proposed.Minor != 0 || proposed.Patch != 0 {
			return &SemverConflict{
				Kind:     kind,
				Proposed: proposed.String(),
				Existing: asStrings,
				Reason:   "new major requires minor=0 and patch=0",
			}
		}
		return nil
	}

	// Same major already exists. The proposed must be strictly greater than
	// the highest existing version on this major.
	cur := highestPerMajor[proposed.Major]
	if proposed.Compare(cur) <= 0 {
		return &SemverConflict{
			Kind:     kind,
			Proposed: proposed.String(),
			Existing: asStrings,
			Reason:   "proposed must be strictly greater than the current highest on the same major",
		}
	}

	// Minor bump: patch must be 0 and minor must be exactly highest_minor+1.
	if proposed.Minor > cur.Minor {
		if proposed.Minor != cur.Minor+1 {
			return &SemverConflict{
				Kind:     kind,
				Proposed: proposed.String(),
				Existing: asStrings,
				Reason:   "minor bump must be exactly +1 over the current highest minor",
			}
		}
		if proposed.Patch != 0 {
			return &SemverConflict{
				Kind:     kind,
				Proposed: proposed.String(),
				Existing: asStrings,
				Reason:   "minor bump requires patch=0",
			}
		}
		return nil
	}

	// Patch bump: minor matches the highest, patch must be highest_patch+1
	// (no gaps).
	prior, ok := highestPerMajorMinor[[2]int{proposed.Major, proposed.Minor}]
	if !ok {
		return &SemverConflict{
			Kind:     kind,
			Proposed: proposed.String(),
			Existing: asStrings,
			Reason:   "no prior version on this (major, minor); use a minor bump",
		}
	}
	if proposed.Patch != prior.Patch+1 {
		return &SemverConflict{
			Kind:     kind,
			Proposed: proposed.String(),
			Existing: asStrings,
			Reason:   "patch bump must be exactly +1 over the highest patch on this (major, minor)",
		}
	}
	return nil
}
