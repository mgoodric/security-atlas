// Package docsync turns the documents of a SharePoint library into one
// full-set push to the platform's policy sync endpoint (#2837), on the
// contract the Confluence connector uses (#2850), and renders the report.
//
// Build converts each document: its library columns become the page's
// properties and its .docx body becomes Markdown. A document with a
// problem (a missing or invalid column) is still pushed, so the platform,
// which owns the validation rules, reports it. A document whose body
// cannot be converted (not a .docx, too large, nested too deeply,
// unreadable) is pushed as unreadable, with the reason and no body or
// properties (#3000): the platform counts it present and changes nothing
// else.
package docsync

import (
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mgoodric/security-atlas/connectors/shared/policysync"
	"github.com/mgoodric/security-atlas/connectors/sharepoint/internal/docxmd"
	"github.com/mgoodric/security-atlas/connectors/sharepoint/internal/graph"
)

// Column names, compared without case, spaces or a trailing colon, and
// the sync property each one sets. A column may also use the property's
// own name (version, approved_by_name, ...). SharePoint's own "Version"
// column (internal name _UIVersionString) is the file's edit count, not
// the policy's version, so the policy version column is "Policy version".
var propertyColumns = map[string]string{
	"policyversion":                 "version",
	"version":                       "version",
	"approvedby":                    "approved_by_name",
	"approvaldate":                  "approved_at",
	"effectivedate":                 "effective_date",
	"nextreviewdate":                "next_review_at",
	"ownerrole":                     "owner_role",
	"approverrole":                  "approver_role",
	"acknowledgmentaudience":        "acknowledgment_required_roles",
	"acknowledgementaudience":       "acknowledgment_required_roles",
	"approved_by_name":              "approved_by_name",
	"approved_at":                   "approved_at",
	"effective_date":                "effective_date",
	"next_review_at":                "next_review_at",
	"owner_role":                    "owner_role",
	"approver_role":                 "approver_role",
	"acknowledgment_required_roles": "acknowledgment_required_roles",
}

// dateProperties take a date. A SharePoint date column's value is an
// instant (midnight in the site's time zone, as UTC), so it is read back
// as a date in the run's --time-zone.
var dateProperties = map[string]bool{"approved_at": true, "effective_date": true, "next_review_at": true}

// listProperty is the one property that takes a list.
const listProperty = "acknowledgment_required_roles"

// encodedChar is SharePoint's escape for a character not allowed in a
// column's internal name, e.g. "Approved_x0020_by" for "Approved by".
var encodedChar = regexp.MustCompile(`_x([0-9A-Fa-f]{4})_`)

// columnKey is a column name as matched against propertyColumns: decoded,
// lower case, without spaces or a trailing colon. Underscores are kept, so
// a column named as the property (approved_by_name) matches as itself.
func columnKey(name string) string {
	name = encodedChar.ReplaceAllStringFunc(name, func(m string) string {
		// Four hex digits: at most 0xFFFF, so 16 bits bounds the rune.
		r, err := strconv.ParseUint(m[2:6], 16, 16)
		if err != nil {
			return m
		}
		return string(rune(r))
	})
	name = strings.TrimSuffix(strings.TrimSpace(name), ":")
	return strings.ToLower(strings.Join(strings.Fields(name), ""))
}

// Properties maps a document's column values to the sync properties.
// Columns that are not policy properties (SharePoint's own, and any
// other) are ignored: a library always carries system columns, so an
// unknown column cannot be refused the way an unknown Confluence
// property is. An empty value is left out, so an empty required column
// is reported as missing. A value of a type no property takes (a person
// or lookup column) is left out and named in skipped.
func Properties(fields map[string]any, loc *time.Location) (props map[string]any, skipped []string) {
	props = map[string]any{}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, col := range keys {
		prop, ok := propertyColumns[columnKey(col)]
		if !ok {
			continue
		}
		if _, dup := props[prop]; dup {
			continue
		}
		v := fields[col]
		if prop == listProperty {
			roles, ok := roleList(v)
			if !ok {
				skipped = append(skipped, col)
				continue
			}
			if len(roles) > 0 {
				props[prop] = roles
			}
			continue
		}
		s, ok := scalar(v)
		if !ok {
			skipped = append(skipped, col)
			continue
		}
		if s == "" {
			continue
		}
		if dateProperties[prop] {
			s = localDate(s, loc)
		}
		props[prop] = s
	}
	return props, skipped
}

// scalar is a column value as text: a string, a number (in its shortest
// form, so 2 is "2" and 1.5 is "1.5") or null. Anything else (a person,
// lookup or multi-value column) is not one.
func scalar(v any) (string, bool) {
	switch t := v.(type) {
	case nil:
		return "", true
	case string:
		return strings.TrimSpace(t), true
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), true
	default:
		return "", false
	}
}

// roleList is the audience column's value: a multi-choice column's list,
// or text with one role per line or comma.
func roleList(v any) ([]string, bool) {
	var raw []string
	switch t := v.(type) {
	case nil:
		return nil, true
	case string:
		raw = strings.FieldsFunc(t, func(c rune) bool { return c == ',' || c == '\n' || c == ';' })
	case []any:
		for _, e := range t {
			s, ok := e.(string)
			if !ok {
				return nil, false
			}
			raw = append(raw, s)
		}
	default:
		return nil, false
	}
	var out []string
	for _, r := range raw {
		if r = strings.TrimSpace(r); r != "" {
			out = append(out, r)
		}
	}
	return out, true
}

// localDate turns a date column's instant into a YYYY-MM-DD date in loc.
// Any other text (already a date, or something the platform will refuse)
// is passed through for the platform to validate.
func localDate(s string, loc *time.Location) string {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	return t.In(loc).Format(time.DateOnly)
}

// Note is what the connector itself found on a document: content the
// converter could not render, columns it could not read, or a body it
// could not convert.
type Note struct {
	ID          string
	Title       string
	Unsupported []docxmd.Unsupported
	// SkippedColumns are columns of a type no property takes.
	SkippedColumns []string
	// ConvertErr is set when the body could not be converted; the
	// document was pushed as unreadable.
	ConvertErr error
}

// Title is a document's policy title: its Title column, else its file
// name without the extension.
func Title(d graph.Document) string {
	if t, ok := d.Fields["Title"].(string); ok && strings.TrimSpace(t) != "" {
		return strings.TrimSpace(t)
	}
	return strings.TrimSuffix(d.Name, path.Ext(d.Name))
}

// Build converts the documents into the push and the connector's own
// notes. The documents keep their input order.
func Build(src policysync.Source, docs []graph.Document, loc *time.Location) (policysync.Request, []Note) {
	req := policysync.Request{Source: src, Pages: make([]policysync.PagePayload, 0, len(docs))}
	var notes []Note
	for _, d := range docs {
		title := Title(d)
		md, unsupported, err := convert(d)
		if err != nil {
			notes = append(notes, Note{ID: d.ID, Title: title, ConvertErr: err})
			req.Pages = append(req.Pages, policysync.PagePayload{
				ID: d.ID, Title: title, URL: d.WebURL,
				Unreadable: true, UnreadableReason: "body not converted: " + err.Error(),
			})
			continue
		}
		props, skipped := Properties(d.Fields, loc)
		if len(unsupported) > 0 || len(skipped) > 0 {
			notes = append(notes, Note{ID: d.ID, Title: title, Unsupported: unsupported, SkippedColumns: skipped})
		}
		req.Pages = append(req.Pages, policysync.PagePayload{ID: d.ID, Title: title, URL: d.WebURL, BodyMd: md, Properties: props})
	}
	return req, notes
}

// errNotDocx is the reason for a file that is not a Word document.
type errNotDocx struct{ name string }

func (e errNotDocx) Error() string {
	ext := path.Ext(e.name)
	if ext == "" {
		ext = "no extension"
	}
	return fmt.Sprintf("not a .docx document (%s); only Word .docx files convert", ext)
}

func convert(d graph.Document) (string, []docxmd.Unsupported, error) {
	if !d.IsDocx() {
		return "", nil, errNotDocx{name: d.Name}
	}
	res, err := docxmd.Convert(d.Content)
	if err != nil {
		return "", nil, err
	}
	return res.Markdown, res.Unsupported, nil
}

// ConverterSkipped counts the documents whose body could not be
// converted, which the connector pushed as unreadable.
func ConverterSkipped(notes []Note) int {
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
func WriteReport(w io.Writer, rep policysync.Report, notes []Note) {
	policysync.WriteResults(w, "document", rep)
	for _, n := range notes {
		if n.ConvertErr != nil {
			// The platform's own line for the document, unreadable with
			// this reason, is printed above.
			continue
		}
		if len(n.Unsupported) > 0 {
			parts := make([]string, 0, len(n.Unsupported))
			for _, u := range n.Unsupported {
				parts = append(parts, fmt.Sprintf("%s x%d", u.Name, u.Count))
			}
			_, _ = fmt.Fprintf(w, "document %s %q: unsupported content: %s\n", n.ID, n.Title, strings.Join(parts, ", "))
		}
		if len(n.SkippedColumns) > 0 {
			_, _ = fmt.Fprintf(w, "document %s %q: columns not read (person, lookup or other unsupported type): %s\n", n.ID, n.Title, strings.Join(n.SkippedColumns, ", "))
		}
	}
	policysync.WriteTotals(w, rep)
}
