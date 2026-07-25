// breadth_test.go — pure-Go coverage for the slice-678 demo-breadth
// fixtures (org_units + risk org_unit/theme linkage + decisions +
// questionnaire + role-holder users for the policy-ack roster).
//
// These are fast table tests (no Postgres, no build tag) following the
// slice-353 Q-2 pure-Go fast-loop convention. The DB-backed rendering of
// these surfaces is pinned by the integration suite (integration_test.go).
//
// Load-bearing functions + branches covered:
//
//   - buildOrgUnits — three-tier shape (1 company + 3 org + 6 team), every
//     non-root has a parent, parents precede children (topo order for the
//     self-ref FK), the root has no parent.
//   - teamOrgUnits — filters to team-level leaves only.
//   - buildRoleUsers — every user holds demoAckRole; emails are unique +
//     @demo.example; a subset are flagged Acks.
//   - roleUserEmail — lower-cased first.last @ demo.example, distinct from
//     the admin/attester email shapes.
//   - buildDecisions — non-empty, decision_id format, a subset link to a
//     seeded risk, the link index is bounds-checked.
//   - buildQuestionnaire — has questions, exactly one needs-mapping
//     unanswered question, answered questions carry an answer.
//   - nullableUUIDPtr — nil / nil-pointed / non-zero branches.
//   - buildFixtures wiring — risks carry org_unit_id + 2 themes (built-in
//     slugs); policies carry a non-empty acknowledgment_required_roles set
//     including demoAckRole.

package demoseed

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestBuildOrgUnits_Shape(t *testing.T) {
	t.Parallel()
	units := buildOrgUnits()

	var company, org, team int
	idSet := map[uuid.UUID]struct{}{}
	for _, u := range units {
		idSet[u.ID] = struct{}{}
		switch u.Level {
		case "company":
			company++
			if u.ParentID != nil {
				t.Errorf("company-level unit %q has a parent; want root", u.Name)
			}
		case "org":
			org++
			if u.ParentID == nil {
				t.Errorf("org-level unit %q has no parent", u.Name)
			}
		case "team":
			team++
			if u.ParentID == nil {
				t.Errorf("team-level unit %q has no parent", u.Name)
			}
		default:
			t.Errorf("unexpected level %q on %q", u.Level, u.Name)
		}
	}
	if company != 1 {
		t.Errorf("company-level count = %d; want 1", company)
	}
	if org < 1 || team < 1 {
		t.Errorf("org=%d team=%d; want at least one of each", org, team)
	}
	if len(idSet) != len(units) {
		t.Errorf("org_unit ids not unique: %d ids for %d units", len(idSet), len(units))
	}

	// Parents must precede children in emission order (the writer relies
	// on this for the self-ref FK).
	seen := map[uuid.UUID]struct{}{}
	for _, u := range units {
		if u.ParentID != nil {
			if _, ok := seen[*u.ParentID]; !ok {
				t.Errorf("unit %q emitted before its parent %s (FK ordering violation)", u.Name, *u.ParentID)
			}
		}
		seen[u.ID] = struct{}{}
	}
}

func TestTeamOrgUnits_FiltersLeaves(t *testing.T) {
	t.Parallel()
	units := buildOrgUnits()
	leaves := teamOrgUnits(units)
	if len(leaves) == 0 {
		t.Fatal("teamOrgUnits returned no leaves")
	}
	for _, u := range leaves {
		if u.Level != "team" {
			t.Errorf("teamOrgUnits returned a non-team unit %q (level %q)", u.Name, u.Level)
		}
	}
	// Empty input → empty output (no panic).
	if got := teamOrgUnits(nil); got != nil {
		t.Errorf("teamOrgUnits(nil) = %v; want nil", got)
	}
}

func TestBuildRoleUsers_RosterShape(t *testing.T) {
	t.Parallel()
	users := buildRoleUsers()
	if len(users) == 0 {
		t.Fatal("buildRoleUsers returned none")
	}
	emails := map[string]struct{}{}
	anyAcks := false
	for _, u := range users {
		// Every role user must hold the org-wide ack role (so every
		// policy's roster denominator is non-zero).
		holdsAckRole := false
		for _, r := range u.OwnerRoles {
			if r == demoAckRole {
				holdsAckRole = true
			}
		}
		if !holdsAckRole {
			t.Errorf("role user %q does not hold demoAckRole %q; roster would be empty", u.Email, demoAckRole)
		}
		if !strings.HasSuffix(u.Email, "@"+personEmailDomain) {
			t.Errorf("role user email %q not @%s", u.Email, personEmailDomain)
		}
		if _, dup := emails[u.Email]; dup {
			t.Errorf("duplicate role user email %q (users_email_per_tenant_unique would fail)", u.Email)
		}
		emails[u.Email] = struct{}{}
		if u.ID == uuid.Nil {
			t.Errorf("role user %q has a nil id", u.Email)
		}
		if u.Acks {
			anyAcks = true
		}
	}
	if !anyAcks {
		t.Error("no role user is flagged Acks; the roster numerator would be zero")
	}
}

func TestRoleUserEmail_Distinct(t *testing.T) {
	t.Parallel()
	for idx := 0; idx < len(fictionalPeople); idx++ {
		got := roleUserEmail(idx)
		if got != strings.ToLower(got) {
			t.Errorf("roleUserEmail(%d) = %q; not all lower-case", idx, got)
		}
		if !strings.Contains(got, ".") {
			t.Errorf("roleUserEmail(%d) = %q; want first.last shape", idx, got)
		}
		// Must differ from the attester shape (firstname@demo.example) so
		// no users_email_per_tenant_unique collision with the admin user
		// or the manual-evidence payloads.
		if got == fictionalUserEmail(idx) {
			t.Errorf("roleUserEmail(%d) collides with fictionalUserEmail: %q", idx, got)
		}
		if !strings.HasSuffix(got, "@"+personEmailDomain) {
			t.Errorf("roleUserEmail(%d) = %q; want @%s suffix", idx, got, personEmailDomain)
		}
	}
}

func TestBuildDecisions_TimelineAndLinks(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	risks := []riskFixture{
		{ID: uuid.New()}, {ID: uuid.New()}, {ID: uuid.New()}, {ID: uuid.New()},
	}
	decisions := buildDecisions(now, risks)
	if len(decisions) == 0 {
		t.Fatal("buildDecisions returned none; timeline would be empty")
	}
	linked := 0
	ids := map[string]struct{}{}
	for _, d := range decisions {
		if d.ID == uuid.Nil {
			t.Errorf("decision %q has a nil id", d.DecisionID)
		}
		if !strings.HasPrefix(d.DecisionID, "DL-") {
			t.Errorf("decision_id %q does not start with DL-", d.DecisionID)
		}
		if _, dup := ids[d.DecisionID]; dup {
			t.Errorf("duplicate decision_id %q", d.DecisionID)
		}
		ids[d.DecisionID] = struct{}{}
		if d.Status != "active" {
			t.Errorf("decision %q status = %q; want active", d.DecisionID, d.Status)
		}
		if d.LinkedRiskID != nil {
			linked++
		}
	}
	if linked == 0 {
		t.Error("no decision links to a seeded risk; the timeline-to-risk resolution is untested")
	}
}

func TestBuildDecisions_LinkBoundsChecked(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	// No risks: the link index must be bounds-checked → no link set, no panic.
	decisions := buildDecisions(now, nil)
	for _, d := range decisions {
		if d.LinkedRiskID != nil {
			t.Errorf("decision %q linked a risk despite an empty risk set", d.DecisionID)
		}
	}
}

func TestBuildQuestionnaire_AnsweredAndNeedsMapping(t *testing.T) {
	t.Parallel()
	q := buildQuestionnaire("Avery Castellan")
	if q.ID == uuid.Nil {
		t.Fatal("questionnaire has a nil id")
	}
	if len(q.Questions) == 0 {
		t.Fatal("questionnaire has no questions; /questionnaires would render empty")
	}
	needsMapping := 0
	answered := 0
	codes := map[string]struct{}{}
	for _, qq := range q.Questions {
		if qq.ID == uuid.Nil {
			t.Errorf("question %q has a nil id", qq.Code)
		}
		if _, dup := codes[qq.Code]; dup {
			t.Errorf("duplicate question code %q (unique_code_per_qn would fail)", qq.Code)
		}
		codes[qq.Code] = struct{}{}
		if qq.SCFAnchorID == nil {
			needsMapping++
		}
		if qq.AnswerValue != "" || qq.Narrative != "" {
			answered++
		}
	}
	if needsMapping != 1 {
		t.Errorf("needs-mapping question count = %d; want exactly 1 (realistic in-progress state)", needsMapping)
	}
	if answered == 0 {
		t.Error("no answered questions; the demo questionnaire reads as a blank intake form")
	}
}

func TestNullableUUIDPtr_Branches(t *testing.T) {
	t.Parallel()
	if got := nullableUUIDPtr(nil); got != nil {
		t.Errorf("nullableUUIDPtr(nil) = %v; want nil", got)
	}
	z := uuid.Nil
	if got := nullableUUIDPtr(&z); got != nil {
		t.Errorf("nullableUUIDPtr(&uuid.Nil) = %v; want nil", got)
	}
	id := uuid.New()
	got := nullableUUIDPtr(&id)
	if u, ok := got.(uuid.UUID); !ok || u != id {
		t.Errorf("nullableUUIDPtr(&id) = %v (%T); want %v", got, got, id)
	}
}

// TestBuildFixtures_BreadthWiring asserts the slice-678 wiring inside
// buildFixtures: every seeded risk carries an org_unit_id + exactly two
// built-in theme slugs, and every published policy carries a non-empty
// acknowledgment_required_roles set that includes demoAckRole.
func TestBuildFixtures_BreadthWiring(t *testing.T) {
	t.Parallel()
	s := &Seeder{scale: 1.0, clock: time.Now}
	fs := s.buildFixtures("demo-breadth")

	if len(fs.orgUnits) == 0 {
		t.Fatal("buildFixtures seeded no org_units")
	}
	builtin := map[string]struct{}{}
	for _, slug := range demoBuiltinThemes {
		builtin[slug] = struct{}{}
	}
	for _, r := range fs.risks {
		if r.OrgUnitID == uuid.Nil {
			t.Errorf("risk %q has no org_unit_id; the heatmap excludes NULL-org risks", r.Title)
		}
		if len(r.Themes) != 2 {
			t.Errorf("risk %q has %d themes; want 2", r.Title, len(r.Themes))
		}
		for _, th := range r.Themes {
			if _, ok := builtin[th]; !ok {
				t.Errorf("risk %q theme %q is not a built-in org_themes slug", r.Title, th)
			}
		}
	}
	for _, p := range fs.policies {
		if len(p.RequiredRoles) == 0 {
			t.Errorf("policy %q has empty acknowledgment_required_roles; roster would be empty", p.Title)
		}
		hasAck := false
		for _, role := range p.RequiredRoles {
			if role == demoAckRole {
				hasAck = true
			}
		}
		if !hasAck {
			t.Errorf("policy %q required roles %v omit demoAckRole %q", p.Title, p.RequiredRoles, demoAckRole)
		}
	}
}
