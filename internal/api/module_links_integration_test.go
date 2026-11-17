//go:build integration

// #2174 — links, ownership and pickers across modules. Each request goes
// through the production router (JWT, tenancy, the module gate, the module
// access gate, OPA with the live user_roles resolver) on real Postgres
// under RLS. Members are real users rows with user_roles; access is set
// through PUT /v1/admin/users/{id}/module-access as an admin would.
//
// Requires DATABASE_URL_APP (atlas_app, RLS) and DATABASE_URL (migrate role).
package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// newModuleLinksEnv is newModuleAccessEnv plus cleanup for the records the
// #2174 tests write. The cleanup is registered after the tenant's, so it
// runs first and children go before their parents.
func newModuleLinksEnv(t *testing.T) *moduleAccessEnv {
	t.Helper()
	e := newModuleAccessEnv(t)
	t.Cleanup(func() {
		ctx := context.Background()
		for _, table := range []string{
			"incident_timeline", "incident_vendors", "incident_risks", "incident_controls",
			"incident_evidence_links", "incidents",
			"vendor_scope_cells", "vendor_reviews", "vendors", "me_audit_log",
			"policies", "controls", "api_keys", "user_roles",
		} {
			_, _ = e.migrate.Exec(ctx, "DELETE FROM "+table+" WHERE tenant_id = $1", e.tenant)
		}
	})
	return e
}

// seedLinkedPolicy writes a policy linked to control.
func (e *moduleAccessEnv) seedLinkedPolicy(t *testing.T, control uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := e.migrate.Exec(context.Background(), `
		INSERT INTO policies (id, tenant_id, title, version, body_md, status, owner_role, approver_role,
		                      created_by, linked_control_ids)
		VALUES ($1, $2, '#2174 linked policy', '1.0', '# Access', 'draft', 'grc_engineer', 'admin',
		        'test-2174', ARRAY[$3]::uuid[])`,
		id, e.tenant, control); err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	return id
}

// createVendor creates a vendor owned by owner, as the operator.
func (e *moduleAccessEnv) createVendor(t *testing.T, owner uuid.UUID) string {
	t.Helper()
	got := e.do(t, http.MethodPost, "/v1/vendors", e.admin, map[string]any{
		"name": "#2174 vendor " + uuid.NewString()[:8], "criticality": "medium",
		"review_cadence": "annual", "owner_user_id": owner.String(),
	})
	if got.status != http.StatusCreated {
		t.Fatalf("POST /v1/vendors: status %d body=%s", got.status, got.body)
	}
	var body struct {
		Vendor struct {
			ID string `json:"id"`
		} `json:"vendor"`
	}
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("decode vendor: %v", err)
	}
	return body.Vendor.ID
}

type flaggedVendor struct {
	ID               string  `json:"id"`
	OwnerUserID      *string `json:"owner_user_id"`
	OwnerLacksAccess bool    `json:"owner_lacks_access"`
}

// readVendor reads one vendor as bearer, and the same vendor from the list.
func (e *moduleAccessEnv) readVendor(t *testing.T, bearer, id string) (one, listed flaggedVendor) {
	t.Helper()
	got := e.do(t, http.MethodGet, "/v1/vendors/"+id, bearer, nil)
	if got.status != http.StatusOK {
		t.Fatalf("GET /v1/vendors/%s: status %d body=%s", id, got.status, got.body)
	}
	var body struct {
		Vendor flaggedVendor `json:"vendor"`
	}
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("decode vendor: %v", err)
	}
	all := e.do(t, http.MethodGet, "/v1/vendors", bearer, nil)
	if all.status != http.StatusOK {
		t.Fatalf("GET /v1/vendors: status %d body=%s", all.status, all.body)
	}
	var list struct {
		Vendors []flaggedVendor `json:"vendors"`
	}
	if err := json.Unmarshal([]byte(all.body), &list); err != nil {
		t.Fatalf("decode vendors: %v", err)
	}
	for _, v := range list.Vendors {
		if v.ID == id {
			listed = v
		}
	}
	if listed.ID == "" {
		t.Fatalf("GET /v1/vendors does not list %s", id)
	}
	return body.Vendor, listed
}

// peopleIDs returns the user ids GET /v1/people answers with query.
func (e *moduleAccessEnv) peopleIDs(t *testing.T, bearer, query string) map[string]bool {
	t.Helper()
	got := e.do(t, http.MethodGet, "/v1/people"+query, bearer, nil)
	if got.status != http.StatusOK {
		t.Fatalf("GET /v1/people%s: status %d body=%s", query, got.status, got.body)
	}
	var body struct {
		Users []struct {
			ID string `json:"id"`
		} `json:"users"`
	}
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("decode people: %v", err)
	}
	out := map[string]bool{}
	for _, u := range body.Users {
		out[u.ID] = true
	}
	return out
}

// A member without Policies access reads a control and gets no
// linked-policy data: the linked-policies read is refused and the control
// itself names no policy. A member with access gets the linked policy.
func TestModuleLinks_ControlLinkedPoliciesFollowAccess(t *testing.T) {
	e := newModuleLinksEnv(t)
	control := seedModuleControl(t, e.migrate, e.tenant)
	policy := e.seedLinkedPolicy(t, control).String()
	narrowed, narrowedBearer := e.member(t, "grc_engineer")
	_, fullBearer := e.member(t, "grc_engineer")
	e.setAccess(t, narrowed, everyOptionalBut("policies"))

	with := e.do(t, http.MethodGet, "/v1/controls/"+control.String()+"/policies", fullBearer, nil)
	if with.status != http.StatusOK || !strings.Contains(with.body, policy) {
		t.Fatalf("with Policies access: GET linked policies status %d body=%s; want 200 naming %s", with.status, with.body, policy)
	}

	without := e.do(t, http.MethodGet, "/v1/controls/"+control.String()+"/policies", narrowedBearer, nil)
	if !noAccess(without, "policies") {
		t.Errorf("no Policies access: GET linked policies status %d body=%s; want the Policies 403", without.status, without.body)
	}
	if strings.Contains(without.body, policy) {
		t.Errorf("no Policies access: linked-policies answer names the policy: %s", without.body)
	}
	// The control itself (the list the control page reads it from, and its
	// state) still reads, and names no policy.
	for _, p := range []string{"/v1/controls", "/v1/controls/" + control.String() + "/state"} {
		got := e.do(t, http.MethodGet, p, narrowedBearer, nil)
		if got.status != http.StatusOK && got.status != http.StatusNotFound {
			t.Fatalf("no Policies access: GET %s status %d body=%s; want a core answer", p, got.status, got.body)
		}
		if noAccess(got, "") {
			t.Errorf("no Policies access: GET %s answered the module 403; Controls is core", p)
		}
		if strings.Contains(got.body, policy) {
			t.Errorf("no Policies access: GET %s names the linked policy: %s", p, got.body)
		}
	}
}

// A vendor whose owner lost Third-party risk access keeps its owner, and
// an admin reading it (one vendor, or the list) sees the flag. Restoring
// access clears the flag.
func TestModuleLinks_VendorOwnerLostAccessFlagged(t *testing.T) {
	e := newModuleLinksEnv(t)
	owner, _ := e.member(t, "grc_engineer")
	_, reader := e.member(t, "grc_engineer")
	vendor := e.createVendor(t, owner)

	if one, listed := e.readVendor(t, e.admin, vendor); one.OwnerLacksAccess || listed.OwnerLacksAccess {
		t.Fatalf("owner with access: flagged (one=%v, list=%v); want no flag", one.OwnerLacksAccess, listed.OwnerLacksAccess)
	}

	e.setAccess(t, owner, everyOptionalBut("third_party_risk"))
	for name, bearer := range map[string]string{"admin": e.admin, "member who can see it": reader} {
		one, listed := e.readVendor(t, bearer, vendor)
		if one.OwnerUserID == nil || *one.OwnerUserID != owner.String() {
			t.Errorf("%s: owner_user_id = %v; want the owner %s kept", name, one.OwnerUserID, owner)
		}
		if !one.OwnerLacksAccess || !listed.OwnerLacksAccess {
			t.Errorf("%s: owner lost access, flag one=%v list=%v; want true on both", name, one.OwnerLacksAccess, listed.OwnerLacksAccess)
		}
	}

	e.setAccess(t, owner, nil)
	if one, _ := e.readVendor(t, e.admin, vendor); one.OwnerLacksAccess {
		t.Errorf("access restored: still flagged")
	}
}

// #2501: a vendor owned by a user with no live role is flagged, though
// nothing narrowed them; granting a role clears the flag. A super-admin
// owner with no role row is not flagged.
func TestModuleLinks_VendorOwnerWithNoLiveRoleFlagged(t *testing.T) {
	e := newModuleLinksEnv(t)
	roleless := seedPeriodUser(t, e.migrate, e.tenant, "active")
	vendor := e.createVendor(t, roleless)
	if one, listed := e.readVendor(t, e.admin, vendor); !one.OwnerLacksAccess || !listed.OwnerLacksAccess {
		t.Errorf("owner with no live role: flag one=%v list=%v; want true on both", one.OwnerLacksAccess, listed.OwnerLacksAccess)
	}

	if _, err := e.migrate.Exec(context.Background(),
		`INSERT INTO user_roles (tenant_id, user_id, role, granted_by) VALUES ($1, $2, 'grc_engineer', 'test-2501')`,
		e.tenant, roleless.String()); err != nil {
		t.Fatalf("grant role: %v", err)
	}
	if one, _ := e.readVendor(t, e.admin, vendor); one.OwnerLacksAccess {
		t.Errorf("role granted: still flagged")
	}

	super := seedPeriodUser(t, e.migrate, e.tenant, "active")
	e.grantSuperAdmin(t, super)
	if one, _ := e.readVendor(t, e.admin, e.createVendor(t, super)); one.OwnerLacksAccess {
		t.Errorf("super-admin owner with no role row: flagged; want no flag")
	}
}

// grantSuperAdmin makes user a platform super-admin for the test.
func (e *moduleAccessEnv) grantSuperAdmin(t *testing.T, user uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	if _, err := e.migrate.Exec(ctx,
		`INSERT INTO super_admins (user_id, granted_via) VALUES ($1, 'manual_grant')`, user); err != nil {
		t.Fatalf("grant super-admin: %v", err)
	}
	t.Cleanup(func() { _, _ = e.migrate.Exec(ctx, `DELETE FROM super_admins WHERE user_id = $1`, user) })
}

// #2501: only a caller who can work in a module may ask who else can. A
// member narrowed out of Third-party risk gets 403 for ?module= on it,
// whatever the directory holds; a core module still answers.
func TestModuleLinks_PeopleModuleFilterNeedsAccess(t *testing.T) {
	e := newModuleLinksEnv(t)
	narrowed, bearer := e.member(t, "grc_engineer")
	e.setAccess(t, narrowed, everyOptionalBut("third_party_risk"))

	if got := e.do(t, http.MethodGet, "/v1/people?module=third_party_risk", bearer, nil); got.status != http.StatusForbidden {
		t.Errorf("narrowed caller ?module=third_party_risk: status %d body=%s; want 403", got.status, got.body)
	}
	if got := e.peopleIDs(t, bearer, "?module=risk"); !got[narrowed.String()] {
		t.Errorf("narrowed caller ?module=risk leaves themself out: %v", got)
	}
	if got := e.peopleIDs(t, bearer, "?module=controls"); !got[narrowed.String()] {
		t.Errorf("narrowed caller ?module=controls leaves themself out: %v", got)
	}
}

// #2501: a caller without Third-party risk access cannot link an incident
// to a vendor, and the refusal is the same for a vendor that exists and
// an id that names none, so it cannot be used to probe. Nothing is
// written. A caller with access links the vendor.
func TestModuleLinks_IncidentCreateRefusesHiddenLinks(t *testing.T) {
	e := newModuleLinksEnv(t)
	owner, _ := e.member(t, "grc_engineer")
	narrowed, narrowedBearer := e.member(t, "grc_engineer")
	_, fullBearer := e.member(t, "grc_engineer")
	vendor := e.createVendor(t, owner)
	e.setAccess(t, narrowed, everyOptionalBut("third_party_risk", "risk"))

	ctx := context.Background()
	count := func() int {
		var n int
		if err := e.migrate.QueryRow(ctx, `SELECT count(*) FROM incidents WHERE tenant_id = $1`, e.tenant).Scan(&n); err != nil {
			t.Fatalf("count incidents: %v", err)
		}
		return n
	}
	before := count()
	var answers []moduleGateResponse
	for _, body := range []map[string]any{
		{"title": "#2501 existing vendor", "severity": "p3", "vendor_ids": []string{vendor}},
		{"title": "#2501 missing vendor", "severity": "p3", "vendor_ids": []string{uuid.NewString()}},
	} {
		answers = append(answers, e.do(t, http.MethodPost, "/v1/incidents", narrowedBearer, body))
	}
	for _, got := range answers {
		if got.status != http.StatusForbidden {
			t.Errorf("no access: POST /v1/incidents vendor_ids status %d body=%s; want 403", got.status, got.body)
		}
	}
	if answers[0].status != answers[1].status || answers[0].body != answers[1].body {
		t.Errorf("existing and missing vendor answer differently: %d %s / %d %s",
			answers[0].status, answers[0].body, answers[1].status, answers[1].body)
	}
	risk := e.do(t, http.MethodPost, "/v1/incidents", narrowedBearer, map[string]any{
		"title": "#2501 risk", "severity": "p3", "risk_ids": []string{uuid.NewString()},
	})
	if risk.status != http.StatusForbidden || !strings.Contains(risk.body, "risk_ids") {
		t.Errorf("no Risk access: POST /v1/incidents risk_ids status %d body=%s; want 403 naming risk_ids", risk.status, risk.body)
	}
	if after := count(); after != before {
		t.Errorf("refused creates wrote %d incidents", after-before)
	}

	// Without links the narrowed caller still records an incident (core).
	if got := e.do(t, http.MethodPost, "/v1/incidents", narrowedBearer, map[string]any{
		"title": "#2501 no links", "severity": "p3",
	}); got.status != http.StatusCreated {
		t.Errorf("no links: POST /v1/incidents status %d body=%s; want 201", got.status, got.body)
	}
	if got := e.do(t, http.MethodPost, "/v1/incidents", fullBearer, map[string]any{
		"title": "#2501 with access", "severity": "p3", "vendor_ids": []string{vendor},
	}); got.status != http.StatusCreated || !strings.Contains(got.body, vendor) {
		t.Errorf("with access: POST /v1/incidents status %d body=%s; want 201 naming the vendor", got.status, got.body)
	}
}

// The assignee options for a Third-party risk record leave out members
// without access to it. Admins and auditors are exempt; a member on "all"
// is listed. The unfiltered directory still lists everyone.
func TestModuleLinks_AssigneeOptionsLeaveOutMembersWithoutAccess(t *testing.T) {
	e := newModuleLinksEnv(t)
	withAccess, _ := e.member(t, "grc_engineer")
	without, _ := e.member(t, "grc_engineer")
	admin, _ := e.member(t, "admin")
	e.setAccess(t, without, everyOptionalBut("third_party_risk"))
	e.setAccess(t, admin, []string{})

	got := e.peopleIDs(t, e.admin, "?module=third_party_risk")
	if !got[withAccess.String()] || !got[admin.String()] {
		t.Errorf("assignee options %v: want the member with access and the admin", got)
	}
	if got[without.String()] {
		t.Errorf("assignee options list %s, who has no Third-party risk access", without)
	}
	// #2501: a user with no live role cannot use anything, so they are
	// not offered, though nothing narrowed them; a super-admin with no
	// role row in the tenant is.
	roleless := seedPeriodUser(t, e.migrate, e.tenant, "active")
	super := seedPeriodUser(t, e.migrate, e.tenant, "active")
	e.grantSuperAdmin(t, super)
	got = e.peopleIDs(t, e.admin, "?module=third_party_risk")
	if got[roleless.String()] {
		t.Errorf("assignee options list %s, who has no live role", roleless)
	}
	if !got[super.String()] {
		t.Errorf("assignee options leave out %s, a super-admin with no role row", super)
	}
	if all := e.peopleIDs(t, e.admin, ""); !all[without.String()] || !all[roleless.String()] {
		t.Errorf("the unfiltered directory leaves out %s or %s; it should list everyone", without, roleless)
	}
	// A core module filters no one; an unknown one is a 400.
	if core := e.peopleIDs(t, e.admin, "?module=controls"); !core[without.String()] {
		t.Errorf("?module=controls leaves out %s; a core module has no access to lack", without)
	}
	if bad := e.do(t, http.MethodGet, "/v1/people?module=nope", e.admin, nil); bad.status != http.StatusBadRequest {
		t.Errorf("?module=nope: status %d; want 400", bad.status)
	}
}

// An incident (core) linked to a vendor leaves the vendor link out for a
// member without Third-party risk access, and keeps it for one with.
func TestModuleLinks_IncidentHidesVendorLinks(t *testing.T) {
	e := newModuleLinksEnv(t)
	owner, _ := e.member(t, "grc_engineer")
	narrowed, narrowedBearer := e.member(t, "grc_engineer")
	_, fullBearer := e.member(t, "grc_engineer")
	vendor := e.createVendor(t, owner)

	created := e.do(t, http.MethodPost, "/v1/incidents", e.admin, map[string]any{
		"title": "#2174 incident", "severity": "p3", "vendor_ids": []string{vendor},
	})
	if created.status != http.StatusCreated {
		t.Fatalf("POST /v1/incidents: status %d body=%s", created.status, created.body)
	}
	var body struct {
		Incident struct {
			Record struct {
				ID string `json:"id"`
			} `json:"record"`
		} `json:"incident"`
	}
	if err := json.Unmarshal([]byte(created.body), &body); err != nil {
		t.Fatalf("decode incident: %v", err)
	}
	e.setAccess(t, narrowed, everyOptionalBut("third_party_risk"))

	path := "/v1/incidents/" + body.Incident.Record.ID
	if got := e.do(t, http.MethodGet, path, fullBearer, nil); got.status != http.StatusOK || !strings.Contains(got.body, vendor) {
		t.Errorf("with access: GET incident status %d body=%s; want the vendor link", got.status, got.body)
	}
	got := e.do(t, http.MethodGet, path, narrowedBearer, nil)
	if got.status != http.StatusOK {
		t.Fatalf("no access: GET incident status %d body=%s; want 200 (Incidents is core)", got.status, got.body)
	}
	if strings.Contains(got.body, vendor) {
		t.Errorf("no Third-party risk access: the incident names the vendor: %s", got.body)
	}
}
