//go:build integration

// HTTP-level integration tests for the slice 733 SCIM /Groups resource +
// live group-role re-derivation, driving the real SCIM auth middleware +
// Group handlers + the slice-509 grouprole resolver against Postgres.
//
// Requires DATABASE_URL_APP (atlas_app, RLS) + DATABASE_URL (BYPASSRLS, for
// the lookup-by-hash auth path + cross-tenant seeding). Shares the TestMain +
// pools + seedTenant helpers in integration_test.go (same package).
//
// Security proofs at the HTTP boundary (each AC / P0 a test):
//   - AC-2 / P0-733-4:  /Groups CRUD works under the SCIM credential; a
//     tenant-A credential cannot read/mutate tenant-B groups (real RLS).
//   - AC-3:             a membership change re-derives the affected user's
//     roles through the slice-509 resolver (REUSE — P0-733-1).
//   - P0-733-3:         an UNMAPPED group's membership grants no role
//     (fail-closed at runtime).
package scim_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	scimapi "github.com/mgoodric/security-atlas/internal/api/scim"
	"github.com/mgoodric/security-atlas/internal/auth/bearer"
	"github.com/mgoodric/security-atlas/internal/auth/grouprole"
	"github.com/mgoodric/security-atlas/internal/scim"
)

// groupHarness mounts the real /Users + /Groups handlers (the Group handler
// wired with the REAL slice-509 resolver via the adapter), issues a SCIM
// bearer, and returns the server + bearer.
type groupHarness struct {
	server  *httptest.Server
	bearer  string
	tenant  uuid.UUID
	cred    scim.Credential
	store   *scim.CredentialStore
	deriver scimapi.RoleDeriver
}

// liveDeriver adapts the real grouprole.Resolver to scimapi.RoleDeriver,
// mirroring the cmd-level scimGroupDeriver (Source=SCIM, NIL idp_config). This
// is the SAME resolver slice 509 ships — no mapping logic re-implemented here
// (P0-733-1).
type liveDeriver struct{ r *grouprole.Resolver }

func (d liveDeriver) Derive(ctx context.Context, in scimapi.DeriveRequest) error {
	_, err := d.r.Derive(ctx, grouprole.DeriveInput{
		UserID: in.UserID, IDPConfigID: uuid.Nil, Groups: in.Groups, Source: grouprole.SourceSCIM,
	})
	return err
}

func newGroupHarness(t *testing.T, tenantName string) *groupHarness {
	t.Helper()
	requireAdminPool(t)
	tenant := seedGroupTenant(t, tenantName)

	h, err := bearer.NewHasher([]byte(testHashKey))
	if err != nil {
		t.Fatalf("hasher: %v", err)
	}
	credStore := scim.NewCredentialStore(appPool, adminPool, h)
	credStore.SetPrefix(bearer.PrefixTest)

	ctx, _ := contextWithTenant(t, tenant)
	cred, plain, err := credStore.Issue(ctx, tenant.String(), "test-idp", nil)
	if err != nil {
		t.Fatalf("issue cred: %v", err)
	}

	deriver := liveDeriver{r: grouprole.NewResolver(appPool)}
	r := chi.NewRouter()
	userH := scimapi.NewHandler(scim.NewStore(appPool))
	groupH := scimapi.NewGroupHandler(scim.NewGroupStore(appPool), deriver)
	r.Group(func(sr chi.Router) {
		sr.Use(scimapi.Middleware(credStore))
		userH.Mount(sr)
		groupH.MountGroups(sr)
	})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	return &groupHarness{server: srv, bearer: plain, tenant: tenant, cred: cred, store: credStore, deriver: deriver}
}

func (h *groupHarness) do(t *testing.T, method, path, token, body string) *http.Response {
	t.Helper()
	gh := &harness{server: h.server}
	return gh.do(t, method, path, token, body)
}

// seedGroupTenant seeds a tenant + cleans up every slice-733/509-touched table.
func seedGroupTenant(t *testing.T, name string) uuid.UUID {
	t.Helper()
	requireAdminPool(t)
	id := uuid.New()
	uniqueName := name + "-" + id.String()[:8]
	if _, err := adminPool.Exec(context.Background(),
		`INSERT INTO tenants (id, name) VALUES ($1, $2)`, id, uniqueName); err != nil {
		t.Fatalf("seed group tenant: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		for _, table := range []string{
			"scim_group_members", "scim_groups", "group_role_audit_log",
			"oidc_idp_group_mappings", "user_roles", "scim_audit_log",
			"scim_credentials", "sessions", "users",
		} {
			_, _ = adminPool.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE tenant_id = $1`, table), id)
		}
		_, _ = adminPool.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, id)
	})
	return id
}

// seedGroupMapping inserts a SCIM-source (idp_config NULL) group->role mapping.
func seedGroupMapping(t *testing.T, tenant uuid.UUID, groupRef, role string) {
	t.Helper()
	if _, err := adminPool.Exec(context.Background(),
		`INSERT INTO oidc_idp_group_mappings (tenant_id, idp_config_id, group_ref, role)
		 VALUES ($1, NULL, $2, $3)`, tenant, groupRef, role); err != nil {
		t.Fatalf("seed group mapping: %v", err)
	}
}

// rolesForUser returns the user's roles+origins (BYPASSRLS read).
func rolesForUser(t *testing.T, tenant uuid.UUID, userID string) map[string]string {
	t.Helper()
	rows, err := adminPool.Query(context.Background(),
		`SELECT role, origin FROM user_roles WHERE tenant_id = $1 AND user_id = $2`, tenant, userID)
	if err != nil {
		t.Fatalf("rolesForUser: %v", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var role, origin string
		if err := rows.Scan(&role, &origin); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[role] = origin
	}
	return out
}

func createGroup(t *testing.T, h *groupHarness, displayName, externalID string, members []string) scim.Group {
	t.Helper()
	mem := ""
	for i, m := range members {
		if i > 0 {
			mem += ","
		}
		mem += fmt.Sprintf(`{"value":%q}`, m)
	}
	body := fmt.Sprintf(`{"schemas":["%s"],"displayName":%q,"externalId":%q,"members":[%s]}`,
		scim.SchemaGroup, displayName, externalID, mem)
	resp := h.do(t, http.MethodPost, "/scim/v2/Groups", h.bearer, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create group status = %d; want 201", resp.StatusCode)
	}
	var g scim.Group
	if err := json.NewDecoder(resp.Body).Decode(&g); err != nil {
		t.Fatalf("decode group: %v", err)
	}
	resp.Body.Close()
	return g
}

// TestHTTP_GroupCRUDRoundTrip proves AC-2: Create → Get → List(filter) →
// Patch(add member) → Delete, all 2xx.
func TestHTTP_GroupCRUDRoundTrip(t *testing.T) {
	h := newGroupHarness(t, "scim-grp-crud")

	g := createGroup(t, h, "Engineering", "ext-eng", nil)
	if g.DisplayName != "Engineering" || g.ID == "" {
		t.Fatalf("created group wrong: %+v", g)
	}

	// Get.
	resp := h.do(t, http.MethodGet, "/scim/v2/Groups/"+g.ID, h.bearer, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d; want 200", resp.StatusCode)
	}
	resp.Body.Close()

	// List with displayName filter.
	resp = h.do(t, http.MethodGet, `/scim/v2/Groups?filter=displayName+eq+%22Engineering%22`, h.bearer, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("filter status = %d; want 200", resp.StatusCode)
	}
	var list scim.ListResponse
	_ = json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if list.TotalResults != 1 {
		t.Fatalf("filter total = %d; want 1", list.TotalResults)
	}

	// Patch: add a member.
	user := uuid.New().String()
	patch := fmt.Sprintf(`{"schemas":["%s"],"Operations":[{"op":"add","path":"members","value":[{"value":%q}]}]}`,
		scim.SchemaPatchOp, user)
	resp = h.do(t, http.MethodPatch, "/scim/v2/Groups/"+g.ID, h.bearer, patch)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch status = %d; want 200 (body)", resp.StatusCode)
	}
	resp.Body.Close()

	// Get reflects the member.
	resp = h.do(t, http.MethodGet, "/scim/v2/Groups/"+g.ID, h.bearer, "")
	var got scim.Group
	_ = json.NewDecoder(resp.Body).Decode(&got)
	resp.Body.Close()
	if len(got.Members) != 1 || got.Members[0].Value != user {
		t.Fatalf("group member not reflected: %+v", got.Members)
	}

	// Delete (204).
	resp = h.do(t, http.MethodDelete, "/scim/v2/Groups/"+g.ID, h.bearer, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d; want 204", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestHTTP_GroupMembershipDrivesDerivation proves AC-3 + P0-733-1: a SCIM group
// membership change re-derives the user's roles through the slice-509 resolver.
// A MAPPED group grants the mapped role (group-derived); an UNMAPPED group
// grants nothing (P0-733-3 fail-closed).
func TestHTTP_GroupMembershipDrivesDerivation(t *testing.T) {
	h := newGroupHarness(t, "scim-grp-derive")
	// "Engineering" maps to grc_engineer; "Marketing" is UNMAPPED.
	seedGroupMapping(t, h.tenant, "ext-eng", "grc_engineer")

	user := uuid.New().String()

	// Create the mapped group WITH the user as a member → derivation grants the
	// role on Create.
	createGroup(t, h, "Engineering", "ext-eng", []string{user})
	if got := rolesForUser(t, h.tenant, user); got["grc_engineer"] != "group-derived" {
		t.Fatalf("mapped group did not derive grc_engineer: %v", got)
	}

	// The user in an UNMAPPED group gets no role (fail-closed, P0-733-3).
	other := uuid.New().String()
	createGroup(t, h, "Marketing", "ext-mkt", []string{other})
	if got := rolesForUser(t, h.tenant, other); len(got) != 0 {
		t.Fatalf("unmapped group leaked a role (P0-733-3): %v", got)
	}
}

// TestHTTP_GroupRemoveMemberRevokesRole proves AC-3 reconciliation: removing a
// user from a mapped group re-derives → revokes the group-derived role.
func TestHTTP_GroupRemoveMemberRevokesRole(t *testing.T) {
	h := newGroupHarness(t, "scim-grp-revoke")
	seedGroupMapping(t, h.tenant, "ext-sec", "auditor")

	user := uuid.New().String()
	g := createGroup(t, h, "Security", "ext-sec", []string{user})
	if rolesForUser(t, h.tenant, user)["auditor"] != "group-derived" {
		t.Fatal("auditor not derived after create")
	}

	// Remove the member via a filtered PATCH remove. The path string embeds an
	// escaped inner-quoted value (`members[value eq "uuid"]`) exactly as Okta /
	// Entra send it — built via json.Marshal so the escaping is correct.
	pathJSON, _ := json.Marshal(fmt.Sprintf(`members[value eq "%s"]`, user))
	patch := fmt.Sprintf(`{"schemas":["%s"],"Operations":[{"op":"remove","path":%s}]}`,
		scim.SchemaPatchOp, string(pathJSON))
	resp := h.do(t, http.MethodPatch, "/scim/v2/Groups/"+g.ID, h.bearer, patch)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("remove-member patch status = %d; want 200", resp.StatusCode)
	}
	resp.Body.Close()

	if _, still := rolesForUser(t, h.tenant, user)["auditor"]; still {
		t.Fatalf("auditor should have been revoked after removal: %v", rolesForUser(t, h.tenant, user))
	}
}

// TestHTTP_GroupCrossTenantIsolation proves P0-733-4: a tenant-A SCIM credential
// cannot read or mutate tenant-B's group. Tenant B creates a group; tenant A
// (a separate harness/credential) gets 404 on it (RLS-confined; no oracle).
func TestHTTP_GroupCrossTenantIsolation(t *testing.T) {
	hA := newGroupHarness(t, "scim-grp-rls-A")
	hB := newGroupHarness(t, "scim-grp-rls-B")

	// Tenant B creates a group.
	gB := createGroup(t, hB, "TenantB-Group", "ext-b", nil)

	// Tenant A's credential tries to GET tenant B's group → 404 (not 200, not
	// 403 — no cross-tenant oracle; RLS makes it invisible).
	resp := hA.do(t, http.MethodGet, "/scim/v2/Groups/"+gB.ID, hA.bearer, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-tenant get status = %d; want 404 (P0-733-4)", resp.StatusCode)
	}
	resp.Body.Close()

	// Tenant A's credential tries to DELETE tenant B's group → 404 (no mutation).
	resp = hA.do(t, http.MethodDelete, "/scim/v2/Groups/"+gB.ID, hA.bearer, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-tenant delete status = %d; want 404 (P0-733-4)", resp.StatusCode)
	}
	resp.Body.Close()

	// Tenant B's group is STILL there (tenant A could not touch it).
	resp = hB.do(t, http.MethodGet, "/scim/v2/Groups/"+gB.ID, hB.bearer, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tenant-B group must survive tenant-A's attempts: status = %d", resp.StatusCode)
	}
	resp.Body.Close()
}
