package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/lifecycle"
)

// committedDir is docs/lifecycles/, relative to this package.
var committedDir = filepath.Join("..", "..", "docs", "lifecycles")

// TestExport_MatchesCommitted: docs/lifecycles/ holds exactly one document
// per declared lifecycle, byte for byte what the export writes. On
// failure, run `just lifecycles-export`.
func TestExport_MatchesCommitted(t *testing.T) {
	t.Parallel()
	docs, err := render(tables())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	entries, err := os.ReadDir(committedDir)
	if err != nil {
		t.Fatalf("read %s: %v", committedDir, err)
	}
	var committed []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			committed = append(committed, e.Name())
		}
	}
	var want []string
	for name := range docs {
		want = append(want, name)
	}
	slices.Sort(committed)
	slices.Sort(want)
	if !slices.Equal(committed, want) {
		t.Fatalf("docs/lifecycles holds %v, the tables export %v; run `just lifecycles-export`", committed, want)
	}
	for name, b := range docs {
		got, err := os.ReadFile(filepath.Join(committedDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if string(got) != string(b) {
			t.Errorf("docs/lifecycles/%s differs from its table; run `just lifecycles-export`", name)
		}
	}
}

// TestExport_DocumentMatchesTable: each committed document decodes to its
// table: the same states, initial and terminal states, and transitions.
func TestExport_DocumentMatchesTable(t *testing.T) {
	t.Parallel()
	for _, tab := range tables() {
		b, err := os.ReadFile(filepath.Join(committedDir, tab.Name+".json"))
		if err != nil {
			t.Fatalf("%s: %v", tab.Name, err)
		}
		var doc lifecycle.Table
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatalf("%s: decode: %v", tab.Name, err)
		}
		if !reflect.DeepEqual(doc, tab) {
			t.Errorf("%s: document does not match the table:\n doc   %+v\n table %+v", tab.Name, doc, tab)
		}
	}
}

func TestTables_Valid(t *testing.T) {
	t.Parallel()
	ts := tables()
	if len(ts) != 8 {
		t.Fatalf("want 8 lifecycles, got %d", len(ts))
	}
	for _, tab := range ts {
		if err := tab.Validate(); err != nil {
			t.Error(err)
		}
	}
}

// TestRoles_MatchAuthzBundle: the roles a table names on a move or an
// entry are exactly the roles the authorization bundle admits on each of
// its routes. The handler's approver gate (RequiresApprover) is a further
// cut on top and is not modelled by the bundle.
func TestRoles_MatchAuthzBundle(t *testing.T) {
	t.Parallel()
	e, err := authz.NewEngine(context.Background(), authz.NoopRolesResolver{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	const tenant = "00000000-0000-4000-8000-000000002146"
	param := regexp.MustCompile(`\{[a-z_]+\}`)
	check := func(lc, what string, routes, roles []string) {
		for _, route := range routes {
			method, path, ok := strings.Cut(route, " ")
			if !ok {
				t.Fatalf("%s %s: route %q is not METHOD PATH", lc, what, route)
			}
			path = param.ReplaceAllString(path, "6f1c2a52-3b0e-4b8e-9a51-1d6a3f0c2146")
			r := httptest.NewRequest(method, path, nil)
			r = r.WithContext(authctx.WithCredential(r.Context(),
				credstore.Credential{ID: "jwt:2146", TenantID: tenant, UserID: "user-2146"}))
			for _, role := range authz.CanonicalRoles() {
				in := authz.BuildInput(r, nil)
				in.User.Roles = []authz.Role{role}
				d, err := e.Decide(context.Background(), in)
				if err != nil {
					t.Fatalf("%s %s %s: Decide: %v", lc, what, route, err)
				}
				if want := slices.Contains(roles, string(role)); d.Allow != want {
					t.Errorf("%s %s: %s as %s: bundle allow=%v, table says %v", lc, what, route, role, d.Allow, want)
				}
			}
		}
	}
	for _, tab := range tables() {
		for _, en := range tab.Initial {
			check(tab.Name, en.Action, en.Routes, en.Roles)
		}
		for _, tr := range tab.Transitions {
			check(tab.Name, tr.From+"->"+tr.To, tr.Routes, tr.Roles)
		}
	}
}
