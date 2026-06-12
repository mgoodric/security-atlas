package scim

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

// Pure-Go unit tests for the slice-733 SCIM Group wire shaping + PatchOp
// parsing (the fast, no-DB surface — slice 353 Q-2). These exercise the
// branches the integration tier would be slow to enumerate.

func TestGroupRef_PrefersExternalID(t *testing.T) {
	t.Parallel()
	g := DomainGroup{DisplayName: "Engineering", ExternalID: "ext-123"}
	if got := g.GroupRef(); got != "ext-123" {
		t.Fatalf("GroupRef = %q; want ext-123 (externalId preferred)", got)
	}
	g2 := DomainGroup{DisplayName: "Engineering"}
	if got := g2.GroupRef(); got != "Engineering" {
		t.Fatalf("GroupRef = %q; want Engineering (display fallback)", got)
	}
}

func TestWireGroup_NoRoleAttribute(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	g := DomainGroup{ID: id, DisplayName: "Sec", ExternalID: "e1", Active: true}
	wire := WireGroup(g, []string{"u1", "u2"}, "https://host/scim/v2/Groups")
	// Round-trip to JSON and assert there is NO role/roles key anywhere
	// (P0-733-3: the Group wire shape never carries a role).
	raw, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := m["role"]; ok {
		t.Fatal("wire Group must not carry a role attribute (P0-733-3)")
	}
	if _, ok := m["roles"]; ok {
		t.Fatal("wire Group must not carry a roles attribute (P0-733-3)")
	}
	if len(wire.Members) != 2 {
		t.Fatalf("members = %d; want 2", len(wire.Members))
	}
	if wire.Members[0].Value != "u1" || wire.Members[0].Ref == "" {
		t.Fatalf("member shape wrong: %+v", wire.Members[0])
	}
	if wire.ID != id.String() {
		t.Fatalf("id = %q; want %q", wire.ID, id.String())
	}
	if wire.Meta == nil || wire.Meta.Location != "https://host/scim/v2/Groups/"+id.String() {
		t.Fatalf("meta.location wrong: %+v", wire.Meta)
	}
}

func TestParseDisplayNameFilter(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in        string
		wantVal   string
		wantPres  bool
		wantError bool
	}{
		{"", "", false, false},
		{`displayName eq "Engineering"`, "Engineering", true, false},
		{`DISPLAYNAME EQ "Sec"`, "Sec", true, false},
		{`userName eq "x"`, "", false, true},
		{`displayName co "x"`, "", false, true},
		{`displayName eq "a" and x eq "b"`, "", false, true},
	}
	for _, c := range cases {
		val, pres, err := ParseDisplayNameFilter(c.in)
		if (err != nil) != c.wantError {
			t.Fatalf("%q: err=%v wantError=%v", c.in, err, c.wantError)
		}
		if val != c.wantVal || pres != c.wantPres {
			t.Fatalf("%q: val=%q pres=%v; want %q/%v", c.in, val, pres, c.wantVal, c.wantPres)
		}
	}
}

func TestPlanGroupPatch_AddRemoveMembers(t *testing.T) {
	t.Parallel()
	ops := []PatchOperation{
		{Op: "add", Path: "members", Value: json.RawMessage(`[{"value":"u1"},{"value":"u2"}]`)},
		{Op: "remove", Path: `members[value eq "u3"]`},
	}
	intent, err := PlanGroupPatch(ops)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(intent.AddMembers) != 2 || intent.AddMembers[0] != "u1" {
		t.Fatalf("add = %v", intent.AddMembers)
	}
	if len(intent.RemoveMembers) != 1 || intent.RemoveMembers[0] != "u3" {
		t.Fatalf("remove = %v", intent.RemoveMembers)
	}
	if intent.ReplaceMembers {
		t.Fatal("should not be a wholesale replace")
	}
}

func TestPlanGroupPatch_ReplaceMembersWholesale(t *testing.T) {
	t.Parallel()
	ops := []PatchOperation{
		{Op: "replace", Path: "members", Value: json.RawMessage(`["a","b"]`)},
	}
	intent, err := PlanGroupPatch(ops)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if !intent.ReplaceMembers {
		t.Fatal("expected wholesale replace")
	}
	if len(intent.ReplaceMemberSet) != 2 {
		t.Fatalf("replace set = %v", intent.ReplaceMemberSet)
	}
}

func TestPlanGroupPatch_RemoveAllMembers(t *testing.T) {
	t.Parallel()
	ops := []PatchOperation{{Op: "remove", Path: "members"}}
	intent, err := PlanGroupPatch(ops)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if !intent.ReplaceMembers || len(intent.ReplaceMemberSet) != 0 {
		t.Fatalf("bare remove members should clear all: %+v", intent)
	}
}

func TestPlanGroupPatch_DisplayName(t *testing.T) {
	t.Parallel()
	ops := []PatchOperation{{Op: "replace", Path: "displayName", Value: json.RawMessage(`"NewName"`)}}
	intent, err := PlanGroupPatch(ops)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if !intent.SetDisplayName || intent.DisplayName != "NewName" {
		t.Fatalf("displayName intent wrong: %+v", intent)
	}
}

func TestPlanGroupPatch_NoPathReplaceObject(t *testing.T) {
	t.Parallel()
	// A no-path replace whose value object carries displayName + members +
	// an UNKNOWN attribute (which must be ignored — no role can sneak in).
	ops := []PatchOperation{{
		Op:    "replace",
		Value: json.RawMessage(`{"displayName":"Ops","members":[{"value":"z"}],"roles":["admin"]}`),
	}}
	intent, err := PlanGroupPatch(ops)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if !intent.SetDisplayName || intent.DisplayName != "Ops" {
		t.Fatalf("displayName not read: %+v", intent)
	}
	if !intent.ReplaceMembers || len(intent.ReplaceMemberSet) != 1 || intent.ReplaceMemberSet[0] != "z" {
		t.Fatalf("members not read: %+v", intent)
	}
	// `roles` is silently ignored — there is no role field on GroupPatchIntent
	// at all, structurally enforcing P0-733-3.
}

func TestPlanGroupPatch_UnknownPathIgnored(t *testing.T) {
	t.Parallel()
	ops := []PatchOperation{
		{Op: "replace", Path: "roles", Value: json.RawMessage(`["admin"]`)},
		{Op: "add", Path: "emails", Value: json.RawMessage(`"x@y.z"`)},
	}
	intent, err := PlanGroupPatch(ops)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if intent.SetDisplayName || intent.ReplaceMembers ||
		len(intent.AddMembers) != 0 || len(intent.RemoveMembers) != 0 {
		t.Fatalf("unknown paths must be ignored: %+v", intent)
	}
}

func TestPlanGroupPatch_DisplayNameWrongType(t *testing.T) {
	t.Parallel()
	ops := []PatchOperation{{Op: "replace", Path: "displayName", Value: json.RawMessage(`123`)}}
	if _, err := PlanGroupPatch(ops); err == nil {
		t.Fatal("expected error for non-string displayName")
	}
}

func TestDecodeMemberValues_Shapes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw  string
		want []string
	}{
		{`[{"value":"a"},{"value":"b"}]`, []string{"a", "b"}},
		{`["c","d"]`, []string{"c", "d"}},
		{`{"value":"e"}`, []string{"e"}},
		{`"f"`, []string{"f"}},
		{`[]`, nil},
		{``, nil},
		{`null`, nil},
	}
	for _, c := range cases {
		got := decodeMemberValues(json.RawMessage(c.raw))
		if len(got) != len(c.want) {
			t.Fatalf("%s: got %v want %v", c.raw, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("%s: got %v want %v", c.raw, got, c.want)
			}
		}
	}
}

func TestMemberFilterValue(t *testing.T) {
	t.Parallel()
	if v, ok := memberFilterValue(`members[value eq "u1"]`); !ok || v != "u1" {
		t.Fatalf("got %q/%v; want u1/true", v, ok)
	}
	if _, ok := memberFilterValue(`members`); ok {
		t.Fatal("non-filtered path should not match")
	}
	if _, ok := memberFilterValue(`members[display eq "x"]`); ok {
		t.Fatal("non-value attribute should not match")
	}
}

func TestDedupeStrings(t *testing.T) {
	t.Parallel()
	got := dedupeStrings([]string{"x", "y", "x", "z", "y"})
	if len(got) != 3 || got[0] != "x" || got[1] != "y" || got[2] != "z" {
		t.Fatalf("dedupe order/uniqueness failed: %v", got)
	}
}
