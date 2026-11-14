package auditnotes

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/audit/period"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// fakePeriods resolves only the ids in known; err, when set, fails every
// lookup the way a database outage would.
type fakePeriods struct {
	known map[uuid.UUID]bool
	err   error
	calls int
}

func (f *fakePeriods) Get(_ context.Context, id uuid.UUID) (period.Period, error) {
	f.calls++
	if f.err != nil {
		return period.Period{}, f.err
	}
	if !f.known[id] {
		return period.Period{}, period.ErrNotFound
	}
	return period.Period{ID: id}, nil
}

func tenantRequest(t *testing.T, method, path, body string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	ctx, err := tenancy.WithTenant(r.Context(), uuid.NewString())
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return r.WithContext(ctx)
}

func TestPeriodResourceAttrs(t *testing.T) {
	t.Parallel()
	known := uuid.New()
	unknown := uuid.New()
	valid := `{"audit_period_id":"` + known.String() + `","scope_type":"period","body":"x"}`

	cases := []struct {
		name      string
		method    string
		path      string
		body      string
		noTenant  bool
		want      map[string]any
		wantCalls int
	}{
		{name: "period in the caller's tenant", method: http.MethodPost, path: "/v1/audit-notes", body: valid,
			want: map[string]any{"audit_period_id": known.String()}, wantCalls: 1},
		{name: "unknown or other-tenant period", method: http.MethodPost, path: "/v1/audit-notes",
			body: `{"audit_period_id":"` + unknown.String() + `"}`, wantCalls: 1},
		{name: "non-uuid period", method: http.MethodPost, path: "/v1/audit-notes", body: `{"audit_period_id":"q1"}`},
		{name: "malformed json", method: http.MethodPost, path: "/v1/audit-notes", body: `{"audit_period_id":`},
		{name: "no tenant on the request", method: http.MethodPost, path: "/v1/audit-notes", body: valid, noTenant: true},
		{name: "read of audit notes", method: http.MethodGet, path: "/v1/audit-notes", body: valid},
		{name: "another route", method: http.MethodPost, path: "/v1/risks", body: valid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			periods := &fakePeriods{known: map[uuid.UUID]bool{known: true}}
			r := tenantRequest(t, tc.method, tc.path, tc.body)
			if tc.noTenant {
				r = httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			}
			got, err := PeriodResourceAttrs(periods)(r)
			if err != nil {
				t.Fatalf("hydrate: %v", err)
			}
			if len(got) != len(tc.want) || got["audit_period_id"] != tc.want["audit_period_id"] {
				t.Errorf("attrs = %v; want %v", got, tc.want)
			}
			if periods.calls != tc.wantCalls {
				t.Errorf("period lookups = %d; want %d", periods.calls, tc.wantCalls)
			}
			// The handler still reads the whole body.
			rest, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read body after hydrate: %v", err)
			}
			if string(rest) != tc.body {
				t.Errorf("body after hydrate = %q; want %q", rest, tc.body)
			}
		})
	}
}

func TestPeriodResourceAttrs_LookupFailureIsAnError(t *testing.T) {
	t.Parallel()
	known := uuid.New()
	periods := &fakePeriods{err: errors.New("connection refused")}
	r := tenantRequest(t, http.MethodPost, "/v1/audit-notes", `{"audit_period_id":"`+known.String()+`"}`)
	if _, err := PeriodResourceAttrs(periods)(r); err == nil {
		t.Fatal("hydrate with a failing lookup: err = nil; want an error")
	}
}

func TestPeriodResourceAttrs_BodyLongerThanPeekIsReplayedWhole(t *testing.T) {
	t.Parallel()
	known := uuid.New()
	body := `{"audit_period_id":"` + known.String() + `","body":"` + strings.Repeat("a", maxPeriodPeekBytes) + `"}`
	r := tenantRequest(t, http.MethodPost, "/v1/audit-notes", body)
	got, err := PeriodResourceAttrs(&fakePeriods{known: map[uuid.UUID]bool{known: true}})(r)
	if err != nil {
		t.Fatalf("hydrate: %v", err)
	}
	if got != nil {
		t.Errorf("attrs for a body past the peek limit = %v; want none", got)
	}
	rest, _ := io.ReadAll(r.Body)
	if string(rest) != body {
		t.Errorf("replayed body has %d bytes; want %d", len(rest), len(body))
	}
}
