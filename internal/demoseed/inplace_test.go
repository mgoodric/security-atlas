package demoseed

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// The stock list must name exactly the bundles bootstrap.sh uploads from
// controls/soc2/, or a fresh install would be refused as holding real data.
func TestStockControlBundleIDs_MatchControlsDir(t *testing.T) {
	entries, err := os.ReadDir("../../controls/soc2")
	if err != nil {
		t.Fatalf("read controls/soc2: %v", err)
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	got := slices.Sorted(slices.Values(stockControlBundleIDs))
	if !slices.Equal(got, dirs) {
		t.Errorf("stockControlBundleIDs drifted from controls/soc2/:\n got  %v\n want %v", got, dirs)
	}
}

func TestOrgData_EmptyAndString(t *testing.T) {
	t.Parallel()
	cases := []struct {
		od    OrgData
		empty bool
		want  string
	}{
		{OrgData{}, true, "none"},
		{OrgData{Controls: 1}, false, "1 non-stock control"},
		{OrgData{Controls: 2, Risks: 1, Evidence: 3, Policies: 1, FrameworkVersions: 2}, false,
			"2 non-stock controls, 1 risk, 3 evidence records, 1 non-stock policy, 2 tenant framework versions"},
	}
	for _, c := range cases {
		if c.od.Empty() != c.empty {
			t.Errorf("%+v.Empty() = %v, want %v", c.od, c.od.Empty(), c.empty)
		}
		if got := c.od.String(); got != c.want {
			t.Errorf("%+v.String() = %q, want %q", c.od, got, c.want)
		}
	}
}

func TestRowKeyExpr(t *testing.T) {
	t.Parallel()
	if got := rowKeyExpr(nil); got != "to_jsonb(t.*)" {
		t.Errorf("no pk: %q", got)
	}
	if got, want := rowKeyExpr([]string{"tenant_id", "id"}),
		`jsonb_build_object('tenant_id', t."tenant_id", 'id', t."id")`; got != want {
		t.Errorf("pk: %q, want %q", got, want)
	}
}

func TestInPlaceTeardownOrder_KnownTablesFirstThenAlphabetical(t *testing.T) {
	t.Parallel()
	order := inPlaceTeardownOrder(map[string][]string{"zeta": nil, "alpha": nil, "controls": nil})
	if !slices.Equal(order[:len(teardownTables)], teardownTables) {
		t.Fatal("teardownTables order not preserved")
	}
	if got := order[len(teardownTables):]; !slices.Equal(got, []string{"alpha", "zeta"}) {
		t.Errorf("extra tables = %v, want [alpha zeta]", got)
	}
}

// A slug-less seed on an install with no tenant, or several, refuses before
// touching the database and says what to do.
func TestImplicitTenant_NoneOrSeveral(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		err  error
		want string
	}{
		"none":    {tenancy.ErrNoTenants, "run the install bootstrap first"},
		"several": {&tenancy.AmbiguousTenantError{Tenants: []tenancy.TenantRef{{ID: uuid.New()}, {ID: uuid.New()}}}, "multi-tenant install"},
	}
	for name, c := range cases {
		s := &Seeder{resolveTenant: func(context.Context) (tenancy.TenantRef, error) { return tenancy.TenantRef{}, c.err }}
		_, err := s.Seed(context.Background(), SeedInput{})
		if !errors.Is(err, ErrNoImplicitTenant) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: Seed err = %v, want ErrNoImplicitTenant mentioning %q", name, err, c.want)
		}
		_, err = s.TeardownImplicit(context.Background(), uuid.Nil, uuid.Nil)
		if !errors.Is(err, ErrNoImplicitTenant) {
			t.Errorf("%s: TeardownImplicit err = %v, want ErrNoImplicitTenant", name, err)
		}
	}
}

func TestIsTenantAuditTable(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]bool{
		"me_audit_log":                         true,
		"evidence_audit_log":                   true,
		"decisions_audit":                      true,
		"questionnaire_mapping_proposal_audit": true,
		"audit_periods":                        false,
		"audit_notes":                          false,
		"auditor_assignments":                  false,
		"controls":                             false,
	} {
		if got := isTenantAuditTable(name); got != want {
			t.Errorf("isTenantAuditTable(%q) = %v, want %v", name, got, want)
		}
	}
}
