package postmortems

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// postmortemsJSON is a fake PagerDuty postmortems response that DELIBERATELY
// includes the dense free-text the over-collection boundary must drop: a
// narrative `body`, a `timeline`, a `root_cause` prose blob, operator-authored
// action-item `title` + `description`, and a customer/PII-bearing summary. The
// client MUST decode ONLY the metadata (id, incident id, status, timestamps,
// action-item completion state) and NONE of the free-text (P0-538). Ids are
// obviously fake; there is NO real-looking PagerDuty token anywhere.
const postmortemsJSON = `{
  "postmortems": [
    {
      "id": "PM-FAKE-1",
      "status": "published",
      "created_at": "2026-05-01T09:00:00Z",
      "published_at": "2026-05-03T12:00:00Z",
      "body": "ROOT CAUSE: customer ACME's SSNs (123-45-6789) were exposed because responder Jane Doe (jane@acme.example, +1-555-0100) misconfigured the export.",
      "narrative": "Long free-text retrospective narrative embedding customer data and responder PII.",
      "timeline": "09:00 Jane paged; 09:05 escalated to Bob (bob@acme.example).",
      "root_cause": "Operator-authored root-cause prose that must never be collected.",
      "notes": "internal notes with PII",
      "incident": {"id": "INC-FAKE-1", "summary": "incident free-text summary that must not be read"},
      "action_items": [
        {"id": "AI1", "title": "Rotate ACME's leaked credentials", "description": "free-text describing the fix", "status": "completed"},
        {"id": "AI2", "title": "Email customer about SSN exposure", "description": "free-text", "status": "open"}
      ]
    }
  ],
  "more": false,
  "limit": 100,
  "offset": 0
}`

func TestClient_ListPostmortems_DropsNarrative(t *testing.T) {
	t.Parallel()
	var gotAuth, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotQuery = r.URL.RawQuery
		if !strings.HasPrefix(r.URL.Path, "/postmortems") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(postmortemsJSON))
	}))
	defer srv.Close()

	c := NewClient(srv.Client(), srv.URL, "obviously-fake-token")
	since := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC)
	got, err := c.ListPostmortems(context.Background(), since, until)
	if err != nil {
		t.Fatalf("ListPostmortems: %v", err)
	}
	if gotAuth != "Token token=obviously-fake-token" {
		t.Errorf("Authorization header = %q", gotAuth)
	}
	for _, want := range []string{"since=", "until=", "limit=", "offset="} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("query %q missing %q", gotQuery, want)
		}
	}
	if len(got) != 1 {
		t.Fatalf("got %d postmortems; want 1", len(got))
	}
	pm := got[0]
	if pm.ID != "PM-FAKE-1" || pm.IncidentID != "INC-FAKE-1" || pm.Status != "published" {
		t.Errorf("metadata = %+v", pm)
	}
	if !pm.CreatedAt.Equal(time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("created_at = %v", pm.CreatedAt)
	}
	if !pm.PublishedAt.Equal(time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("published_at = %v", pm.PublishedAt)
	}
	if len(pm.ActionItems) != 2 {
		t.Fatalf("action items = %d; want 2", len(pm.ActionItems))
	}
	if !pm.ActionItems[0].Completed || pm.ActionItems[1].Completed {
		t.Errorf("action-item completion = %v / %v; want true / false", pm.ActionItems[0].Completed, pm.ActionItems[1].Completed)
	}

	// THE load-bearing assertion: NONE of the narrative free-text the fake
	// response carried can appear anywhere in the rendered record view. We
	// stringify the entire RawPostmortem + its action items (%+v walks every
	// field by reflection) and prove the free-text substrings are absent. The
	// RawPostmortem / RawActionItem types have NO field that could hold them, so
	// this can only pass — but the test fails the build the moment someone adds
	// such a field and decodes into it.
	rendered := fmt.Sprintf("%+v", got)
	for _, leak := range []string{
		"ROOT CAUSE", "SSN", "123-45-6789", "jane@acme.example", "+1-555-0100",
		"Jane Doe", "narrative", "Rotate ACME", "Email customer", "bob@acme.example",
		"incident free-text summary", "internal notes", "retrospective narrative",
	} {
		if strings.Contains(rendered, leak) {
			t.Fatalf("LEAK: narrative/PII substring %q reached the record view: %s", leak, rendered)
		}
	}
}

func TestClient_ListPostmortems_Paginates(t *testing.T) {
	t.Parallel()
	// Two pages: page 0 (offset=0, more=true) then page 1 (offset=100, more=false).
	page := func(offset int, more bool) string {
		return fmt.Sprintf(`{"postmortems":[{"id":"PM-%d","status":"published","created_at":"2026-05-01T09:00:00Z","incident":{"id":"INC-%d"},"action_items":[]}],"more":%t,"limit":100,"offset":%d}`,
			offset, offset, more, offset)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "offset=100") {
			_, _ = w.Write([]byte(page(100, false)))
			return
		}
		_, _ = w.Write([]byte(page(0, true)))
	}))
	defer srv.Close()

	c := NewClient(srv.Client(), srv.URL, "fake")
	got, err := c.ListPostmortems(context.Background(), time.Now().Add(-24*time.Hour), time.Now())
	if err != nil {
		t.Fatalf("ListPostmortems: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d; want 2 across two pages", len(got))
	}
}

func TestClient_ListPostmortems_StopsAtRunCap(t *testing.T) {
	t.Parallel()
	// Every page returns a full page and claims more=true forever; the run cap
	// (MaxRecords) is the stop condition (DoS guard), not the source's honesty.
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var b strings.Builder
		b.WriteString(`{"postmortems":[`)
		for i := 0; i < pageLimit; i++ {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, `{"id":"PM-%d-%d","status":"published","created_at":"2026-05-01T09:00:00Z","incident":{"id":"INC"},"action_items":[]}`, calls, i)
		}
		b.WriteString(`],"more":true,"limit":100,"offset":0}`)
		_, _ = w.Write([]byte(b.String()))
	}))
	defer srv.Close()

	c := NewClient(srv.Client(), srv.URL, "fake")
	got, err := c.ListPostmortems(context.Background(), time.Now().Add(-24*time.Hour), time.Now())
	if err != nil {
		t.Fatalf("ListPostmortems: %v", err)
	}
	if len(got) != MaxRecords {
		t.Fatalf("got %d; want run cap %d", len(got), MaxRecords)
	}
}

func TestParseTime(t *testing.T) {
	t.Parallel()
	if !parseTime("").IsZero() {
		t.Error("empty -> zero")
	}
	if !parseTime("not-a-time").IsZero() {
		t.Error("bad -> zero")
	}
	if parseTime("2026-05-01T09:00:00Z").IsZero() {
		t.Error("good -> non-zero")
	}
}

func TestIsActionItemDone(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"completed", "COMPLETE", "done", "resolved", "closed"} {
		if !isActionItemDone(s) {
			t.Errorf("isActionItemDone(%q) = false; want true", s)
		}
	}
	for _, s := range []string{"open", "in_progress", "", "pending"} {
		if isActionItemDone(s) {
			t.Errorf("isActionItemDone(%q) = true; want false", s)
		}
	}
}
