// Risk record editing (#2104): PATCH /v1/risks/{id} changes the record
// fields beside placement and category. Treatment and acceptance are not
// record fields; they stay off PATCH.
//
// Every edit that changes something writes one risk_audit_log row,
// `fields_changed`, in the write's transaction. Its before_state and
// after_state hold only the fields that changed, keyed by wire name, so the
// History section can render "title: A -> B" without diffing whole rows.

package risk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// ErrRecordFieldInvalid is returned for a record field PATCH cannot store:
// an empty title, or an instrument_reference cleared on a transfer risk.
var ErrRecordFieldInvalid = errors.New("risk: invalid record field")

// ErrRuleGeneratedScore is returned when a PATCH edits the score or
// methodology of a rule-generated meta-risk. The aggregation engine owns
// that score and would overwrite the edit on its next evaluation.
var ErrRuleGeneratedScore = errors.New("risk: a rule-generated meta-risk's score and methodology are set by its aggregation rule")

// IsRecordValidation reports whether err is a caller error in a record
// field, which the HTTP layer renders as a 400.
func IsRecordValidation(err error) bool {
	return errors.Is(err, ErrRecordFieldInvalid) ||
		errors.Is(err, ErrRuleGeneratedScore) ||
		errors.Is(err, ErrInvalidMethodology) ||
		errors.Is(err, ErrInherentScoreInvalid)
}

// RecordFields are the record fields a PATCH may change. A nil field keeps
// its value.
type RecordFields struct {
	Title               *string
	Description         *string
	Methodology         *dbx.RiskMethodology
	InherentScore       json.RawMessage
	TreatmentOwner      *string
	ReviewDueAt         *time.Time
	InstrumentReference *string
}

// Empty reports whether no record field is set.
func (f RecordFields) Empty() bool {
	return f.Title == nil && f.Description == nil && f.Methodology == nil &&
		f.InherentScore == nil && f.TreatmentOwner == nil &&
		f.ReviewDueAt == nil && f.InstrumentReference == nil
}

// recordDiff is the changed-fields half of a fields_changed audit row.
type recordDiff map[string]any

// applyRecordFields returns the risk with in applied, and the before and
// after values of each field that changed. A methodology change resets the
// residual score as create sets it (FAIR: the inherent exposure; otherwise
// pending evaluation), because a residual on the old scale no longer reads
// on the new one.
func applyRecordFields(current Risk, in RecordFields) (Risk, recordDiff, recordDiff, error) {
	next := current
	before, after := recordDiff{}, recordDiff{}

	if in.Title != nil {
		title := strings.TrimSpace(*in.Title)
		if title == "" {
			return Risk{}, nil, nil, fmt.Errorf("%w: title cannot be empty", ErrRecordFieldInvalid)
		}
		next.Title = title
	}
	if in.Description != nil {
		next.Description = *in.Description
	}
	if in.TreatmentOwner != nil {
		next.TreatmentOwner = strings.TrimSpace(*in.TreatmentOwner)
	}
	if in.InstrumentReference != nil {
		next.InstrumentReference = strings.TrimSpace(*in.InstrumentReference)
		if next.Treatment == dbx.RiskTreatmentTransfer && next.InstrumentReference == "" {
			return Risk{}, nil, nil, fmt.Errorf("%w: a transfer risk needs its instrument_reference", ErrRecordFieldInvalid)
		}
	}
	if in.ReviewDueAt != nil {
		t := in.ReviewDueAt.UTC()
		next.ReviewDueAt = &t
	}

	scoreTouched := in.Methodology != nil || in.InherentScore != nil
	if scoreTouched {
		if isRuleGeneratedScore(current.InherentScore) {
			return Risk{}, nil, nil, ErrRuleGeneratedScore
		}
		if in.Methodology != nil {
			next.Methodology = *in.Methodology
		}
		if in.InherentScore != nil {
			next.InherentScore = []byte(in.InherentScore)
		}
		if err := ValidateInherentScore(next.Methodology, next.InherentScore); err != nil {
			return Risk{}, nil, nil, err
		}
		if next.Methodology == dbx.RiskMethodologyFair {
			normalized, err := NormalizeFairScoreJSON(next.InherentScore)
			if err != nil {
				return Risk{}, nil, nil, fmt.Errorf("%w: %v", ErrInherentScoreInvalid, err)
			}
			next.InherentScore = normalized
		}
		if next.Methodology != current.Methodology {
			if next.Methodology == dbx.RiskMethodologyFair {
				next.ResidualScore = next.InherentScore
			} else {
				next.ResidualScore = []byte("{}")
			}
		}
	}

	diffString := func(name, old, new string) {
		if old != new {
			before[name], after[name] = old, new
		}
	}
	diffString("title", current.Title, next.Title)
	diffString("description", current.Description, next.Description)
	diffString("treatment_owner", current.TreatmentOwner, next.TreatmentOwner)
	diffString("instrument_reference", current.InstrumentReference, next.InstrumentReference)
	diffString("methodology", string(current.Methodology), string(next.Methodology))
	if !sameTime(current.ReviewDueAt, next.ReviewDueAt) {
		before["review_due_at"], after["review_due_at"] = current.ReviewDueAt, next.ReviewDueAt
	}
	if !sameJSON(current.InherentScore, next.InherentScore) {
		before["inherent_score"], after["inherent_score"] = jsonValue(current.InherentScore), jsonValue(next.InherentScore)
	}
	if !sameJSON(current.ResidualScore, next.ResidualScore) {
		before["residual_score"], after["residual_score"] = jsonValue(current.ResidualScore), jsonValue(next.ResidualScore)
	}
	return next, before, after, nil
}

// updateRecordTx writes the record fields of next through UpdateRisk,
// carrying every treatment and acceptance column over unchanged, and
// records the change. The caller holds the row lock.
func updateRecordTx(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, next Risk, actor string, before, after recordDiff) (Risk, error) {
	row, err := q.UpdateRisk(ctx, dbx.UpdateRiskParams{
		TenantID:            pgUUID(tenantID),
		ID:                  pgUUID(next.ID),
		Title:               next.Title,
		Description:         next.Description,
		Methodology:         next.Methodology,
		InherentScore:       next.InherentScore,
		Treatment:           next.Treatment,
		TreatmentOwner:      next.TreatmentOwner,
		ResidualScore:       defaultResidual(next.ResidualScore),
		ReviewDueAt:         pgTimestamptzPtr(next.ReviewDueAt),
		AcceptedUntil:       pgDatePtr(next.AcceptedUntil),
		Accepter:            next.Accepter,
		InstrumentReference: next.InstrumentReference,
		AcceptanceStatus:    NullAcceptanceStatus(next.AcceptanceStatus),
	})
	if err != nil {
		return Risk{}, fmt.Errorf("update risk record: %w", err)
	}
	if err := insertRiskAudit(ctx, q, tenantID, next.ID, actor, "fields_changed", before, after); err != nil {
		return Risk{}, err
	}
	return riskFromRow(row), nil
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

func sameJSON(a, b []byte) bool {
	return reflect.DeepEqual(jsonValue(a), jsonValue(b))
}

// jsonValue decodes a JSONB value for comparison and for the audit row. An
// empty or undecodable value reads as an empty object, as the column
// defaults do.
func jsonValue(b []byte) any {
	var v any
	if len(b) == 0 || json.Unmarshal(b, &v) != nil || v == nil {
		return map[string]any{}
	}
	return v
}
