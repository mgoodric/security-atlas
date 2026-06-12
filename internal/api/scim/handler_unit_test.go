// Pure-Go unit tests for the slice-508 SCIM HTTP handler helpers + the
// auth/credential-missing deny branches (no Postgres, no build tag). Covers
// the active-presence decode (the SCIM "omitted = enabled" gotcha) and the
// 401 path when no SCIM credential is on context.
package scim

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/scim"
)

func TestInboundUser_ActiveOrDefault(t *testing.T) {
	t.Parallel()
	tru := true
	fls := false
	cases := []struct {
		name string
		in   inboundUser
		want bool
	}{
		{"omitted defaults enabled", inboundUser{Active: nil}, true},
		{"explicit true", inboundUser{Active: &tru}, true},
		{"explicit false (deprovision)", inboundUser{Active: &fls}, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.in.activeOrDefault(); got != tc.want {
				t.Errorf("activeOrDefault = %v; want %v", got, tc.want)
			}
		})
	}
}

func TestInboundUser_ActivePresenceDecode(t *testing.T) {
	t.Parallel()
	// A JSON body with no `active` key → nil → default enabled.
	var omitted inboundUser
	if err := json.Unmarshal([]byte(`{"userName":"a@b.io"}`), &omitted); err != nil {
		t.Fatal(err)
	}
	if !omitted.activeOrDefault() {
		t.Error("omitted active should default to enabled")
	}
	// A JSON body with active:false → explicit deprovision.
	var explicit inboundUser
	if err := json.Unmarshal([]byte(`{"userName":"a@b.io","active":false}`), &explicit); err != nil {
		t.Fatal(err)
	}
	if explicit.activeOrDefault() {
		t.Error("explicit active:false should be false")
	}
}

func TestInboundUser_ResolveDisplayName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   inboundUser
		want string
	}{
		{"top-level displayName wins", inboundUser{UserName: "u", DisplayName: "D", Name: &scim.Name{Formatted: "F"}}, "D"},
		{"falls back to name.formatted", inboundUser{UserName: "u", Name: &scim.Name{Formatted: "F"}}, "F"},
		{"falls back to userName", inboundUser{UserName: "u"}, "u"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.in.resolveDisplayName(); got != tc.want {
				t.Errorf("resolveDisplayName = %q; want %q", got, tc.want)
			}
		})
	}
}

// TestSCIMPagination pins the RFC 7644 §3.4.2.4 clamp: count is bounded to
// [1, maxCount], startIndex to [1, maxStartIndex], and the derived offset is
// non-negative — so the downstream int32 narrowing in the store cannot
// overflow (CodeQL go/incorrect-integer-conversion regression guard).
func TestSCIMPagination(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name           string
		startIndex     string
		count          string
		wantStartIndex int
		wantCount      int
		wantOffset     int
	}{
		{"defaults", "", "", 1, defaultCount, 0},
		{"negative count → default", "1", "-5", 1, defaultCount, 0},
		{"huge count → maxCount", "1", "100000", 1, maxCount, 0},
		{"negative startIndex → 1", "-3", "10", 1, 10, 0},
		{"zero startIndex → 1", "0", "10", 1, 10, 0},
		{"normal page two", "11", "10", 11, 10, 10},
		{"huge startIndex → maxStartIndex", "999999999", "10", maxStartIndex, 10, maxStartIndex - 1},
		{"garbage → defaults", "abc", "xyz", 1, defaultCount, 0},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			si, c, off := scimPagination(tc.startIndex, tc.count)
			if si != tc.wantStartIndex || c != tc.wantCount || off != tc.wantOffset {
				t.Errorf("scimPagination(%q,%q) = (%d,%d,%d); want (%d,%d,%d)",
					tc.startIndex, tc.count, si, c, off, tc.wantStartIndex, tc.wantCount, tc.wantOffset)
			}
			if off < 0 {
				t.Errorf("offset must be non-negative, got %d", off)
			}
		})
	}
}

func TestParsePositiveInt(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		def  int
		want int
	}{
		{"", 1, 1},
		{"0", 1, 1},
		{"-3", 5, 5},
		{"abc", 7, 7},
		{"42", 1, 42},
	}
	for _, tc := range cases {
		if got := parsePositiveInt(tc.in, tc.def); got != tc.want {
			t.Errorf("parsePositiveInt(%q,%d) = %d; want %d", tc.in, tc.def, got, tc.want)
		}
	}
}

// TestHandlers_RejectMissingCredential proves every mutating handler returns
// 401 when no SCIM credential is on context (defense-in-depth: the middleware
// is the primary gate, but a handler reached without a credential must fail
// closed, never operate on an empty tenant).
func TestHandlers_RejectMissingCredential(t *testing.T) {
	t.Parallel()
	// A nil store is safe: the credential check fires before any store call.
	h := NewHandler(nil)
	cases := []struct {
		name   string
		method string
		fn     http.HandlerFunc
	}{
		{"create", http.MethodPost, h.CreateUser},
		{"get", http.MethodGet, h.GetUser},
		{"list", http.MethodGet, h.ListUsers},
		{"replace", http.MethodPut, h.ReplaceUser},
		{"patch", http.MethodPatch, h.PatchUser},
		{"delete", http.MethodDelete, h.DeleteUser},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(tc.method, "/scim/v2/Users", nil)
			rec := httptest.NewRecorder()
			tc.fn(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d; want 401", rec.Code)
			}
		})
	}
}

// TestMiddleware_NoBearer returns 401 with the SCIM error shape when no bearer
// is presented (the auth gate's primary deny branch — no DB needed).
func TestMiddleware_NoBearer(t *testing.T) {
	t.Parallel()
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	mw := Middleware(stubAuth{}) // never reached — no bearer
	req := httptest.NewRequest(http.MethodGet, "/scim/v2/Users", nil)
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)
	if called {
		t.Error("next should not be called without a bearer")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); got == "" {
		t.Error("missing WWW-Authenticate header")
	}
}

// stubAuth always errors — proves the bearer-extraction guard short-circuits
// before any Authenticate call.
type stubAuth struct{}

func (stubAuth) Authenticate(ctx context.Context, token string) (scim.Credential, error) {
	return scim.Credential{}, scim.ErrUnknownCredential
}
