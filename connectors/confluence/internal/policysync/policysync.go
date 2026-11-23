// Package policysync turns labelled Confluence pages into one full-set
// push to the platform's policy sync endpoint, POST /v1/policies:sync
// (#2850, #2856), and renders the per-page report.
//
// Build converts each page: its Page Properties table becomes the
// page's properties and its body becomes Markdown. A page with a problem
// is still pushed (missing properties, a misspelt key, an empty body) so
// the platform, which owns the validation rules, reports it. The one
// exception is a body the converter refuses (too large, nested too
// deeply, unparseable): there is no Markdown to send, so the page is
// reported here as skipped and left out of the push.
package policysync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/mgoodric/security-atlas/connectors/confluence/internal/confluenceapi"
	"github.com/mgoodric/security-atlas/connectors/confluence/internal/storagemd"
)

// Source names the set a push covers: one space on one site.
type Source struct {
	Site  string `json:"site"`
	Space string `json:"space"`
}

// PagePayload is one page in the push. Properties is nil (JSON null) for
// a page with no Page Properties macro, which the platform reports as
// "missing page properties".
type PagePayload struct {
	ID         string         `json:"id"`
	Title      string         `json:"title"`
	URL        string         `json:"url"`
	BodyMd     string         `json:"body_md"`
	Properties map[string]any `json:"properties"`
}

// Request is the body of POST /v1/policies:sync.
type Request struct {
	Source Source        `json:"source"`
	Pages  []PagePayload `json:"pages"`
}

// PageNote is what the connector itself found on a page: macros the
// converter could not render, or a body it refused.
type PageNote struct {
	PageID      string
	Title       string
	Unsupported []storagemd.Unsupported
	// ConvertErr is set when the body could not be converted; the page
	// was left out of the push.
	ConvertErr error
}

// Labels in the Page Properties table, compared without case and with
// whitespace collapsed, and the sync property each one sets. A row may
// also use the property's own name (version, approved_by_name, ...).
var propertyLabels = map[string]string{
	"version":                       "version",
	"approved by":                   "approved_by_name",
	"approval date":                 "approved_at",
	"effective date":                "effective_date",
	"next review date":              "next_review_at",
	"owner role":                    "owner_role",
	"approver role":                 "approver_role",
	"acknowledgment audience":       "acknowledgment_required_roles",
	"acknowledgement audience":      "acknowledgment_required_roles",
	"approved_by_name":              "approved_by_name",
	"approved_at":                   "approved_at",
	"effective_date":                "effective_date",
	"next_review_at":                "next_review_at",
	"owner_role":                    "owner_role",
	"approver_role":                 "approver_role",
	"acknowledgment_required_roles": "acknowledgment_required_roles",
}

// listProperty is the one property that takes a list: one audience per
// line, or separated by commas.
const listProperty = "acknowledgment_required_roles"

// Properties maps a page's Page Properties rows to the sync properties.
// A row whose label is not recognised is passed through under its label,
// so the platform refuses it as an unknown property and reports the page
// instead of silently ignoring the row. An empty value is left out, so
// an empty optional row means "not set" and an empty required row is
// reported as missing. When a label repeats, the first row counts.
func Properties(rows []storagemd.Property) map[string]any {
	out := map[string]any{}
	for _, r := range rows {
		label := strings.ToLower(strings.Join(strings.Fields(strings.TrimSuffix(strings.TrimSpace(r.Key), ":")), " "))
		key, ok := propertyLabels[label]
		if !ok {
			key = strings.TrimSpace(r.Key)
		}
		if _, dup := out[key]; dup {
			continue
		}
		if key == listProperty {
			var roles []string
			for _, f := range strings.FieldsFunc(r.Value, func(c rune) bool { return c == ',' || c == '\n' }) {
				if f = strings.TrimSpace(f); f != "" {
					roles = append(roles, f)
				}
			}
			if len(roles) > 0 {
				out[key] = roles
			}
			continue
		}
		if v := strings.TrimSpace(r.Value); v != "" {
			out[key] = v
		}
	}
	return out
}

// Build converts the pages into the push and the connector's own notes.
// The pages keep their input order.
func Build(src Source, pages []confluenceapi.Page) (Request, []PageNote) {
	req := Request{Source: src, Pages: make([]PagePayload, 0, len(pages))}
	var notes []PageNote
	for _, p := range pages {
		conv, err := storagemd.ConvertPage(p.Storage)
		if err != nil {
			notes = append(notes, PageNote{PageID: p.ID, Title: p.Title, ConvertErr: err})
			continue
		}
		if len(conv.Unsupported) > 0 {
			notes = append(notes, PageNote{PageID: p.ID, Title: p.Title, Unsupported: conv.Unsupported})
		}
		pp := PagePayload{ID: p.ID, Title: p.Title, URL: p.URL, BodyMd: conv.Markdown}
		if conv.HasProperties {
			pp.Properties = Properties(conv.Properties)
		}
		req.Pages = append(req.Pages, pp)
	}
	return req, notes
}

// Result is one line of the platform's report.
type Result struct {
	PageID        string `json:"page_id"`
	Title         string `json:"title,omitempty"`
	Status        string `json:"status"`
	Reason        string `json:"reason,omitempty"`
	PolicyID      string `json:"policy_id,omitempty"`
	Version       string `json:"version,omitempty"`
	PredecessorID string `json:"predecessor_id,omitempty"`
}

// Report is the platform's response to a sync.
type Report struct {
	Created    int      `json:"created"`
	NewVersion int      `json:"new_version"`
	TakenOver  int      `json:"taken_over"`
	Unchanged  int      `json:"unchanged"`
	Drift      int      `json:"drift"`
	Skipped    int      `json:"skipped"`
	Missing    int      `json:"missing"`
	Results    []Result `json:"results"`
	// Warnings are run-level notes from the platform, such as a run that
	// leaves more than half of the source's policies missing (#2902).
	Warnings []string `json:"warnings"`
}

// SyncPath is the platform's policy sync endpoint.
const SyncPath = "/v1/policies:sync"

// maxErrorBody is how much of an error response is quoted in the error.
const maxErrorBody = 1024

// PushError is a push the platform answered with anything but 200.
// Message is the platform's {"error": ...} text when the body carries
// one, else the start of the body.
type PushError struct {
	Status  int
	Message string
}

func (e *PushError) Error() string {
	if e.Status == http.StatusUnprocessableEntity {
		// The platform refuses an empty push for a source that has synced
		// policies (#2902). The connector refuses an empty set before it
		// pushes, so reaching this means the two disagree.
		return fmt.Sprintf("push sync: platform refused an empty push (HTTP 422): %s", e.Message)
	}
	return fmt.Sprintf("push sync: HTTP %d: %s", e.Status, e.Message)
}

// Push sends the full set in one request, authenticated with the
// policy-sync key, and returns the report. Any answer but 200 is a
// *PushError.
func Push(ctx context.Context, hc *http.Client, platform *url.URL, key string, req Request) (Report, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return Report{}, fmt.Errorf("encode sync request: %w", err)
	}
	u := *platform
	u.Path = strings.TrimRight(u.Path, "/") + SyncPath
	u.RawPath = ""
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return Report{}, fmt.Errorf("build sync request: %w", err)
	}
	hr.Header.Set("Authorization", "Bearer "+key)
	hr.Header.Set("Content-Type", "application/json")
	hr.Header.Set("Accept", "application/json")
	resp, err := hc.Do(hr)
	if err != nil {
		return Report{}, fmt.Errorf("push sync: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		msg := strings.TrimSpace(string(snippet))
		var body struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(snippet, &body) == nil && strings.TrimSpace(body.Error) != "" {
			msg = strings.TrimSpace(body.Error)
		}
		return Report{}, &PushError{Status: resp.StatusCode, Message: msg}
	}
	var rep Report
	if err := json.NewDecoder(resp.Body).Decode(&rep); err != nil {
		return Report{}, fmt.Errorf("decode sync report: %w", err)
	}
	return rep, nil
}

// ConverterSkipped counts the pages the connector left out of the push
// because their body could not be converted.
func ConverterSkipped(notes []PageNote) int {
	n := 0
	for _, note := range notes {
		if note.ConvertErr != nil {
			n++
		}
	}
	return n
}

// WriteReport writes the run's report: one line per platform result,
// then the connector's own notes, then the platform's warnings, then the
// totals.
func WriteReport(w io.Writer, rep Report, notes []PageNote) {
	for _, r := range rep.Results {
		line := fmt.Sprintf("page %s %q: %s", r.PageID, r.Title, r.Status)
		if r.Version != "" {
			line += " version=" + r.Version
		}
		if r.PolicyID != "" {
			line += " policy=" + r.PolicyID
		}
		if r.PredecessorID != "" {
			line += " predecessor=" + r.PredecessorID
		}
		if r.Reason != "" {
			line += " reason=" + r.Reason
		}
		_, _ = fmt.Fprintln(w, line)
	}
	for _, n := range notes {
		if n.ConvertErr != nil {
			_, _ = fmt.Fprintf(w, "page %s %q: skipped by connector reason=body not converted: %v\n", n.PageID, n.Title, n.ConvertErr)
			continue
		}
		parts := make([]string, 0, len(n.Unsupported))
		for _, u := range n.Unsupported {
			parts = append(parts, fmt.Sprintf("%s x%d", u.Name, u.Count))
		}
		sort.Strings(parts)
		_, _ = fmt.Fprintf(w, "page %s %q: unsupported macros: %s\n", n.PageID, n.Title, strings.Join(parts, ", "))
	}
	for _, warn := range rep.Warnings {
		_, _ = fmt.Fprintf(w, "warning: %s\n", warn)
	}
	_, _ = fmt.Fprintf(w, "sync: created=%d new_version=%d taken_over=%d unchanged=%d drift=%d skipped=%d missing=%d skipped_by_connector=%d\n",
		rep.Created, rep.NewVersion, rep.TakenOver, rep.Unchanged, rep.Drift, rep.Skipped, rep.Missing, ConverterSkipped(notes))
}
