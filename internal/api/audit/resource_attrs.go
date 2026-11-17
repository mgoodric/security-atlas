package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authzmw"
	"github.com/mgoodric/security-atlas/internal/audit"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// maxSampleBodyPeekBytes bounds how much of a POST /v1/samples body the
// hydrator reads to find population_id. The body is a population id, a
// count and a seed; anything longer finds no id in its prefix and an
// auditor's draw is denied.
const maxSampleBodyPeekBytes = 64 << 10

// periodLookup is the slice of audit.Store the hydrator reads. Every
// method reads under the caller's tenant; a row of another tenant is
// absent.
type periodLookup interface {
	PopulationPeriod(ctx context.Context, id uuid.UUID) (uuid.UUID, bool, error)
	SamplePeriod(ctx context.Context, id uuid.UUID) (uuid.UUID, bool, error)
	EvidencePeriods(ctx context.Context, id uuid.UUID) (audit.EvidenceScope, bool, error)
	ControlPeriods(ctx context.Context, controlID uuid.UUID, since, until time.Time) ([]uuid.UUID, error)
}

// ResourceAttrs returns the authz resource-attrs hydrator for the auditor's
// period-scoped reads and writes (#2157). The auditor rules compare these
// attributes with the periods the caller is assigned
// (input.user.attrs.audit_period_ids):
//
//	GET  /v1/populations/{id}               audit_period_id  the population's period
//	POST /v1/samples                        audit_period_id  the body population_id's period
//	GET  /v1/samples/{id}                   audit_period_id  the sample's population's period
//	GET  /v1/samples/{id}/annotations       audit_period_id  (same)
//	POST /v1/samples/{id}/annotations       audit_period_id  (same)
//	GET  /v1/evidence/{id}                  audit_period_ids the periods whose populations contain the record
//	                                        scope_cell_id    the record's scope cell
//	GET  /v1/evidence?control_id=           audit_period_ids the periods whose populations cover the whole control
//	     &since=&until=                                      and whose window holds [since, until]
//
// It fails closed: a malformed id, an unknown or other-tenant row, a
// population attached to no period, or a list without control_id or
// without explicit since and until leaves
// the period attribute unset, and the auditor rules deny. A lookup error is
// returned, and the middleware answers 500. Every other request gets no
// attributes.
func ResourceAttrs(store periodLookup) authzmw.ResourceAttrsFunc {
	return func(r *http.Request) (map[string]any, error) {
		// No tenant means no credential: the decision default-denies, and
		// there is no tenant to look the row up in.
		if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
			return nil, nil
		}
		rest, ok := strings.CutPrefix(r.URL.Path, "/v1/")
		if !ok {
			return nil, nil
		}
		segs := strings.Split(rest, "/")
		switch {
		case len(segs) == 2 && segs[0] == "populations" && r.Method == http.MethodGet:
			return periodAttr(r.Context(), segs[1], store.PopulationPeriod)
		case len(segs) == 1 && segs[0] == "samples" && r.Method == http.MethodPost:
			return drawSampleAttrs(r, store)
		case len(segs) == 2 && segs[0] == "samples" && r.Method == http.MethodGet,
			len(segs) == 3 && segs[0] == "samples" && segs[2] == "annotations" &&
				(r.Method == http.MethodGet || r.Method == http.MethodPost):
			return periodAttr(r.Context(), segs[1], store.SamplePeriod)
		case len(segs) == 2 && segs[0] == "evidence" && r.Method == http.MethodGet:
			return evidenceRecordAttrs(r.Context(), segs[1], store)
		case len(segs) == 1 && segs[0] == "evidence" && r.Method == http.MethodGet:
			return evidenceListAttrs(r, store)
		}
		return nil, nil
	}
}

func periodAttr(ctx context.Context, rawID string, get func(context.Context, uuid.UUID) (uuid.UUID, bool, error)) (map[string]any, error) {
	id, err := uuid.Parse(rawID)
	if err != nil {
		return nil, nil
	}
	period, ok, err := get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("audit resource attrs: %w", err)
	}
	if !ok {
		return nil, nil
	}
	return map[string]any{"audit_period_id": period.String()}, nil
}

func drawSampleAttrs(r *http.Request, store periodLookup) (map[string]any, error) {
	peek, err := authzmw.PeekBody(r, maxSampleBodyPeekBytes)
	if err != nil {
		return nil, fmt.Errorf("audit resource attrs: read body: %w", err)
	}
	var req struct {
		PopulationID string `json:"population_id"`
	}
	if json.Unmarshal(peek, &req) != nil {
		return nil, nil
	}
	return periodAttr(r.Context(), req.PopulationID, store.PopulationPeriod)
}

func evidenceRecordAttrs(ctx context.Context, rawID string, store periodLookup) (map[string]any, error) {
	id, err := uuid.Parse(rawID)
	if err != nil {
		// /v1/evidence/freshness and the like: not a record.
		return nil, nil
	}
	es, ok, err := store.EvidencePeriods(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("audit resource attrs: %w", err)
	}
	if !ok {
		return nil, nil
	}
	return evidenceAttrs(es.Periods, es.ScopeCellID), nil
}

// evidenceListAttrs covers GET /v1/evidence. An auditor reads the list
// only for one control (?control_id=), and only when an assigned period's
// population covers every cell of it: the per-control list ignores
// ?scope_cell_id=, so a cell-scoped population must not admit it. A list
// without control_id gets no period, so no tenant-wide read.
//
// #2424: the list is bounded in time too. The caller passes explicit since
// and until (RFC 3339, parsed as the handler parses them), and an admitting
// population's window and freeze horizon must hold both. Without them the
// handler defaults to the last 30 days, and a caller-chosen range could
// reach years of the control's evidence, so a missing or malformed bound
// gets no period.
func evidenceListAttrs(r *http.Request, store periodLookup) (map[string]any, error) {
	q := r.URL.Query()
	controlID, err := uuid.Parse(q.Get("control_id"))
	if err != nil {
		return nil, nil
	}
	since, err := time.Parse(time.RFC3339, q.Get("since"))
	if err != nil {
		return nil, nil
	}
	until, err := time.Parse(time.RFC3339, q.Get("until"))
	if err != nil {
		return nil, nil
	}
	periods, err := store.ControlPeriods(r.Context(), controlID, since.UTC(), until.UTC())
	if err != nil {
		return nil, fmt.Errorf("audit resource attrs: %w", err)
	}
	return evidenceAttrs(periods, nil), nil
}

func evidenceAttrs(periods []uuid.UUID, cell *uuid.UUID) map[string]any {
	ids := make([]any, len(periods))
	for i, p := range periods {
		ids[i] = p.String()
	}
	out := map[string]any{"audit_period_ids": ids}
	if cell != nil {
		out["scope_cell_id"] = cell.String()
	}
	return out
}
