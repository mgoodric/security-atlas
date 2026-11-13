package risk

import (
	"errors"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// ErrTreatmentValidation is returned when a write fails the per-treatment
// required-field rules canvas §6.1 declares. Callers translate to 400.
var ErrTreatmentValidation = errors.New("risk: treatment validation")

// TreatmentInput is the slice of fields the validator consults. Decouples
// from any HTTP wire shape so the rules can be reused by other call sites
// (CSV import, evaluation engine).
type TreatmentInput struct {
	Treatment            dbx.RiskTreatment
	AcceptanceStatus     dbx.RiskAcceptanceStatus
	AcceptedUntilPresent bool
	Accepter             string
	InstrumentReference  string
	LinkedControlCount   int
	// LinkedActionPlanCount is the number of action plans linked to the
	// risk. A planned mitigation with no control in the tool yet is a
	// valid mitigate (#1746).
	LinkedActionPlanCount int
}

// ValidateTreatment enforces canvas §6.1's per-treatment rules:
//
//	untreated → no decision yet; no extra fields (the create default, #1746)
//	accept    → acceptance_status required. proposed (pending sign-off)
//	            needs nothing else; approved requires accepted_until +
//	            accepter (exec sign-off out-of-scope v1)
//	mitigate  → at least one linked control or linked action plan
//	transfer  → instrument_reference required (policy number / SOW)
//	avoid     → status-only, no extra fields
//
// acceptance_status is set exactly when treatment is accept; the DB CHECK
// risks_acceptance_status_iff_accept mirrors this.
//
// Returns nil on success, ErrTreatmentValidation (wrapped with a per-field
// reason) on failure.
func ValidateTreatment(in TreatmentInput) error {
	if in.Treatment != dbx.RiskTreatmentAccept && in.AcceptanceStatus != "" {
		return wrapTreatment("acceptance_status applies only to treatment=accept")
	}
	switch in.Treatment {
	case dbx.RiskTreatmentUntreated:
		// no decision yet; no extra fields required
	case dbx.RiskTreatmentAccept:
		switch in.AcceptanceStatus {
		case dbx.RiskAcceptanceStatusProposed:
			// pending sign-off; accepter + expiry come with approval
		case dbx.RiskAcceptanceStatusApproved:
			if !in.AcceptedUntilPresent {
				return wrapTreatment("approved acceptance requires accepted_until")
			}
			if in.Accepter == "" {
				return wrapTreatment("approved acceptance requires accepter")
			}
		case "":
			return wrapTreatment("treatment=accept requires acceptance_status (proposed or approved)")
		default:
			return wrapTreatment("acceptance_status is not a recognized value")
		}
	case dbx.RiskTreatmentMitigate:
		if in.LinkedControlCount < 1 && in.LinkedActionPlanCount < 1 {
			return wrapTreatment("treatment=mitigate requires at least one linked control or linked action plan")
		}
	case dbx.RiskTreatmentTransfer:
		if in.InstrumentReference == "" {
			return wrapTreatment("treatment=transfer requires instrument_reference")
		}
	case dbx.RiskTreatmentAvoid:
		// no extra fields required
	default:
		return wrapTreatment("treatment is not a recognized value")
	}
	return nil
}

func wrapTreatment(msg string) error {
	return &treatmentErr{msg: msg}
}

type treatmentErr struct{ msg string }

func (e *treatmentErr) Error() string { return "risk: treatment validation: " + e.msg }
func (e *treatmentErr) Unwrap() error { return ErrTreatmentValidation }

// IsTreatmentValidation reports whether err is a treatment-validation failure.
// Convenience for the HTTP handler's 400-vs-500 distinction.
func IsTreatmentValidation(err error) bool {
	return errors.Is(err, ErrTreatmentValidation)
}

// IsOpenTreatment reports whether a risk still counts as open exposure.
// Only an approved acceptance takes a risk out of the open set: untreated
// risks and acceptances still pending sign-off remain open (#1746). The
// open_risk_financial_exposure metric's SQL predicate mirrors this.
func IsOpenTreatment(t dbx.RiskTreatment, s dbx.RiskAcceptanceStatus) bool {
	return t != dbx.RiskTreatmentAccept || s != dbx.RiskAcceptanceStatusApproved
}

// TreatmentLabel renders a treatment for display surfaces (board pack,
// exports): "accept (proposed)" / "accept (approved)" for an acceptance,
// the bare treatment otherwise. An accept with no status renders as plain
// "accept".
func TreatmentLabel(t dbx.RiskTreatment, s dbx.RiskAcceptanceStatus) string {
	if t == dbx.RiskTreatmentAccept && s != "" {
		return string(t) + " (" + string(s) + ")"
	}
	return string(t)
}
