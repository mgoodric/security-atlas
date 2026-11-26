package policysync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func serve(t *testing.T, h http.HandlerFunc) *url.URL {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL + "/base/")
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestPushSendsKeyAndDecodesReport(t *testing.T) {
	t.Parallel()
	var got Request
	u := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/base"+SyncPath {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer k1" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"created":1,"results":[{"page_id":"7","status":"created"}],"warnings":["w"]}`))
	})
	req := Request{Source: Source{Site: "s", Space: "p"}, Pages: []PagePayload{{ID: "7", Title: "T"}, {ID: "8", Unreadable: true, UnreadableReason: "r"}}}
	rep, err := Push(context.Background(), http.DefaultClient, u, "k1", req)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Created != 1 || len(rep.Results) != 1 || rep.Warnings[0] != "w" {
		t.Fatalf("report = %+v", rep)
	}
	if got.Source.Space != "p" || len(got.Pages) != 2 || !got.Pages[1].Unreadable {
		t.Fatalf("pushed = %+v", got)
	}
	if req.Readable() != 1 {
		t.Fatalf("Readable = %d, want 1", req.Readable())
	}
}

func TestPushErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"json error", http.StatusForbidden, `{"error":"policy sync requires a policy-sync API key"}`, "push sync: HTTP 403: policy sync requires a policy-sync API key"},
		{"text error", http.StatusBadGateway, "bad gateway", "push sync: HTTP 502: bad gateway"},
		{"empty refused", http.StatusUnprocessableEntity, `{"error":"empty"}`, "platform refused an empty push (HTTP 422): empty"},
		{"bad report", http.StatusOK, "not json", "decode sync report"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			u := serve(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			_, err := Push(context.Background(), http.DefaultClient, u, "k", Request{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			var pe *PushError
			if tc.status != http.StatusOK && (!errors.As(err, &pe) || pe.Status != tc.status) {
				t.Fatalf("err = %#v, want *PushError %d", err, tc.status)
			}
		})
	}
}

func TestPushTransportError(t *testing.T) {
	t.Parallel()
	u, _ := url.Parse("http://127.0.0.1:1")
	if _, err := Push(context.Background(), http.DefaultClient, u, "k", Request{}); err == nil || !strings.HasPrefix(err.Error(), "push sync: ") {
		t.Fatalf("err = %v", err)
	}
	if _, err := Push(nilCtx(), http.DefaultClient, u, "k", Request{}); err == nil || !strings.Contains(err.Error(), "build sync request") {
		t.Fatalf("nil ctx err = %v", err)
	}
	bad := Request{Pages: []PagePayload{{Properties: map[string]any{"x": func() {}}}}}
	if _, err := Push(context.Background(), http.DefaultClient, u, "k", bad); err == nil || !strings.Contains(err.Error(), "encode sync request") {
		t.Fatalf("unencodable err = %v", err)
	}
}

// nilCtx makes http.NewRequestWithContext fail.
func nilCtx() context.Context { return nil } //nolint:staticcheck // the nil context is the point

func TestWriteResultsAndTotals(t *testing.T) {
	t.Parallel()
	rep := Report{
		Created: 1, Skipped: 1,
		Results: []Result{
			{PageID: "12", Title: "Access", Status: "new_version", Version: "2.0", PolicyID: "p", PredecessorID: "q"},
			{PageID: "13", Title: "Vendor", Status: "skipped", Reason: "missing"},
		},
		Warnings: []string{"half missing"},
	}
	var b bytes.Buffer
	WriteResults(&b, "document", rep)
	WriteTotals(&b, rep)
	want := `document 12 "Access": new_version version=2.0 policy=p predecessor=q
document 13 "Vendor": skipped reason=missing
warning: half missing
sync: created=1 new_version=0 taken_over=0 unchanged=0 drift=0 skipped=1 missing=0 unreadable=0
`
	if b.String() != want {
		t.Fatalf("report =\n%s\nwant\n%s", b.String(), want)
	}
}
