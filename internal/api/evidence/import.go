package evidence

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/evidence/historicalimport"
)

// importMaxBodyBytes caps POST /v1/evidence:import. A batch holds up to
// historicalimport.MaxRecords records; each record's payload is capped at
// 1 MiB by ingest, but an import is history, not artifacts, so the body cap
// is set for a large batch of small records.
const importMaxBodyBytes = 256 << 20 // 256 MiB

// ImportHandler serves POST /v1/evidence:import, the admin-only historical
// evidence import (#1742). It writes straight through the ingest stage in
// one transaction, never through the push stream: the batch commits or
// rolls back as a whole.
type ImportHandler struct {
	importer *historicalimport.Importer
}

// NewImportHandler wires the handler.
func NewImportHandler(importer *historicalimport.Importer) *ImportHandler {
	return &ImportHandler{importer: importer}
}

type importRequest struct {
	Source      string   `json:"source"`
	AmendFrozen []string `json:"amend_frozen,omitempty"`
	// AmendReason is required with amend_frozen (Matt, 2026-11-24).
	AmendReason string       `json:"amend_reason,omitempty"`
	Records     []recordWire `json:"records"`
}

type amendmentWire struct {
	AuditPeriodID   string    `json:"audit_period_id"`
	Revision        int       `json:"revision"`
	FrozenHash      string    `json:"frozen_hash"`
	PreviousHash    string    `json:"previous_hash"`
	FrozenAt        time.Time `json:"frozen_at"`
	Reason          string    `json:"amend_reason"`
	EvidenceAddedN  int       `json:"evidence_added_n"`
	AmendedByImport bool      `json:"amended_by_import"`
}

type importResponse struct {
	ImportBatchID string          `json:"import_batch_id"`
	Receipts      []receiptWire   `json:"receipts"`
	Amendments    []amendmentWire `json:"amendments"`
}

type frozenHitWire struct {
	AuditPeriodID string `json:"audit_period_id"`
	Name          string `json:"name"`
	Records       int    `json:"records"`
}

type frozenErrorBody struct {
	Error         string          `json:"error"`
	Code          string          `json:"code"`
	FrozenPeriods []frozenHitWire `json:"frozen_periods"`
}

// ImportHTTP handles POST /v1/evidence:import.
func (h *ImportHandler) ImportHTTP(w http.ResponseWriter, r *http.Request) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		httpresp.WriteJSON(w, http.StatusUnauthorized, errorBody{Error: "authentication required"})
		return
	}
	// Refuse before reading the body: a non-admin writes nothing, not even
	// an audit row.
	if cred.System || !authz.IsTenantAdmin(cred) {
		httpresp.WriteJSON(w, http.StatusForbidden, errorBody{Error: "admin credential required", Code: "forbidden"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, importMaxBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		httpresp.WriteJSON(w, http.StatusRequestEntityTooLarge, errorBody{Error: "body too large or unreadable: " + err.Error(), Code: "oversized"})
		return
	}
	var req importRequest
	if err := json.Unmarshal(body, &req); err != nil {
		httpresp.WriteJSON(w, http.StatusBadRequest, errorBody{Error: "invalid JSON body: " + err.Error(), Code: "invalid_json"})
		return
	}

	in := historicalimport.Request{Source: req.Source, AmendReason: req.AmendReason}
	for _, s := range req.AmendFrozen {
		id, err := uuid.Parse(s)
		if err != nil {
			httpresp.WriteJSON(w, http.StatusBadRequest, errorBody{Error: fmt.Sprintf("amend_frozen %q is not a UUID", s), Code: "invalid_request"})
			return
		}
		in.AmendFrozen = append(in.AmendFrozen, id)
	}
	in.Records = make([]*evidencev1.EvidenceRecord, 0, len(req.Records))
	for i, rw := range req.Records {
		rec, err := recordWireToProto(rw)
		if err != nil {
			httpresp.WriteJSON(w, http.StatusBadRequest, errorBody{Error: fmt.Sprintf("record[%d]: %s", i, err.Error()), Code: "invalid_request"})
			return
		}
		in.Records = append(in.Records, rec)
	}

	res, err := h.importer.Import(r.Context(), cred, in)
	if err != nil {
		writeImportError(w, r, err)
		return
	}
	out := importResponse{
		ImportBatchID: res.BatchID.String(),
		Receipts:      make([]receiptWire, 0, len(res.Receipts)),
		Amendments:    make([]amendmentWire, 0, len(res.Amendments)),
	}
	for _, rc := range res.Receipts {
		out.Receipts = append(out.Receipts, receiptWire{
			RecordID:     rc.RecordID,
			Hash:         rc.Hash,
			IngestedAt:   rc.IngestedAt.Format(time.RFC3339Nano),
			CredentialID: rc.CredentialID,
			Deduplicated: rc.Deduplicated,
		})
	}
	for _, a := range res.Amendments {
		out.Amendments = append(out.Amendments, amendmentWire{
			AuditPeriodID:   a.PeriodID.String(),
			Revision:        a.Revision.Revision,
			FrozenHash:      hex.EncodeToString(a.Revision.FrozenHash),
			PreviousHash:    hex.EncodeToString(a.Revision.PreviousHash),
			FrozenAt:        a.Revision.FrozenAt,
			Reason:          a.Revision.Justification,
			EvidenceAddedN:  len(a.Revision.EvidenceRecordIDs),
			AmendedByImport: true,
		})
	}
	httpresp.WriteJSON(w, http.StatusOK, out)
}

func writeImportError(w http.ResponseWriter, r *http.Request, err error) {
	var fpe *historicalimport.FrozenPeriodError
	var ate *historicalimport.AmendTargetError
	var rerr *historicalimport.RecordError
	switch {
	case errors.Is(err, historicalimport.ErrNotAdmin):
		httpresp.WriteJSON(w, http.StatusForbidden, errorBody{Error: "admin credential required", Code: "forbidden"})
	case errors.Is(err, historicalimport.ErrConcurrentFreeze):
		httpresp.WriteJSON(w, http.StatusConflict, errorBody{Error: err.Error(), Code: "concurrent_freeze"})
	case errors.Is(err, historicalimport.ErrInvalid):
		httpresp.WriteJSON(w, http.StatusBadRequest, errorBody{Error: err.Error(), Code: "invalid_request"})
	case errors.As(err, &fpe):
		hits := make([]frozenHitWire, len(fpe.Hits))
		for i, h := range fpe.Hits {
			hits[i] = frozenHitWire{AuditPeriodID: h.PeriodID.String(), Name: h.Name, Records: h.Records}
		}
		httpresp.WriteJSON(w, http.StatusConflict, frozenErrorBody{Error: err.Error(), Code: "frozen_audit_period", FrozenPeriods: hits})
	case errors.As(err, &ate):
		httpresp.WriteJSON(w, http.StatusBadRequest, errorBody{Error: err.Error(), Code: "invalid_amend_frozen"})
	case errors.As(err, &rerr):
		writeBatchError(w, rerr.Index, rerr.Decision, rerr.Err)
	default:
		httperr.WriteInternal(w, r, "historical evidence import", err)
	}
}
