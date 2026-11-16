// Pure-Go unit tests for GET /v1/people (#2085). No Postgres.

package people

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestDisplayName(t *testing.T) {
	t.Parallel()
	cases := []struct{ dn, email, want string }{
		{"Alice Admin", "alice@example.com", "Alice Admin"},
		{"  Alice  ", "alice@example.com", "Alice"},
		{"", "alice@example.com", "alice"},
		{"   ", " bob@example.com ", "bob"},
		{"", "no-at-sign", "no-at-sign"},
		{"", "", ""},
	}
	for _, c := range cases {
		if got := DisplayName(c.dn, c.email); got != c.want {
			t.Errorf("DisplayName(%q, %q) = %q, want %q", c.dn, c.email, got, c.want)
		}
	}
}

// The wire shape carries no email, token, prefix or last4 field.
func TestResponseFields(t *testing.T) {
	t.Parallel()
	b, err := json.Marshal(Response{
		Users:   []User{{ID: "u", DisplayName: "d", Status: "active"}},
		APIKeys: []APIKey{{ID: "k", OwnerID: "u"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string][]map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	keys := func(m map[string]any) []string {
		out := []string{}
		for _, k := range []string{"id", "display_name", "status", "owner_id", "email", "token", "bearer_token", "last4", "prefix"} {
			if _, ok := m[k]; ok {
				out = append(out, k)
			}
		}
		return out
	}
	if k := keys(got["users"][0]); !reflect.DeepEqual(k, []string{"id", "display_name", "status"}) {
		t.Errorf("user fields = %v", k)
	}
	if k := keys(got["api_keys"][0]); !reflect.DeepEqual(k, []string{"id", "owner_id"}) {
		t.Errorf("api key fields = %v", k)
	}
}

func TestListRequiresCredential(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	New(nil).List(rec, httptest.NewRequest(http.MethodGet, "/v1/people", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "missing credential") {
		t.Errorf("body = %s", rec.Body.String())
	}
}
