package policies

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/policy"
	"github.com/mgoodric/security-atlas/internal/policy/importfile"
)

// Sync request limits, the import's (a policy space is tens of pages).
const (
	maxSyncBodyBytes = maxImportBodyBytes
	maxSyncPages     = maxImportFiles
	maxSyncSourceLen = 500
)

// syncReq is one full-set push: every labelled page of one source.
type syncReq struct {
	Source syncSourceReq `json:"source"`
	Pages  []syncPageReq `json:"pages"`
}

type syncSourceReq struct {
	Site  string `json:"site"`
	Space string `json:"space"`
}

// syncPageReq is one page. Properties is the page's Page Properties
// table, decoded per page so one bad table skips its page only.
type syncPageReq struct {
	ID         string          `json:"id"`
	Title      string          `json:"title"`
	URL        string          `json:"url"`
	BodyMd     string          `json:"body_md"`
	Properties json.RawMessage `json:"properties"`
}

// syncProperties is the import's front-matter field set without title,
// source_url and linked_controls: the title and URL are the page's own,
// and control links are kept in security-atlas.
type syncProperties struct {
	Version                     string   `json:"version"`
	ApprovedByName              string   `json:"approved_by_name"`
	ApprovedAt                  string   `json:"approved_at"`
	EffectiveDate               string   `json:"effective_date"`
	NextReviewAt                string   `json:"next_review_at"`
	OwnerRole                   string   `json:"owner_role"`
	ApproverRole                string   `json:"approver_role"`
	AcknowledgmentRequiredRoles []string `json:"acknowledgment_required_roles"`
}

type syncResultWire struct {
	PageID   string `json:"page_id"`
	Title    string `json:"title,omitempty"`
	Status   string `json:"status"`
	Reason   string `json:"reason,omitempty"`
	PolicyID string `json:"policy_id,omitempty"`
	Version  string `json:"version,omitempty"`
	// PredecessorID is the superseded policy a new_version result chains
	// to (#2851), or the imported policy a taken_over result superseded
	// as its next version (#2854).
	PredecessorID string `json:"predecessor_id,omitempty"`
	// DriftDetectedAt is when drift on a drift result, or on a policy a
	// taken_over result took over in place, was first seen.
	DriftDetectedAt *time.Time `json:"drift_detected_at,omitempty"`
	// MissingSince is when a run first found a missing result's page
	// absent (#2853).
	MissingSince *time.Time `json:"missing_since,omitempty"`
}

type syncRespWire struct {
	Created    int              `json:"created"`
	NewVersion int              `json:"new_version"`
	TakenOver  int              `json:"taken_over"`
	Unchanged  int              `json:"unchanged"`
	Drift      int              `json:"drift"`
	Skipped    int              `json:"skipped"`
	Missing    int              `json:"missing"`
	Results    []syncResultWire `json:"results"`
}

// SyncPolicies handles POST /v1/policies:sync (#2850): a full-set push of
// policy pages from the Confluence policy connector.
//
// Only a policy-sync API key reaches this handler: the policy-sync gate
// in internal/api authenticates the key for this path and refuses every
// other caller, and the handler checks the purpose again. Each new page
// becomes a published, synced policy, a version bump the next version
// and a body change without one drift. A page's first sync takes over an
// imported policy with its title (#2854). A previously synced page absent
// from the push is reported missing and its policy left in force
// (#2853); see policy.Store.Sync.
//
// Response: 200 with the per-page report. 400 when the request itself is
// malformed (no source, too many pages); a bad page is reported, not
// refused.
func (h *Handler) SyncPolicies(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.tenantCredContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	if !cred.PolicySyncOnly() {
		httpresp.WriteError(w, http.StatusForbidden, "policy sync requires a policy-sync API key")
		return
	}
	var req syncReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSyncBodyBytes)).Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpresp.WriteError(w, http.StatusRequestEntityTooLarge, "sync body exceeds 16 MiB")
			return
		}
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	src := policy.SyncSource{Site: strings.TrimSpace(req.Source.Site), Space: strings.TrimSpace(req.Source.Space)}
	if src.Site == "" || src.Space == "" {
		httpresp.WriteError(w, http.StatusBadRequest, "source.site and source.space are required")
		return
	}
	if len(src.Site) > maxSyncSourceLen || len(src.Space) > maxSyncSourceLen {
		httpresp.WriteError(w, http.StatusBadRequest, "source.site and source.space are at most 500 characters")
		return
	}
	if len(req.Pages) > maxSyncPages {
		httpresp.WriteError(w, http.StatusBadRequest, "too many pages in one sync (max 1000)")
		return
	}

	pages, invalid := parseSyncPages(req.Pages)
	report, err := h.store.Sync(ctx, src, pages, invalid, cred.ID)
	if err != nil {
		httperr.WriteInternal(w, r, "sync policies", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, wireFromSyncReport(report))
}

// parseSyncPages validates every page with the import's rules, splitting
// the pages into inputs for the store and skipped pages to report.
func parseSyncPages(in []syncPageReq) ([]policy.SyncPage, []policy.SyncResult) {
	var pages []policy.SyncPage
	var invalid []policy.SyncResult
	for i, p := range in {
		id := strings.TrimSpace(p.ID)
		title := strings.TrimSpace(p.Title)
		skip := func(reason string) {
			name := id
			if name == "" {
				name = "page " + strconv.Itoa(i+1)
			}
			invalid = append(invalid, policy.SyncResult{PageID: name, Title: title, Status: policy.SyncStatusSkipped, Reason: reason})
		}
		if id == "" {
			skip("missing page id")
			continue
		}
		if strings.TrimSpace(p.URL) == "" {
			skip("missing page url")
			continue
		}
		props, err := decodeSyncProperties(p.Properties)
		if err != nil {
			skip(err.Error())
			continue
		}
		parsed, err := importfile.FromPage(id, importfile.Fields{
			Title:                       title,
			Version:                     props.Version,
			ApprovedByName:              props.ApprovedByName,
			ApprovedAt:                  props.ApprovedAt,
			EffectiveDate:               props.EffectiveDate,
			NextReviewAt:                props.NextReviewAt,
			OwnerRole:                   props.OwnerRole,
			ApproverRole:                props.ApproverRole,
			SourceURL:                   strings.TrimSpace(p.URL),
			AcknowledgmentRequiredRoles: props.AcknowledgmentRequiredRoles,
		}, p.BodyMd)
		if err != nil {
			skip(err.Error())
			continue
		}
		// The audience enum, as the import holds it (#2586).
		if bad := unknownAudiences(parsed.AcknowledgmentRequiredRoles); len(bad) > 0 {
			skip(fmt.Sprintf("acknowledgment_required_roles: unrecognised audience %s; choose from %s",
				strings.Join(bad, ", "), strings.Join(AudienceValues(), ", ")))
			continue
		}
		pages = append(pages, policy.SyncPage{PageID: id, In: parsed})
	}
	return pages, invalid
}

// decodeSyncProperties decodes one page's properties. An unknown key is
// refused, as the import refuses an unknown front-matter key, so a
// misspelt property is reported rather than dropped.
func decodeSyncProperties(raw json.RawMessage) (syncProperties, error) {
	var props syncProperties
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return props, errors.New("missing page properties")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&props); err != nil {
		return props, fmt.Errorf("page properties: %v", err)
	}
	return props, nil
}

func wireFromSyncReport(r policy.SyncReport) syncRespWire {
	out := syncRespWire{Results: make([]syncResultWire, 0, len(r.Results))}
	for _, res := range r.Results {
		rw := syncResultWire{PageID: res.PageID, Title: res.Title, Status: res.Status, Reason: res.Reason}
		if res.Policy != nil {
			rw.PolicyID = res.Policy.ID.String()
			rw.Version = res.Policy.Version
			if (res.Status == policy.SyncStatusNewVersion || res.Status == policy.SyncStatusTakenOver) && res.Policy.PredecessorID != nil {
				rw.PredecessorID = res.Policy.PredecessorID.String()
			}
			if res.Status == policy.SyncStatusDrift || res.Status == policy.SyncStatusTakenOver {
				rw.DriftDetectedAt = res.Policy.SyncDriftDetectedAt
			}
			if res.Status == policy.SyncStatusMissing {
				rw.MissingSince = res.Policy.SyncMissingSince
			}
		}
		switch res.Status {
		case policy.SyncStatusCreated:
			out.Created++
		case policy.SyncStatusNewVersion:
			out.NewVersion++
		case policy.SyncStatusTakenOver:
			out.TakenOver++
		case policy.SyncStatusUnchanged:
			out.Unchanged++
		case policy.SyncStatusDrift:
			out.Drift++
		case policy.SyncStatusSkipped:
			out.Skipped++
		case policy.SyncStatusMissing:
			out.Missing++
		}
		out.Results = append(out.Results, rw)
	}
	return out
}
