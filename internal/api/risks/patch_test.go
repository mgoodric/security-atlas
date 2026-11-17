package risks

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// TestCategoryRefs covers the ?category= forms of #1812: repeated
// parameters, comma-separated values, and both, with blanks dropped.
func TestCategoryRefs(t *testing.T) {
	t.Parallel()
	got := categoryRefs([]string{"privacy, regulatory", "", "operational,"})
	want := []string{"privacy", "regulatory", "operational"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v, want %v", got, want)
	}
	if categoryRefs(nil) != nil {
		t.Fatal("no values: want nil (no filter)")
	}
}

// TestDecodePatch covers the PATCH /v1/risks/{id} body rules (#1749): an
// omitted org_unit_id keeps the unit, an explicit null unbinds it, and any
// field other than level / org_unit_id / category is refused.
func TestDecodePatch(t *testing.T) {
	t.Parallel()
	ou := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	cases := []struct {
		name       string
		body       string
		wantMsg    bool
		wantLevel  dbx.RiskLevel
		wantSet    bool
		wantUnitID *uuid.UUID
		wantCat    string
	}{
		{name: "level only", body: `{"level":"org"}`, wantLevel: dbx.RiskLevelOrg},
		{name: "unit only", body: `{"org_unit_id":"` + ou.String() + `"}`, wantSet: true, wantUnitID: &ou},
		{name: "explicit null unbinds", body: `{"org_unit_id":null}`, wantSet: true},
		{name: "both", body: `{"level":"team","org_unit_id":"` + ou.String() + `"}`, wantLevel: dbx.RiskLevelTeam, wantSet: true, wantUnitID: &ou},
		{name: "category only", body: `{"category":"privacy"}`, wantCat: "privacy"},
		{name: "category with level", body: `{"category":"privacy","level":"org"}`, wantLevel: dbx.RiskLevelOrg, wantCat: "privacy"},
		{name: "empty category", body: `{"category":""}`, wantMsg: true},
		{name: "empty body", body: `{}`, wantMsg: true},
		{name: "empty level", body: `{"level":""}`, wantMsg: true},
		{name: "unknown field", body: `{"nickname":"x"}`, wantMsg: true},
		{name: "record field alone", body: `{"title":"x"}`},
		{name: "null inherent score", body: `{"inherent_score":null}`, wantMsg: true},
		{name: "null review due", body: `{"review_due_at":null}`, wantMsg: true},
		{name: "date-only review due", body: `{"review_due_at":"2027-01-01"}`, wantMsg: true},
		{name: "non-string title", body: `{"title":7}`, wantMsg: true},
		{name: "bad unit", body: `{"org_unit_id":"nope"}`, wantMsg: true},
		{name: "non-string unit", body: `{"org_unit_id":7}`, wantMsg: true},
		{name: "not json", body: `level=org`, wantMsg: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in, msg := decodePatch([]byte(tc.body))
			if tc.wantMsg {
				if msg == "" {
					t.Fatalf("want a 400 message, got none (in=%+v)", in)
				}
				return
			}
			if msg != "" {
				t.Fatalf("unexpected message: %s", msg)
			}
			if in.Level != tc.wantLevel || in.OrgUnitSet != tc.wantSet || in.CategoryRef != tc.wantCat {
				t.Fatalf("in = %+v", in)
			}
			if (in.OrgUnitID == nil) != (tc.wantUnitID == nil) ||
				(in.OrgUnitID != nil && *in.OrgUnitID != *tc.wantUnitID) {
				t.Fatalf("org_unit_id = %v, want %v", in.OrgUnitID, tc.wantUnitID)
			}
		})
	}
}

// TestDecodePatchRecordFields covers #2104: each record field decodes, and
// every treatment or acceptance field is refused with a message naming it.
func TestDecodePatchRecordFields(t *testing.T) {
	t.Parallel()
	in, msg := decodePatch([]byte(`{
		"title":"T","description":"D","methodology":"fair",
		"inherent_score":{"lef":1,"lm":2},"treatment_owner":"O",
		"review_due_at":"2027-03-01T00:00:00Z","instrument_reference":"I"}`))
	if msg != "" {
		t.Fatalf("unexpected message: %s", msg)
	}
	f := in.Fields
	if *f.Title != "T" || *f.Description != "D" || *f.Methodology != "fair" ||
		*f.TreatmentOwner != "O" || *f.InstrumentReference != "I" ||
		string(f.InherentScore) != `{"lef":1,"lm":2}` ||
		f.ReviewDueAt.Format("2006-01-02") != "2027-03-01" {
		t.Fatalf("fields = %+v", f)
	}

	for _, field := range patchRefusedFields {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			body := `{"title":"T","` + field + `":"x"}`
			_, msg := decodePatch([]byte(body))
			if !strings.HasPrefix(msg, field+" cannot be changed") {
				t.Fatalf("msg = %q, want it to name %s", msg, field)
			}
		})
	}
}

// patchBeforeStore sends a PATCH with a credentialed tenant context to a
// handler with no store: a response written before the store is reached
// comes back; reaching the store would panic.
func patchBeforeStore(t *testing.T, body, ifMatch string) *httptest.ResponseRecorder {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), "11111111-1111-1111-1111-111111111111")
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	ctx = authctx.WithCredential(ctx, credstore.Credential{ID: "key_abc"})
	r := httptest.NewRequest(http.MethodPatch, "/v1/risks/"+uuid.NewString(),
		strings.NewReader(body)).WithContext(ctx)
	if ifMatch != "" {
		r.Header.Set("If-Match", ifMatch)
	}
	w := httptest.NewRecorder()
	router := chi.NewRouter()
	router.Patch("/v1/risks/{id}", New(nil).PatchRisk)
	router.ServeHTTP(w, r)
	return w
}

// TestPatchRisk_OversizedBodyIs413 covers #2311: a body past maxPatchBytes
// is refused with 413 before it is decoded, and one just under the cap is
// read (and refused as malformed, a 400, since it is not JSON).
func TestPatchRisk_OversizedBodyIs413(t *testing.T) {
	t.Parallel()
	title := strings.Repeat("x", maxPatchBytes)
	w := patchBeforeStore(t, `{"title":"`+title+`"}`, "")
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, want 413: %.200s", w.Code, w.Body.String())
	}
	w = patchBeforeStore(t, strings.Repeat(" ", maxPatchBytes-2)+"[]", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("at the cap: status %d, want 400: %.200s", w.Code, w.Body.String())
	}
}

// TestPatchRisk_MalformedIfMatchIs400 covers #2311: an If-Match that is
// not one quoted timestamp is refused before the store is reached.
func TestPatchRisk_MalformedIfMatchIs400(t *testing.T) {
	t.Parallel()
	for _, h := range []string{`2026-11-17T00:00:00Z`, `"v3"`, `"a", "b"`, `"`} {
		w := patchBeforeStore(t, `{"title":"T"}`, h)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "If-Match") {
			t.Fatalf("If-Match %s: status %d: %s", h, w.Code, w.Body.String())
		}
	}
}

// TestParseIfMatch covers the accepted If-Match forms (#2311): absent and
// "*" are unconditional; a quoted RFC 3339 time, strong or weak, in any
// zone, round-trips riskETag.
func TestParseIfMatch(t *testing.T) {
	t.Parallel()
	for _, h := range []string{"", "*", " * "} {
		if got, msg := parseIfMatch(h); got != nil || msg != "" {
			t.Fatalf("%q: got %v, %q; want unconditional", h, got, msg)
		}
	}
	at := time.Date(2026, 11, 17, 19, 11, 33, 123456000, time.UTC)
	for _, h := range []string{
		riskETag(at),
		"W/" + riskETag(at),
		`"2026-11-17T11:11:33.123456-08:00"`,
	} {
		got, msg := parseIfMatch(h)
		if msg != "" || got == nil || !got.Equal(at) {
			t.Fatalf("%s: got %v, %q; want %v", h, got, msg, at)
		}
	}
}

// TestParseAuditLimit covers ?limit= on the audit log (#2311): default 200,
// at most 500, and anything else a 400 rather than a clamp.
func TestParseAuditLimit(t *testing.T) {
	t.Parallel()
	ok := map[string]int{"": defaultAuditLimit, "1": 1, "500": maxAuditLimit}
	for v, want := range ok {
		if got, msg := parseAuditLimit(v); got != want || msg != "" {
			t.Fatalf("limit=%q: got %d, %q; want %d", v, got, msg, want)
		}
	}
	for _, v := range []string{"0", "-1", "501", "abc", "1.5"} {
		if _, msg := parseAuditLimit(v); msg == "" {
			t.Fatalf("limit=%q: want a 400 message", v)
		}
	}
}
