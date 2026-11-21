package authz_test

// #2706: drift guard for the #2447 helper set. gateSites2447 inventories
// the calls of the helpers isHelper2447 names, so a new tenant-role helper
// in roles.go would be called from handlers without the inventory (or the
// OPA agreement check behind it) seeing it. This test parses roles.go and
// fails on any exported IsTenant* or Has*Role function that is neither an
// inventoried helper nor listed in nonGate2447 with a reason.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"slices"
	"sort"
	"testing"
)

// nonGate2447 is each tenant-role-shaped helper in roles.go that is not
// inventoried, with why. docs/audit-log/2447-tenant-role-gates-followups-decisions.md
// D5 records the reasoning.
var nonGate2447 = map[string]string{
	"HasTenantRole": "sets has_tenant_role on /v1/me and never allows or denies a request; " +
		"the route's gate is the Rego roleless self-read allow (defaults.rego, #2494)",
	"HasOwnerRole": "per-resource ownership of one control against AckRoleLabels (attest.go 403); " +
		"Rego admits the control_owner role and hands ownership to the handler (control_owner.rego, #2523)",
}

var roleHelperName2706 = regexp.MustCompile(`^IsTenant|^Has.*Role$`)

// roleHelpers2706 returns the exported IsTenant* / Has*Role functions
// declared in src.
func roleHelpers2706(t *testing.T, filename string, src any) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), filename, src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", filename, err)
	}
	var out []string
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || !fn.Name.IsExported() || !roleHelperName2706.MatchString(fn.Name.Name) {
			continue
		}
		out = append(out, fn.Name.Name)
	}
	sort.Strings(out)
	return out
}

func TestRoleHelpers2706_EveryHelperDecided(t *testing.T) {
	t.Parallel()
	found := map[string]bool{}
	for _, name := range roleHelpers2706(t, "roles.go", nil) {
		found[name] = true
		_, nonGate := nonGate2447[name]
		switch {
		case isHelper2447(name) && nonGate:
			t.Errorf("%s is both inventoried (isHelper2447) and in nonGate2447: pick one", name)
		case !isHelper2447(name) && !nonGate:
			t.Errorf("roles.go exports %s with no #2447 decision: add it to isHelper2447 and gateSites2447, "+
				"or to nonGate2447 with a reason", name)
		}
	}
	for name, why := range nonGate2447 {
		if !found[name] {
			t.Errorf("nonGate2447 lists %s, which roles.go no longer exports: remove it", name)
		}
		if why == "" {
			t.Errorf("nonGate2447 gives no reason for %s", name)
		}
	}
	for _, name := range []string{admin2447, approver2447, riskReader2447, auditReader2447, anyRole2447, ctlOwner2447} {
		if !found[name] {
			t.Errorf("isHelper2447 names %s, which roles.go no longer exports", name)
		}
	}
}

// TestRoleHelpers2706_MatcherCatches pins which declarations the scan
// treats as tenant-role helpers.
func TestRoleHelpers2706_MatcherCatches(t *testing.T) {
	t.Parallel()
	const src = `package authz
func IsTenantNew() bool { return false }
func HasNewRole() bool { return false }
func HasRoleThing() bool { return false }
func isTenantPrivate() bool { return false }
func TenantRoleLabels() []string { return nil }
type T struct{}
func (T) IsTenantMethod() bool { return false }
`
	got := roleHelpers2706(t, "x.go", src)
	want := []string{"HasNewRole", "IsTenantNew"}
	if !slices.Equal(got, want) {
		t.Fatalf("roleHelpers2706 = %v; want %v", got, want)
	}
}
