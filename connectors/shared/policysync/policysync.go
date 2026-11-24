// Package policysync is the client side of the platform's policy sync
// endpoint, POST /v1/policies:sync (#2850), shared by the policy
// connectors (Confluence #2856, SharePoint #2837): the request and
// report wire shapes, the push, and the report lines every connector
// prints the same way.
//
// What a connector reads from its source and how it turns a document
// into a page stays in the connector. This package only carries the
// contract, which no connector may change on its own.
package policysync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Source names the set a push covers: one space (or library) on one
// site.
type Source struct {
	Site  string `json:"site"`
	Space string `json:"space"`
}

// PagePayload is one page in the push. Properties is nil (JSON null) for
// a page with no properties, which the platform reports as "missing page
// properties". Unreadable marks a page whose body the connector could not
// read (#3000); it carries UnreadableReason and no body or properties.
type PagePayload struct {
	ID               string         `json:"id"`
	Title            string         `json:"title"`
	URL              string         `json:"url"`
	BodyMd           string         `json:"body_md"`
	Properties       map[string]any `json:"properties"`
	Unreadable       bool           `json:"unreadable,omitempty"`
	UnreadableReason string         `json:"unreadable_reason,omitempty"`
}

// Request is the body of POST /v1/policies:sync.
type Request struct {
	Source Source        `json:"source"`
	Pages  []PagePayload `json:"pages"`
}

// Readable counts the pages of a push that carry a body.
func (r Request) Readable() int {
	n := 0
	for _, p := range r.Pages {
		if !p.Unreadable {
			n++
		}
	}
	return n
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
	Unreadable int      `json:"unreadable"`
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
		// policies (#2902). A connector refuses an empty set before it
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

// WriteResults writes one line per platform result, each starting with
// noun ("page", "document") and the result's id and title.
func WriteResults(w io.Writer, noun string, rep Report) {
	for _, r := range rep.Results {
		line := fmt.Sprintf("%s %s %q: %s", noun, r.PageID, r.Title, r.Status)
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
}

// WriteTotals writes the platform's warnings, then the totals line.
func WriteTotals(w io.Writer, rep Report) {
	for _, warn := range rep.Warnings {
		_, _ = fmt.Fprintf(w, "warning: %s\n", warn)
	}
	_, _ = fmt.Fprintf(w, "sync: created=%d new_version=%d taken_over=%d unchanged=%d drift=%d skipped=%d missing=%d unreadable=%d\n",
		rep.Created, rep.NewVersion, rep.TakenOver, rep.Unchanged, rep.Drift, rep.Skipped, rep.Missing, rep.Unreadable)
}
