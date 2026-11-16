package moduleaccess

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestNormalize(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   []string
		want []string
		err  error
	}{
		{"nil is all", nil, nil, nil},
		{"empty is core only", []string{}, []string{}, nil},
		{"registry order, deduplicated", []string{"questionnaires", "policies", "questionnaires"}, []string{"policies", "questionnaires"}, nil},
		{"core module refused", []string{"controls"}, nil, ErrUnknownModule},
		{"unknown key refused", []string{"policies", "nope"}, nil, ErrUnknownModule},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := Normalize(c.in)
			if !errors.Is(err, c.err) {
				t.Fatalf("err = %v; want %v", err, c.err)
			}
			if c.err == nil && (!reflect.DeepEqual(got, c.want) || (got == nil) != (c.want == nil)) {
				t.Fatalf("Normalize(%v) = %#v; want %#v", c.in, got, c.want)
			}
		})
	}
}

func TestAccessHas(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		access Access
		key    string
		want   bool
	}{
		{"all", Access{}, "risk", true},
		{"in set", Access{Modules: []string{"risk"}}, "risk", true},
		{"outside set", Access{Modules: []string{"risk"}}, "policies", false},
		{"empty set", Access{Modules: []string{}}, "risk", false},
		{"exempt ignores the set", Access{Exempt: true, Modules: []string{}}, "risk", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := c.access.Has(c.key); got != c.want {
				t.Fatalf("Has(%q) = %v; want %v", c.key, got, c.want)
			}
		})
	}
}

// A core route never reaches the store: the gate falls through without a
// credential lookup, so a nil store is never touched.
func TestGate_CoreRouteFallsThrough(t *testing.T) {
	t.Parallel()
	reached := false
	h := Gate(nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	for _, p := range []string{"/v1/controls", "/v1/evidence", "/v1/me/modules", "/health"} {
		reached = false
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, p, nil))
		if !reached {
			t.Fatalf("GET %s did not reach the handler", p)
		}
	}
}
