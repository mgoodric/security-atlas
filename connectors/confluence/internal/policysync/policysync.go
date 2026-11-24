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
// pushed as unreadable, with the converter's error as its reason and no
// body or properties (#3000). The platform counts it present, so its
// policy is not marked missing, and changes nothing else.
package policysync

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/mgoodric/security-atlas/connectors/confluence/internal/confluenceapi"
	"github.com/mgoodric/security-atlas/connectors/confluence/internal/storagemd"
	shared "github.com/mgoodric/security-atlas/connectors/shared/policysync"
)

// The wire shapes and the push are the shared policy-sync client's
// (connectors/shared/policysync), which the SharePoint connector uses too
// (#2837).
type (
	Source      = shared.Source
	PagePayload = shared.PagePayload
	Request     = shared.Request
	Result      = shared.Result
	Report      = shared.Report
	PushError   = shared.PushError
)

// SyncPath is the platform's policy sync endpoint.
const SyncPath = shared.SyncPath

// PageNote is what the connector itself found on a page: macros the
// converter could not render, or a body it refused.
type PageNote struct {
	PageID      string
	Title       string
	Unsupported []storagemd.Unsupported
	// ConvertErr is set when the body could not be converted; the page
	// was pushed as unreadable.
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
			req.Pages = append(req.Pages, PagePayload{
				ID: p.ID, Title: p.Title, URL: p.URL,
				Unreadable: true, UnreadableReason: unreadableReason(err),
			})
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

// unreadableReason is an unreadable page's reason: the converter's error.
func unreadableReason(err error) string {
	return "body not converted: " + err.Error()
}

// Push sends the full set in one request; see shared.Push.
func Push(ctx context.Context, hc *http.Client, platform *url.URL, key string, req Request) (Report, error) {
	return shared.Push(ctx, hc, platform, key, req)
}

// ConverterSkipped counts the pages whose body could not be converted,
// which the connector pushed as unreadable.
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
	shared.WriteResults(w, "page", rep)
	for _, n := range notes {
		if n.ConvertErr != nil {
			// The platform's own line for the page, unreadable with this
			// reason, is printed above.
			continue
		}
		parts := make([]string, 0, len(n.Unsupported))
		for _, u := range n.Unsupported {
			parts = append(parts, fmt.Sprintf("%s x%d", u.Name, u.Count))
		}
		sort.Strings(parts)
		_, _ = fmt.Fprintf(w, "page %s %q: unsupported macros: %s\n", n.PageID, n.Title, strings.Join(parts, ", "))
	}
	shared.WriteTotals(w, rep)
}
