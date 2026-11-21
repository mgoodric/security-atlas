package riskimport

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// Treatment and acceptance arrive under the rules a single create follows
// (spec #2812, Q14; #2842):
//
//   - an accepted row stages a proposed acceptance with its justification
//     and until-date. A create never approves an acceptance (#2371,
//     ADR-0024), and the register's accepter is kept on the staged row as
//     historical text, never as the risk's accepter;
//   - `mitigate` with no control link that resolves to the tenant's
//     controls stages as `untreated`, flagged treatment_downgraded;
//   - `transfer` with no instrument reference does the same.

// Treatment and acceptance fields. Like the others they are column-map keys.
const (
	FieldTreatment           = "treatment"
	FieldJustification       = "justification"
	FieldAcceptedUntil       = "accepted_until"
	FieldAccepter            = "accepter"
	FieldInstrumentReference = "instrument_reference"
	FieldLinkedControls      = "linked_controls"
)

// Rejection reasons for treatment and acceptance cells.
const (
	RejectInvalidTreatment  = "invalid_treatment"
	RejectInvalidAcceptance = "invalid_acceptance"
)

// treatmentValues are the cell spellings each treatment is read from,
// compared after normalizeHeader. "accept (proposed)" and "accept
// (approved)" are risk.TreatmentLabel's renderings; both stage proposed.
var treatmentValues = map[string]dbx.RiskTreatment{
	"untreated":         dbx.RiskTreatmentUntreated,
	"accept":            dbx.RiskTreatmentAccept,
	"accepted":          dbx.RiskTreatmentAccept,
	"accept (proposed)": dbx.RiskTreatmentAccept,
	"accept (approved)": dbx.RiskTreatmentAccept,
	"mitigate":          dbx.RiskTreatmentMitigate,
	"mitigated":         dbx.RiskTreatmentMitigate,
	"transfer":          dbx.RiskTreatmentTransfer,
	"transferred":       dbx.RiskTreatmentTransfer,
	"avoid":             dbx.RiskTreatmentAvoid,
	"avoided":           dbx.RiskTreatmentAvoid,
}

// parseTreatment reads a treatment cell; blank is untreated, as on a single
// create.
func parseTreatment(raw string) (dbx.RiskTreatment, bool) {
	if strings.TrimSpace(raw) == "" {
		return dbx.RiskTreatmentUntreated, true
	}
	t, ok := treatmentValues[normalizeHeader(raw)]
	return t, ok
}

// treatmentCells are a row's treatment and acceptance cells as read.
type treatmentCells struct {
	treatment, justification, acceptedUntil, accepter, instrument, controls string
}

// readTreatment checks a row's treatment cells and fills s. An accepted row
// must carry the justification and until-date its proposal needs; anything
// else about the treatment is decided at staging, where the control links
// resolve (applyTreatmentRules).
func readTreatment(s *StagedRisk, c treatmentCells) (reason, detail string, ok bool) {
	t, ok := parseTreatment(c.treatment)
	if !ok {
		return RejectInvalidTreatment, fmt.Sprintf("treatment %s is not one of untreated, accept, mitigate, transfer, avoid", quoteCell(c.treatment)), false
	}
	s.Treatment = t
	s.Justification = c.justification
	s.HistoricalAccepter = c.accepter
	s.InstrumentReference = c.instrument
	s.ControlRefs = splitControlRefs(c.controls)
	if c.acceptedUntil != "" {
		d, ok := parseDate(c.acceptedUntil)
		if !ok {
			return RejectInvalidAcceptance, fmt.Sprintf("accepted until %s is not a date (YYYY-MM-DD)", quoteCell(c.acceptedUntil)), false
		}
		s.ProposedUntil = &d
	}
	if t == dbx.RiskTreatmentAccept {
		var missing []string
		if s.Justification == "" {
			missing = append(missing, FieldJustification)
		}
		if s.ProposedUntil == nil {
			missing = append(missing, FieldAcceptedUntil)
		}
		if len(missing) > 0 {
			return RejectInvalidAcceptance, "an accepted risk imports as a proposed acceptance, which needs " + strings.Join(missing, " and "), false
		}
	}
	return "", "", true
}

// splitControlRefs splits a linked-controls cell on commas, semicolons and
// line breaks, dropping blanks and repeats.
func splitControlRefs(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' || r == '\n' || r == '\r' })
	seen := map[string]bool{}
	var out []string
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f == "" || seen[strings.ToLower(f)] {
			continue
		}
		seen[strings.ToLower(f)] = true
		out = append(out, f)
	}
	return out
}

// applyTreatmentRules sets the row's links from the resolved control refs
// and downgrades a treatment the row cannot back: `mitigate` with no
// resolved control link, or `transfer` with no instrument reference, stages
// as `untreated` with the treatment_downgraded flag and the register's
// treatment kept in RequestedTreatment. resolved maps a ref, as written, to
// the control it names; a ref not in it is kept in UnresolvedControls.
func applyTreatmentRules(row *BatchRow, requested dbx.RiskTreatment, refs []string, resolved map[string]uuid.UUID) {
	seen := map[uuid.UUID]bool{}
	for _, ref := range refs {
		id, ok := resolved[ref]
		if !ok {
			row.UnresolvedControls = append(row.UnresolvedControls, ref)
			continue
		}
		if !seen[id] {
			seen[id] = true
			row.LinkedControlIDs = append(row.LinkedControlIDs, id.String())
		}
	}
	sort.Strings(row.LinkedControlIDs)
	row.Treatment = string(requested)
	downgrade := (requested == dbx.RiskTreatmentMitigate && len(row.LinkedControlIDs) == 0) ||
		(requested == dbx.RiskTreatmentTransfer && row.InstrumentReference == "")
	if downgrade {
		row.Treatment = string(dbx.RiskTreatmentUntreated)
		row.RequestedTreatment = string(requested)
		row.Flags = append(row.Flags, FlagTreatmentDowngraded)
	}
}

// proposalOf is the acceptance a staged row's create proposes: its
// justification and until-date for an accepted row, none otherwise. A
// justification or until-date on a row that is not accepted stays on the
// staged row and is not sent, as a single create refuses them there.
func proposalOf(treatment dbx.RiskTreatment, justification string, until *time.Time) (dbx.RiskAcceptanceStatus, string, time.Time) {
	if treatment != dbx.RiskTreatmentAccept || until == nil {
		return "", "", time.Time{}
	}
	return dbx.RiskAcceptanceStatusProposed, justification, *until
}
