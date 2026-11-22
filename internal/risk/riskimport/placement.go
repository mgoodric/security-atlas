package riskimport

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/risk"
)

// Imported risks are placed in the organisation's hierarchy (spec #2812,
// Q17; #2845):
//
//   - a level column (team, org, company, ignoring case) is the risk's
//     level. Any other value rejects the row at parse (invalid_level);
//   - an org unit column is matched by name, trimmed and ignoring case, to
//     the uploading tenant's org units. A name that matches none rejects
//     the row (unknown_org_unit), and so does a name two units share
//     (ambiguous_org_unit), since either guess could misplace the risk;
//   - the pair then follows the create path's rule (risk.CheckCreatePlacement):
//     with a unit and no level the risk takes the unit's level, and a level
//     that differs from the unit's rejects the row (level_mismatch). With
//     neither, the risk is a team risk with no unit.
//
// Approval creates the risk with the staged level and unit, so the create
// path re-checks both. A unit deleted, or moved to another level, after
// upload is refused there and the row stays pending with the reason.

// Placement fields. Like the others they are column-map keys.
const (
	FieldLevel   = "level"
	FieldOrgUnit = "org_unit"
)

// Placement rejection reasons.
const (
	RejectInvalidLevel     = "invalid_level"
	RejectUnknownOrgUnit   = "unknown_org_unit"
	RejectAmbiguousOrgUnit = "ambiguous_org_unit"
	RejectLevelMismatch    = "level_mismatch"
)

// parseLevel reads a level cell. A blank cell names no level.
func parseLevel(raw string) (dbx.RiskLevel, bool) {
	switch l := dbx.RiskLevel(strings.ToLower(strings.TrimSpace(raw))); l {
	case "":
		return "", true
	case dbx.RiskLevelTeam, dbx.RiskLevelOrg, dbx.RiskLevelCompany:
		return l, true
	default:
		return "", false
	}
}

// orgUnitKey is how an org unit cell and an org unit name are compared.
func orgUnitKey(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// orgUnitIndex groups the tenant's org units by orgUnitKey(name).
type orgUnitIndex map[string][]risk.OrgUnit

func indexOrgUnits(units []risk.OrgUnit) orgUnitIndex {
	out := orgUnitIndex{}
	for _, u := range units {
		k := orgUnitKey(u.Name)
		out[k] = append(out[k], u)
	}
	return out
}

// applyPlacement sets the row's level and org unit from the staged risk,
// or returns the rejection when the row cannot be placed.
func applyPlacement(row *BatchRow, s StagedRisk, units orgUnitIndex) (RejectedRow, bool) {
	reject := func(reason, detail string) (RejectedRow, bool) {
		return RejectedRow{RowNumber: s.RowNumber, ExternalRef: s.ExternalRef, Reason: reason, Detail: detail}, false
	}
	var unitLevel *dbx.RiskLevel
	if s.OrgUnit != "" {
		matches := units[orgUnitKey(s.OrgUnit)]
		switch len(matches) {
		case 0:
			return reject(RejectUnknownOrgUnit, fmt.Sprintf("org unit %q matches none of this organisation's org units", s.OrgUnit))
		case 1:
		default:
			return reject(RejectAmbiguousOrgUnit, fmt.Sprintf("org unit %q is the name of %d org units", s.OrgUnit, len(matches)))
		}
		id, level := matches[0].ID, matches[0].Level
		row.OrgUnitID = id.String()
		row.OrgUnitValue = s.OrgUnit
		unitLevel = &level
	}
	if err := risk.CheckCreatePlacement(s.Level, unitLevel); err != nil {
		if errors.Is(err, risk.ErrLevelMismatch) {
			return reject(RejectLevelMismatch, fmt.Sprintf("level %q differs from org unit %q's level %q", s.Level, s.OrgUnit, *unitLevel))
		}
		return reject(RejectInvalidLevel, err.Error())
	}
	row.Level = string(s.Level)
	return RejectedRow{}, true
}
