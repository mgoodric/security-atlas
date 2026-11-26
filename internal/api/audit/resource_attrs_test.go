package audit

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/audit"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// fakeLookup answers from fixed maps; err, when set, fails every lookup
// the way a database outage would.
type fakeLookup struct {
	populations map[uuid.UUID]uuid.UUID
	samples     map[uuid.UUID]uuid.UUID
	evidence    map[uuid.UUID]audit.EvidenceScope
	controls    map[uuid.UUID][]uuid.UUID
	// window is the population window the controls' periods cover; a list
	// asking outside it gets no period, as the store answers.
	window [2]time.Time
	err    error
}

func (f fakeLookup) PopulationPeriod(_ context.Context, id uuid.UUID) (uuid.UUID, bool, error) {
	p, ok := f.populations[id]
	return p, ok, f.err
}

func (f fakeLookup) SamplePeriod(_ context.Context, id uuid.UUID) (uuid.UUID, bool, error) {
	p, ok := f.samples[id]
	return p, ok, f.err
}

func (f fakeLookup) EvidencePeriods(_ context.Context, id uuid.UUID) (audit.EvidenceScope, bool, error) {
	e, ok := f.evidence[id]
	return e, ok, f.err
}

func (f fakeLookup) ControlPeriods(_ context.Context, id uuid.UUID, since, until time.Time) ([]uuid.UUID, error) {
	if since.Before(f.window[0]) || until.After(f.window[1]) {
		return nil, f.err
	}
	return f.controls[id], f.err
}

func attrsRequest(t *testing.T, method, target, body string, withTenant bool) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	if withTenant {
		ctx, err := tenancy.WithTenant(r.Context(), uuid.NewString())
		if err != nil {
			t.Fatalf("WithTenant: %v", err)
		}
		r = r.WithContext(ctx)
	}
	return r
}

func TestResourceAttrs2157_Routes(t *testing.T) {
	t.Parallel()
	period, other := uuid.New(), uuid.New()
	pop, unattached, sample, rec, orphanRec, control, cell := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	store := fakeLookup{
		populations: map[uuid.UUID]uuid.UUID{pop: period},
		samples:     map[uuid.UUID]uuid.UUID{sample: period},
		evidence: map[uuid.UUID]audit.EvidenceScope{
			rec:       {Periods: []uuid.UUID{other, period}, ScopeCellID: &cell},
			orphanRec: {},
		},
		controls: map[uuid.UUID][]uuid.UUID{control: {period}},
		window: [2]time.Time{
			time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			time.Date(2026, 3, 31, 23, 59, 59, 0, time.UTC),
		},
	}
	list := "/v1/evidence?control_id=" + control.String()
	inWindow := "&since=2026-01-01T00:00:00Z&until=2026-03-31T23:59:59Z"
	one := map[string]any{"audit_period_id": period.String()}
	cases := []struct {
		name, method, target, body string
		want                       map[string]any
	}{
		{"population", "GET", "/v1/populations/" + pop.String(), "", one},
		{"unattached population", "GET", "/v1/populations/" + unattached.String(), "", nil},
		{"population, bad id", "GET", "/v1/populations/nope", "", nil},
		{"population create", "POST", "/v1/populations", `{"control_id":"` + control.String() + `"}`, nil},
		{"draw sample", "POST", "/v1/samples", `{"population_id":"` + pop.String() + `","n":1,"seed":"s"}`, one},
		{"draw sample, unattached population", "POST", "/v1/samples", `{"population_id":"` + unattached.String() + `"}`, nil},
		{"draw sample, malformed body", "POST", "/v1/samples", `{`, nil},
		{"sample", "GET", "/v1/samples/" + sample.String(), "", one},
		{"sample annotations read", "GET", "/v1/samples/" + sample.String() + "/annotations", "", one},
		{"sample annotate", "POST", "/v1/samples/" + sample.String() + "/annotations", `{}`, one},
		{"unknown sample", "GET", "/v1/samples/" + uuid.NewString(), "", nil},
		{"evidence record", "GET", "/v1/evidence/" + rec.String(), "", map[string]any{
			"audit_period_ids": []any{other.String(), period.String()},
			"scope_cell_id":    cell.String(),
		}},
		{"evidence record in no period", "GET", "/v1/evidence/" + orphanRec.String(), "", map[string]any{"audit_period_ids": []any{}}},
		{"unknown evidence record", "GET", "/v1/evidence/" + uuid.NewString(), "", nil},
		{"freshness", "GET", "/v1/evidence/freshness", "", nil},
		{"receipt", "GET", "/v1/evidence/receipts/" + rec.String(), "", nil},
		{"evidence list for a control", "GET", list + inWindow, "", map[string]any{"audit_period_ids": []any{period.String()}}},
		{"evidence list, offset bounds", "GET", list + "&since=2026-01-01T02:00:00%2B02:00&until=2026-02-01T00:00:00Z", "", map[string]any{"audit_period_ids": []any{period.String()}}},
		{"evidence list, cell ignored", "GET", list + inWindow + "&scope_cell_id=" + cell.String(), "", map[string]any{"audit_period_ids": []any{period.String()}}},
		{"evidence list, outside the window", "GET", list + "&since=2025-01-01T00:00:00Z&until=2026-03-31T23:59:59Z", "", map[string]any{"audit_period_ids": []any{}}},
		{"evidence list, no bounds", "GET", list, "", nil},
		{"evidence list, no since", "GET", list + "&until=2026-03-31T23:59:59Z", "", nil},
		{"evidence list, no until", "GET", list + "&since=2026-01-01T00:00:00Z", "", nil},
		{"evidence list, malformed since", "GET", list + "&since=2026-01-01&until=2026-03-31T23:59:59Z", "", nil},
		{"evidence list, no control", "GET", "/v1/evidence?scope_cell_id=" + cell.String(), "", nil},
		{"evidence list, bad control", "GET", "/v1/evidence?control_id=nope", "", nil},
		{"evidence push", "POST", "/v1/evidence:push", `{}`, nil},
		{"unrelated route", "GET", "/v1/controls/" + control.String(), "", nil},
	}
	hydrate := ResourceAttrs(store)
	for _, tc := range cases {
		r := attrsRequest(t, tc.method, tc.target, tc.body, true)
		got, err := hydrate(r)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: attrs = %#v; want %#v", tc.name, got, tc.want)
		}
		// The handler still reads the whole body.
		if b, _ := io.ReadAll(r.Body); string(b) != tc.body {
			t.Errorf("%s: handler body = %q; want %q", tc.name, b, tc.body)
		}
	}
}

func TestResourceAttrs2157_FailsClosed(t *testing.T) {
	t.Parallel()
	pop := uuid.New()
	t.Run("no tenant: no lookup, no attrs", func(t *testing.T) {
		t.Parallel()
		got, err := ResourceAttrs(fakeLookup{populations: map[uuid.UUID]uuid.UUID{pop: uuid.New()}})(
			attrsRequest(t, "GET", "/v1/populations/"+pop.String(), "", false))
		if err != nil || got != nil {
			t.Fatalf("attrs = %v, err = %v; want nil, nil", got, err)
		}
	})
	t.Run("lookup error is returned", func(t *testing.T) {
		t.Parallel()
		outage := errors.New("db down")
		for _, target := range []string{
			"/v1/populations/" + pop.String(),
			"/v1/samples/" + pop.String(),
			"/v1/evidence/" + pop.String(),
			"/v1/evidence?control_id=" + pop.String() + "&since=2026-01-01T00:00:00Z&until=2026-01-02T00:00:00Z",
		} {
			_, err := ResourceAttrs(fakeLookup{err: outage})(attrsRequest(t, "GET", target, "", true))
			if !errors.Is(err, outage) {
				t.Errorf("GET %s: err = %v; want the lookup error", target, err)
			}
		}
	})
}
