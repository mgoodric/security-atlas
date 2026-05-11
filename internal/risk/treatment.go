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
	AcceptedUntilPresent bool
	Accepter             string
	InstrumentReference  string
	LinkedControlCount   int
}

// ValidateTreatment enforces canvas §6.1's per-treatment rules:
//
//	accept   → accepted_until + accepter required (exec sign-off out-of-scope v1)
//	mitigate → at least one linked control required
//	transfer → instrument_reference required (policy number / SOW)
//	avoid    → status-only, no extra fields
//
// Returns nil on success, ErrTreatmentValidation (wrapped with a per-field
// reason) on failure.
func ValidateTreatment(in TreatmentInput) error {
	switch in.Treatment {
	case dbx.RiskTreatmentAccept:
		if !in.AcceptedUntilPresent {
			return wrapTreatment("treatment=accept requires accepted_until")
		}
		if in.Accepter == "" {
			return wrapTreatment("treatment=accept requires accepter")
		}
	case dbx.RiskTreatmentMitigate:
		if in.LinkedControlCount < 1 {
			return wrapTreatment("treatment=mitigate requires at least one linked_control_id")
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
