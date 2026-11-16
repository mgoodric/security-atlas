// #2067 — pure unit tests for grouped ordering, the shared limit, and
// the policies admit. No Postgres.

package search

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
)

func hit(typ, id string, score float64) SearchHit {
	return SearchHit{ID: id, Type: typ, RelevanceScore: score}
}

func ids(hits []SearchHit) string {
	parts := make([]string, len(hits))
	for i, h := range hits {
		parts[i] = h.ID
	}
	return strings.Join(parts, ",")
}

// TestMergeGrouped_OwnRecordsBeforeCatalog: an anchor that matches
// every token still sorts after a control that matches one, and within
// a group the best match comes first.
func TestMergeGrouped_OwnRecordsBeforeCatalog(t *testing.T) {
	t.Parallel()
	perType := map[string][]SearchHit{
		TypeAnchors:  {hit(TypeAnchors, "a1", 1.0)},
		TypeEvidence: {hit(TypeEvidence, "e1", 1.0)},
		TypePolicies: {hit(TypePolicies, "p1", 0.5), hit(TypePolicies, "p2", 1.0)},
		TypeRisks:    {hit(TypeRisks, "r1", 1.0)},
		TypeControls: {hit(TypeControls, "c1", 0.5), hit(TypeControls, "c2", 0.5)},
	}
	got := ids(mergeGrouped(allTypes, perType, 50))
	if want := "c1,c2,r1,p2,p1,e1,a1"; got != want {
		t.Fatalf("mergeGrouped = %s, want %s", got, want)
	}
}

// TestMergeGrouped_SharesLimitAcrossGroups: a group with many matches
// cannot push a later group out of the response.
func TestMergeGrouped_SharesLimitAcrossGroups(t *testing.T) {
	t.Parallel()
	var controls []SearchHit
	for _, id := range []string{"c1", "c2", "c3", "c4", "c5", "c6"} {
		controls = append(controls, hit(TypeControls, id, 1))
	}
	perType := map[string][]SearchHit{
		TypeControls: controls,
		TypePolicies: {hit(TypePolicies, "p1", 1)},
		TypeAnchors:  {hit(TypeAnchors, "a1", 1), hit(TypeAnchors, "a2", 1)},
	}
	types := []string{TypeControls, TypePolicies, TypeAnchors}
	// One row per group per round: c1,p1,a1 then c2,a2.
	if got, want := ids(mergeGrouped(types, perType, 5)), "c1,c2,p1,a1,a2"; got != want {
		t.Fatalf("limit 5: got %s, want %s", got, want)
	}
	if got, want := ids(mergeGrouped(types, perType, 50)), "c1,c2,c3,c4,c5,c6,p1,a1,a2"; got != want {
		t.Fatalf("limit 50: got %s, want %s", got, want)
	}
}

func TestTotalOf(t *testing.T) {
	t.Parallel()
	if n := totalOf(nil); n != 0 {
		t.Errorf("totalOf(nil) = %d, want 0", n)
	}
	if n := totalOf([]map[string]string{{"total": "37"}}); n != 37 {
		t.Errorf("totalOf(37) = %d, want 37", n)
	}
}

// TestBuildTokenizedQuery_RanksByMatchedTokens: LIMIT keeps the rows
// matching the most tokens, with the caller's order as tiebreaker.
func TestBuildTokenizedQuery_RanksByMatchedTokens(t *testing.T) {
	t.Parallel()
	stmt, args := buildTokenizedQuery("SELECT id FROM t WHERE ", []string{"title"}, []string{"iam", "review"}, "iam review", "ORDER BY title ASC", 7)
	want := "ORDER BY ((title ILIKE $1)::int + (title ILIKE $2)::int) DESC, title ASC LIMIT $3"
	if !strings.HasSuffix(stmt, want) {
		t.Fatalf("stmt = %q, want suffix %q", stmt, want)
	}
	if len(args) != 3 || args[2] != 7 {
		t.Fatalf("args = %v, want limit 7 last", args)
	}
}

type fixedRoles []authz.Role

func (f fixedRoles) RolesFor(context.Context, string, string) ([]authz.Role, error) {
	return f, nil
}

// TestPoliciesAdmit_MatchesPolicyList: for every role, search returns
// policy hits exactly when OPA admits GET /v1/policies. Search adds no
// visibility of its own, drafts included.
func TestPoliciesAdmit_MatchesPolicyList(t *testing.T) {
	t.Parallel()
	roles := []authz.Role{authz.RoleAdmin, authz.RoleGRCEngineer, authz.RoleControlOwner, authz.RoleAuditor, authz.RoleViewer}
	// A plain tenant credential plus one resolver role per run, so each
	// role's policies read rule is exercised on both paths.
	for _, role := range roles {
		engine, err := authz.NewEngine(context.Background(), fixedRoles{role})
		if err != nil {
			t.Fatalf("NewEngine: %v", err)
		}
		cred := credstore.Credential{ID: "c", TenantID: "11111111-1111-1111-1111-111111111111", UserID: "user-1"}

		listReq := httptest.NewRequest("GET", "/v1/policies", nil)
		listReq = listReq.WithContext(authctx.WithCredential(listReq.Context(), cred))
		listDecision, err := engine.Decide(listReq.Context(), authz.BuildInput(listReq, nil))
		if err != nil {
			t.Fatalf("%s: Decide list: %v", role, err)
		}

		searchReq := httptest.NewRequest("GET", "/v1/search?q=access", nil)
		searchReq = searchReq.WithContext(authctx.WithCredential(searchReq.Context(), cred))
		admitted, _ := New(nil, engine).partitionByAdmit(searchReq, []string{TypePolicies})
		searchAdmits := len(admitted) == 1

		if searchAdmits != listDecision.Allow {
			t.Errorf("%s: search admits policies=%v, GET /v1/policies allow=%v", role, searchAdmits, listDecision.Allow)
		}
	}
}

// TestPoliciesAdmit_NoCredentialDenied: with no credential the policies
// type is denied and reported in partial_types.
func TestPoliciesAdmit_NoCredentialDenied(t *testing.T) {
	t.Parallel()
	engine, err := authz.NewEngine(context.Background(), authz.NoopRolesResolver{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	req := httptest.NewRequest("GET", "/v1/search?q=access", nil)
	admitted, denied := New(nil, engine).partitionByAdmit(req, []string{TypeControls, TypePolicies})
	if len(admitted) != 0 || !equalStringSlices(denied, []string{TypeControls, TypePolicies}) {
		t.Fatalf("admitted=%v denied=%v, want everything denied", admitted, denied)
	}
}
