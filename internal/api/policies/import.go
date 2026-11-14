package policies

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/policy"
	"github.com/mgoodric/security-atlas/internal/policy/importfile"
)

// Import request limits. A policy library is tens of documents; these
// bound a single request without constraining a real one.
const (
	maxImportBodyBytes = 16 << 20 // 16 MiB across all files
	maxImportFiles     = 1000
)

// importFileReq is one policy file: its name (kept for the audit record
// and error reports) and its full content, Markdown with YAML front
// matter (see internal/policy/importfile).
type importFileReq struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

type importReq struct {
	Files   []importFileReq `json:"files"`
	Partial bool            `json:"partial"`
}

type importResultWire struct {
	File   string      `json:"file"`
	Status string      `json:"status"`
	Reason string      `json:"reason,omitempty"`
	Policy *policyWire `json:"policy,omitempty"`
}

type importRespWire struct {
	Committed bool               `json:"committed"`
	Imported  int                `json:"imported"`
	Skipped   int                `json:"skipped"`
	Failed    int                `json:"failed"`
	Results   []importResultWire `json:"results"`
}

// ImportPolicies handles POST /v1/admin/policies/import (#1747). Admin
// only: the import writes published, audit-binding policies without the
// in-tool approval workflow, so the admin gate stands in for it.
//
// Response: 200 with the per-file report when the import committed (some
// files may be skipped, or failed under partial); 422 with the same
// report when a file failed and partial was false, in which case nothing
// was written.
func (h *Handler) ImportPolicies(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.tenantCredContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	if !cred.IsAdmin {
		httpresp.WriteError(w, http.StatusForbidden, "admin role required for policy import")
		return
	}
	var req importReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBodyBytes)).Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpresp.WriteError(w, http.StatusRequestEntityTooLarge, "import body exceeds 16 MiB")
			return
		}
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if len(req.Files) == 0 {
		httpresp.WriteError(w, http.StatusBadRequest, "files must name at least one policy file")
		return
	}
	if len(req.Files) > maxImportFiles {
		httpresp.WriteError(w, http.StatusBadRequest, "too many files in one import (max 1000)")
		return
	}

	// The importer is the user behind the credential when there is one
	// (a JWT session), else the credential id (an API key). Same rule as
	// the policies export meta-audit.
	importer := cred.UserID
	if importer == "" {
		importer = cred.ID
	}
	items, parseFailures := parseImportFiles(req.Files)
	report, err := h.store.Import(ctx, items, parseFailures, importer, req.Partial)
	if err != nil {
		httperr.WriteInternal(w, r, "import policies", err)
		return
	}
	status := http.StatusOK
	if !report.Committed {
		status = http.StatusUnprocessableEntity
	}
	httpresp.WriteJSON(w, status, wireFromImportReport(report))
}

// parseImportFiles parses every file, splitting them into inputs for the
// store and failures to report.
func parseImportFiles(files []importFileReq) ([]policy.ImportInput, []policy.ImportResult) {
	var items []policy.ImportInput
	var failures []policy.ImportResult
	for i, f := range files {
		name := strings.TrimSpace(f.Name)
		if name == "" {
			name = "file " + strconv.Itoa(i+1)
		}
		in, err := importfile.Parse(name, []byte(f.Content))
		if err != nil {
			failures = append(failures, policy.ImportResult{
				SourceFile: name,
				Status:     policy.ImportStatusFailed,
				Reason:     err.Error(),
			})
			continue
		}
		items = append(items, in)
	}
	return items, failures
}

func wireFromImportReport(r policy.ImportReport) importRespWire {
	out := importRespWire{Committed: r.Committed, Results: make([]importResultWire, 0, len(r.Results))}
	for _, res := range r.Results {
		rw := importResultWire{File: res.SourceFile, Status: res.Status, Reason: res.Reason}
		if res.Policy != nil {
			pw := wireFromPolicy(*res.Policy)
			rw.Policy = &pw
		}
		switch res.Status {
		case policy.ImportStatusImported:
			out.Imported++
		case policy.ImportStatusSkipped:
			out.Skipped++
		case policy.ImportStatusFailed:
			out.Failed++
		}
		out.Results = append(out.Results, rw)
	}
	return out
}
