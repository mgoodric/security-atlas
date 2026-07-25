package metrics

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestAggregateOnly_StructuralGuard is the load-bearing over-collection guard
// (P0-539 / threat-model I — DOMINANT). It walks every field of every
// connector-side record type by reflection and FAILS THE BUILD if any field
// name suggests it could hold a RESPONDER IDENTITY (a named individual's name,
// email, phone, id-as-person, or any free-text). The aggregation altitude is
// structural: if the struct physically can't hold a responder identity, an
// individual-performance surveillance store can't form. This test is the
// tripwire that keeps it that way — the moment a future change adds an
// `Acknowledger`/`Responder`/`AssigneeName`/`UserEmail` field to capture "who
// was slow", this test goes red.
//
// Mirrors the slice-538 postmortems metadata-only structural guard; here the
// banned set centers on responder-identity tokens because the metrics surface's
// over-collection risk is profiling named individuals.
func TestAggregateOnly_StructuralGuard(t *testing.T) {
	t.Parallel()

	// Field-name substrings that signal a responder identity / free-text slot.
	banned := []string{
		"responder", "acknowledger", "assignee", "assigner", "resolver",
		"user", "person", "agent", "operator", "engineer", "member",
		"name", "email", "phone", "contact", "login", "handle", "username",
		"body", "title", "description", "summary", "note", "text", "comment",
		"message", "author", "reporter", "customer", "narrative",
	}

	// The exhaustive set of connector-side record types that flow toward a
	// pushed evidence record. Each must be aggregate / identity-free.
	types := []reflect.Type{
		reflect.TypeOf(RawAck{}),
		reflect.TypeOf(RawTiming{}),
		reflect.TypeOf(ServiceMetrics{}),
	}

	timeType := reflect.TypeOf(time.Time{})

	var walk func(prefix string, rt reflect.Type)
	walk = func(prefix string, rt reflect.Type) {
		switch rt.Kind() {
		case reflect.Slice, reflect.Array, reflect.Pointer:
			walk(prefix, rt.Elem())
			return
		case reflect.Struct:
			if rt == timeType {
				return // time.Time is a metadata timestamp, not an identity.
			}
			for i := 0; i < rt.NumField(); i++ {
				f := rt.Field(i)
				lname := strings.ToLower(f.Name)
				for _, b := range banned {
					if strings.Contains(lname, b) {
						t.Errorf("%s.%s: field name contains banned responder-identity/free-text token %q — metrics records must be SERVICE-level aggregates, never per-named-responder (P0-539). If you think you need it, you do NOT; reconsider the aggregation altitude.", prefix, f.Name, b)
					}
				}
				// A bare string field that survives the name check is still a
				// risk surface: the ONLY allowed string is the opaque service id
				// (the aggregation grain). Any NEW string field must be justified
				// here explicitly.
				if f.Type.Kind() == reflect.String {
					allowed := map[string]bool{"ServiceID": true}
					if !allowed[f.Name] {
						t.Errorf("%s.%s: unexpected string field — only the opaque ServiceID (aggregation grain) is allowed on a metrics record (P0-539). A responder identity is a string; do NOT add one.", prefix, f.Name)
					}
				}
				walk(prefix+"."+f.Name, f.Type)
			}
		default:
			// int/bool/etc. cannot hold a responder identity.
		}
	}

	for _, rt := range types {
		walk(rt.Name(), rt)
	}
}

// TestCollect_DropsResponderIdentity is the behavioral companion to the
// structural guard: it feeds a real HTTP response that DOES carry named
// acknowledgers (and incident titles, assignees, responder emails) and proves
// NO per-named-responder identity becomes the grain of any emitted metrics
// record. Mirrors the slice-489-family TestNormalize_DropsSecretBearingSettings
// shape: source contains the sensitive field; the connector data does not.
func TestCollect_DropsResponderIdentity(t *testing.T) {
	t.Parallel()

	// A PagerDuty incidents payload WITH responder identities embedded in
	// every place the API would put them: the acknowledgment's `acknowledger`,
	// the `assignments`, the incident `title`, and a responder `email`.
	body := `{
      "incidents": [
        {
          "id": "INC1",
          "title": "Prod outage caused by Jane Doe's deploy",
          "created_at": "2026-06-01T00:00:00Z",
          "resolved_at": "2026-06-01T00:20:00Z",
          "service": {"id": "SVCA", "summary": "Payments"},
          "acknowledgments": [
            {"at": "2026-06-01T00:02:00Z",
             "acknowledger": {"id": "PUSER1", "type": "user_reference", "summary": "Jane Doe", "email": "jane.doe@example.com"}}
          ],
          "assignments": [
            {"assignee": {"id": "PUSER1", "summary": "Jane Doe", "email": "jane.doe@example.com"}}
          ],
          "last_status_change_by": {"id": "PUSER1", "summary": "Jane Doe"}
        }
      ],
      "more": false
    }`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := NewClient(srv.Client(), srv.URL, "test-pd-token")
	raw, err := c.ListIncidentTimings(context.Background(), ts("2026-05-01T00:00:00Z"), ts("2026-06-02T00:00:00Z"))
	if err != nil {
		t.Fatalf("list timings: %v", err)
	}

	// 1. The decoded raw timing carries the SERVICE grain + timing, never an
	//    identity. Re-encode every raw record and assert no identity token
	//    survived the decode boundary.
	assertNoIdentityTokens(t, "raw", raw)
	if len(raw) != 1 || raw[0].ServiceID != "SVCA" {
		t.Fatalf("want one SVCA timing; got %+v", raw)
	}

	// 2. Aggregating yields a SERVICE-grained record — the grain is "SVCA",
	//    never "PUSER1" / "Jane Doe" / an email.
	agg, err := Collect(context.Background(), staticAPI{raw}, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(agg) != 1 {
		t.Fatalf("want one service aggregate; got %d", len(agg))
	}
	if agg[0].ServiceID != "SVCA" {
		t.Errorf("aggregate grain = %q; want the SERVICE id SVCA, never a responder", agg[0].ServiceID)
	}
	if agg[0].MTTASecondsMean != 120 || agg[0].MTTRSecondsMean != 1200 {
		t.Errorf("aggregate timings wrong: %+v", agg[0])
	}
	assertNoIdentityTokens(t, "aggregate", agg)
}

type staticAPI struct{ out []RawTiming }

func (s staticAPI) ListIncidentTimings(_ context.Context, _, _ time.Time) ([]RawTiming, error) {
	return s.out, nil
}

// assertNoIdentityTokens JSON-encodes v and fails if any responder-identity
// token leaked through. This catches an identity that hid in a field the
// structural guard's name-check might miss (e.g. a generic map), proving the
// drop is real end-to-end.
func assertNoIdentityTokens(t *testing.T, label string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("%s: marshal: %v", label, err)
	}
	blob := strings.ToLower(string(b))
	for _, tok := range []string{"jane", "doe", "puser1", "jane.doe@example.com", "acknowledger", "assignee", "outage"} {
		if strings.Contains(blob, strings.ToLower(tok)) {
			t.Errorf("%s: responder-identity / free-text token %q leaked into connector data: %s", label, tok, blob)
		}
	}
}
