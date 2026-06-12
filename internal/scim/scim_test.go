// Pure-Go unit tests for the slice-508 SCIM marshaling + filter/PatchOp
// parsing (slice-353 Q-2 fast loop: no Postgres, no build tag). The
// no-role-escalation proof lives here at the unit tier (P0-508-3): planPatch
// must DROP any roles/groups op and surface only {active, displayName}.
package scim

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestClampInt32 pins the pagination-narrowing guard (CodeQL
// go/incorrect-integer-conversion): a negative clamps to 0, a value above
// math.MaxInt32 clamps to math.MaxInt32, so the int32() conversion in
// Store.List can never overflow.
func TestClampInt32(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   int
		want int32
	}{
		{-1, 0},
		{-1000, 0},
		{0, 0},
		{100, 100},
		{math.MaxInt32, math.MaxInt32},
		{math.MaxInt32 + 1, math.MaxInt32},
	}
	for _, tc := range cases {
		if got := clampInt32(tc.in); got != tc.want {
			t.Errorf("clampInt32(%d) = %d; want %d", tc.in, got, tc.want)
		}
	}
}

func TestParseUserNameFilter(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		filter   string
		wantVal  string
		wantPres bool
		wantErr  bool
	}{
		{"empty is no-filter", "", "", false, false},
		{"basic eq", `userName eq "alice@example.com"`, "alice@example.com", true, false},
		{"case-insensitive attr", `USERNAME EQ "bob@x.io"`, "bob@x.io", true, false},
		{"extra whitespace", `  userName    eq   "c@d.io"  `, "c@d.io", true, false},
		{"wrong attribute rejected", `displayName eq "x"`, "", false, true},
		{"wrong operator rejected", `userName co "x"`, "", false, true},
		{"compound filter rejected", `userName eq "a" and active eq "true"`, "", false, true},
		{"no quotes rejected", `userName eq alice`, "", false, true},
		{"missing value rejected", `userName eq`, "", false, true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			val, pres, err := ParseUserNameFilter(tc.filter)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got val=%q pres=%v", tc.filter, val, pres)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if val != tc.wantVal || pres != tc.wantPres {
				t.Errorf("got (%q,%v); want (%q,%v)", val, pres, tc.wantVal, tc.wantPres)
			}
		})
	}
}

// TestPlanPatch_NoRoleEscalation is the P0-508-3 unit proof: a PatchOp that
// tries to set roles (by path or in a no-path value object) must NOT produce
// any intent — roles are silently dropped, and the active/displayName
// allow-list is the only thing honored.
func TestPlanPatch_NoRoleEscalation(t *testing.T) {
	t.Parallel()

	mk := func(op, path, value string) PatchOperation {
		return PatchOperation{Op: op, Path: path, Value: json.RawMessage(value)}
	}

	t.Run("roles path is dropped", func(t *testing.T) {
		t.Parallel()
		intent, err := planPatch([]PatchOperation{
			mk("replace", "roles", `["admin"]`),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if intent.setActive || intent.setDisplayName {
			t.Fatalf("roles op leaked into intent: %+v", intent)
		}
	})

	t.Run("no-path object with roles drops roles keeps active", func(t *testing.T) {
		t.Parallel()
		intent, err := planPatch([]PatchOperation{
			mk("replace", "", `{"roles":["admin"],"active":false,"displayName":"X"}`),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !intent.setActive || intent.active != false {
			t.Errorf("active not honored: %+v", intent)
		}
		if !intent.setDisplayName || intent.displayName != "X" {
			t.Errorf("displayName not honored: %+v", intent)
		}
		// roles must not have produced any side-channel — there is no roles
		// field on patchIntent, so the only proof is that the op did not error
		// and active/displayName came through cleanly. (The store never reads
		// roles.)
	})

	t.Run("urn-qualified roles path dropped", func(t *testing.T) {
		t.Parallel()
		intent, err := planPatch([]PatchOperation{
			mk("add", "urn:ietf:params:scim:schemas:core:2.0:User:roles", `["super-admin"]`),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if intent.setActive || intent.setDisplayName {
			t.Fatalf("urn roles op leaked: %+v", intent)
		}
	})
}

func TestPlanPatch_ActiveFlip(t *testing.T) {
	t.Parallel()
	mk := func(path, value string) PatchOperation {
		return PatchOperation{Op: "replace", Path: path, Value: json.RawMessage(value)}
	}
	cases := []struct {
		name       string
		op         PatchOperation
		wantSet    bool
		wantActive bool
	}{
		{"bare bool false (deprovision)", mk("active", `false`), true, false},
		{"bare bool true (reprovision)", mk("active", `true`), true, true},
		{"stringified false", mk("active", `"false"`), true, false},
		{"case-insensitive path", mk("Active", `false`), true, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			intent, err := planPatch([]PatchOperation{tc.op})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if intent.setActive != tc.wantSet || intent.active != tc.wantActive {
				t.Errorf("got set=%v active=%v; want set=%v active=%v", intent.setActive, intent.active, tc.wantSet, tc.wantActive)
			}
		})
	}
}

func TestPlanPatch_InvalidActiveValue(t *testing.T) {
	t.Parallel()
	_, err := planPatch([]PatchOperation{
		{Op: "replace", Path: "active", Value: json.RawMessage(`123`)},
	})
	if err == nil {
		t.Fatal("expected error for non-bool active value")
	}
}

func TestDecodeBool(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw     string
		wantVal bool
		wantOk  bool
	}{
		{`true`, true, true},
		{`false`, false, true},
		{`"true"`, true, true},
		{`"FALSE"`, false, true},
		{`123`, false, false},
		{`"maybe"`, false, false},
		{``, false, false},
	}
	for _, tc := range cases {
		v, ok := decodeBool(json.RawMessage(tc.raw))
		if v != tc.wantVal || ok != tc.wantOk {
			t.Errorf("decodeBool(%q) = (%v,%v); want (%v,%v)", tc.raw, v, ok, tc.wantVal, tc.wantOk)
		}
	}
}

func TestNormalizePath(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"active":                          "active",
		"  displayName ":                  "displayname",
		"urn:ietf:params:...:User:active": "active",
		"":                                "",
	}
	for in, want := range cases {
		if got := normalizePath(in); got != want {
			t.Errorf("normalizePath(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestWireUser_NoRolesEmitted(t *testing.T) {
	t.Parallel()
	u := DomainUser{
		ID:          uuid.New(),
		Email:       "alice@example.com",
		DisplayName: "Alice",
		ExternalID:  "ext-123",
		Active:      true,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	wire := WireUser(u, "https://host/scim/v2/Users")
	b, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(b)
	// The SCIM wire shape must never carry a role/group attribute (P0-508-3).
	for _, banned := range []string{`"roles"`, `"groups"`} {
		if strings.Contains(js, banned) {
			t.Errorf("wire User leaked %s: %s", banned, js)
		}
	}
	if wire.UserName != u.Email {
		t.Errorf("userName = %q; want %q", wire.UserName, u.Email)
	}
	if !wire.Active {
		t.Error("active should be true")
	}
	if wire.Meta == nil || !strings.HasSuffix(wire.Meta.Location, u.ID.String()) {
		t.Errorf("meta.location malformed: %+v", wire.Meta)
	}
}

func TestNewError_WireShape(t *testing.T) {
	t.Parallel()
	e := NewError(404, "", "not found")
	if e.Status != "404" {
		t.Errorf("status = %q; want 404", e.Status)
	}
	if len(e.Schemas) != 1 || e.Schemas[0] != SchemaError {
		t.Errorf("schemas = %v", e.Schemas)
	}
	e2 := NewError(409, "uniqueness", "dup")
	if e2.SCIMType != "uniqueness" {
		t.Errorf("scimType = %q", e2.SCIMType)
	}
}

func TestDiscoveryDocs(t *testing.T) {
	t.Parallel()
	spc := ServiceProviderConfig()
	if _, ok := spc["patch"]; !ok {
		t.Error("ServiceProviderConfig missing patch")
	}
	if schemas, ok := spc["schemas"].([]string); !ok || len(schemas) != 1 || schemas[0] != SchemaServiceProviderConfig {
		t.Errorf("SPC schemas wrong: %v", spc["schemas"])
	}
	rts := ResourceTypes("https://host/scim/v2")
	if len(rts) != 1 || rts[0]["id"] != ResourceTypeUser {
		t.Errorf("ResourceTypes wrong: %v", rts)
	}
	sch := Schemas()
	if len(sch) != 1 || sch[0]["id"] != SchemaUser {
		t.Errorf("Schemas wrong: %v", sch)
	}
}
