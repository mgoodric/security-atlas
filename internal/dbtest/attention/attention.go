//go:build integration

// Package attention is the front-door fixture (#2130, parent #2072,
// Testing Decisions): two tenants, two users in each, and every
// needs-attention source. Each suite reads the subset it is about: the
// calendar's due-date tests read the dated sources, the controls list
// (#2131) reads the controls. Every source is seeded in both tenants so a
// test can prove none of them leaks into the other tenant.
//
// It began in internal/api/calendar's tests (#2130) and moved here so
// more than one package can seed it (#2131).
package attention

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// Tenant is one tenant's slice of the fixture: the ids of its two
// users and of each needs-attention source.
type Tenant struct {
	ID string

	Owner uuid.UUID // owns the controls, risks and plans below
	Other uuid.UUID // a second user in the same tenant who owns nothing

	FailingControl    uuid.UUID // in scope, latest evaluation failed
	PendingApproval   uuid.UUID // an exception requested and awaiting approval
	OverduePolicy     uuid.UUID // published, review due 5 days ago, owner role admin
	OverdueRisk       uuid.UUID // review due 3 days ago
	OverdueVendor     uuid.UUID // monthly review, last done 40 days ago, owned by Owner
	OverduePlan       uuid.UUID // open action plan due 2 days ago
	PeriodicControl   uuid.UUID // quarterly manual control, last evaluated 150 days ago (due 30 days ago)
	ExpiringWaiver    uuid.UUID // active exception expiring in 10 days
	UnownedControl    uuid.UUID // in scope, no owner assignment
	UnownedRisk       uuid.UUID // no treatment owner
	DueSoonRisk       uuid.UUID // review due in 7 days
	DueSoonPlan       uuid.UUID // open action plan due in 7 days
	LaterRisk         uuid.UUID // review due in 30 days (upcoming, not due soon)
	CompletedPlan     uuid.UUID // past due but completed: never on the calendar
	WaiverControl     uuid.UUID // the control the two exceptions waive
	AckDueSoon        uuid.UUID // Owner's latest ack of OverduePolicy, lapses in 10 days
	AckOverdue        uuid.UUID // Other's ack of OverduePolicy, lapsed 35 days ago
	AckSuperseded     uuid.UUID // Owner's older ack, superseded by AckDueSoon
	RiskTitlePrefix   string
	PlanTitlePrefix   string
	PolicyTitlePrefix string
}

// Fixture is the two-tenant fixture.
type Fixture struct {
	A, B Tenant
	Now  time.Time
}

// CleanupTables lists, children first, every table the fixture
// writes, for dbtest.SeedTenant's cleanup.
var CleanupTables = []string{
	"control_owner_assignments",
	"control_evaluations",
	"exceptions",
	"policy_acknowledgments",
	"action_plans",
	"risks",
	"org_risk_categories",
	"controls",
	"policies",
	"vendors",
	"users",
}

// Seed seeds both tenants through the privileged migrate
// pool. now anchors every relative date.
func Seed(t *testing.T, admin *pgxpool.Pool, now time.Time) Fixture {
	t.Helper()
	return Fixture{
		A:   seedTenant(t, admin, now, "A"),
		B:   seedTenant(t, admin, now, "B"),
		Now: now,
	}
}

func seedTenant(t *testing.T, admin *pgxpool.Pool, now time.Time, label string) Tenant {
	t.Helper()
	ctx := context.Background()
	day := 24 * time.Hour
	at := Tenant{
		ID:                dbtest.SeedTenant(t, admin, CleanupTables...),
		Owner:             uuid.New(),
		Other:             uuid.New(),
		RiskTitlePrefix:   "tenant " + label + " risk: ",
		PlanTitlePrefix:   "tenant " + label + " plan: ",
		PolicyTitlePrefix: "tenant " + label + " policy: ",
	}
	exec := func(what, sql string, args ...any) {
		t.Helper()
		if _, err := admin.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("attention fixture %s: seed %s: %v", label, what, err)
		}
	}

	for i, u := range []uuid.UUID{at.Owner, at.Other} {
		exec("user", `
			INSERT INTO users (id, tenant_id, email, display_name, status)
			VALUES ($1, $2, $3, $4, 'active')
		`, u, at.ID, "attention-"+u.String()[:8]+"@test.example", []string{"Owner", "Other"}[i]+" "+label)
	}

	control := func(title, implType string, freshness *string) uuid.UUID {
		id := uuid.New()
		exec("control "+title, `
			INSERT INTO controls (
				id, tenant_id, title, control_family, implementation_type,
				bundle_id, evidence_queries, applicability_expr, freshness_class,
				lifecycle_state
			)
			VALUES ($1, $2, $3, 'AAA', $4, $5, '[]'::jsonb, 'true', $6, 'active')
		`, id, at.ID, title, implType, "test-bundle-2130-"+id.String(), freshness)
		return id
	}
	evaluate := func(ctrl uuid.UUID, result string, when time.Time) {
		exec("evaluation", `
			INSERT INTO control_evaluations (
				id, tenant_id, control_id, eval_run_id, evaluated_at,
				result, freshness_status, evidence_count_in_window, trigger
			)
			VALUES ($1, $2, $3, $4, $5, $6, 'fresh', 1, 'manual')
		`, uuid.New(), at.ID, ctrl, uuid.New(), when, result)
	}
	own := func(ctrl uuid.UUID) {
		exec("owner assignment", `
			INSERT INTO control_owner_assignments (tenant_id, control_id, owner_user_id, assigned_by)
			VALUES ($1, $2, $3, $3)
		`, at.ID, ctrl, at.Owner)
	}
	quarterly := "quarterly"

	at.FailingControl = control("failing control", "automated", nil)
	evaluate(at.FailingControl, "fail", now.Add(-1*time.Hour))
	own(at.FailingControl)

	at.PeriodicControl = control("periodic control", "manual_periodic", &quarterly)
	evaluate(at.PeriodicControl, "pass", now.Add(-150*day))
	own(at.PeriodicControl)

	at.UnownedControl = control("unowned control", "automated", nil)

	at.WaiverControl = control("waived control", "automated", nil)
	own(at.WaiverControl)

	exception := func(status string, expires time.Time) uuid.UUID {
		id := uuid.New()
		exec("exception "+status, `
			INSERT INTO exceptions (
				id, tenant_id, control_id, scope_cell_predicate,
				justification, requested_by, requested_at, expires_at, activated_at, status
			)
			VALUES ($1, $2, $3, '{}'::jsonb, 'attention fixture', 'tester', now(), $4,
			        CASE WHEN $5 IN ('active', 'expired', 'revoked') THEN now() END, $5)
		`, id, at.ID, at.WaiverControl, expires, status)
		return id
	}
	at.PendingApproval = exception("requested", now.Add(60*day))
	at.ExpiringWaiver = exception("active", now.Add(10*day))

	// The policy's owner role is a live tenant role, so a user holding it
	// counts the review as theirs (#2134).
	at.OverduePolicy = uuid.New()
	exec("policy", `
		INSERT INTO policies (
			id, tenant_id, title, version, status, effective_date,
			body_md, owner_role, approver_role, created_by,
			published_at, published_by, next_review_at
		)
		VALUES ($1, $2, $3, '1.0.0', 'published', now()::date,
		        '# body', 'admin', 'ciso', 'tester',
		        now(), 'tester', $4)
	`, at.OverduePolicy, at.ID, at.PolicyTitlePrefix+"overdue review", now.Add(-5*day))

	ack := func(user uuid.UUID, when time.Time) uuid.UUID {
		id := uuid.New()
		exec("policy acknowledgment", `
			INSERT INTO policy_acknowledgments (
				id, tenant_id, policy_id, policy_version_id, user_id,
				acknowledged_at, ack_token
			)
			VALUES ($1, $2, $3, $3, $4, $5, $6)
		`, id, at.ID, at.OverduePolicy, user, when, "attention-"+id.String())
		return id
	}
	at.AckSuperseded = ack(at.Owner, now.Add(-700*day))
	at.AckDueSoon = ack(at.Owner, now.Add(-355*day))
	at.AckOverdue = ack(at.Other, now.Add(-400*day))

	// An owned risk names its owner as a user (#2129) as well as in the
	// older free text; the unowned risk has neither.
	risk := func(title, owner string, reviewDue time.Time) uuid.UUID {
		id := uuid.New()
		exec("risk "+title, `
			INSERT INTO risks (
				id, tenant_id, title, category_id, treatment, treatment_owner,
				inherent_score, residual_score, review_due_at, owner_user_id
			)
			VALUES ($1, $2, $3, risk_category_id($2, 'operational'), 'mitigate'::risk_treatment, $4,
			        '{"likelihood": 3, "impact": 4}'::jsonb, '{"likelihood": 2, "impact": 3}'::jsonb, $5,
			        NULLIF($4, '')::uuid)
		`, id, at.ID, at.RiskTitlePrefix+title, owner, reviewDue)
		return id
	}
	at.OverdueRisk = risk("overdue review", at.Owner.String(), now.Add(-3*day))
	at.UnownedRisk = risk("unowned", "", now.Add(120*day))
	at.DueSoonRisk = risk("review due soon", at.Owner.String(), now.Add(7*day))
	at.LaterRisk = risk("review later", at.Owner.String(), now.Add(30*day))

	at.OverdueVendor = uuid.New()
	exec("vendor", `
		INSERT INTO vendors (id, tenant_id, name, criticality, review_cadence, last_review_date, owner_user_id)
		VALUES ($1, $2, $3, 'high', 'monthly', $4, $5)
	`, at.OverdueVendor, at.ID, "tenant "+label+" vendor", now.Add(-40*day), at.Owner)

	plan := func(title, status string, due time.Time) uuid.UUID {
		id := uuid.New()
		exec("action plan "+title, `
			INSERT INTO action_plans (id, tenant_id, title, owner_id, due_date, status)
			VALUES ($1, $2, $3, $4, $5::date, $6)
		`, id, at.ID, at.PlanTitlePrefix+title, at.Owner, due.UTC().Format("2006-01-02"), status)
		return id
	}
	at.OverduePlan = plan("past due", "in_progress", now.Add(-2*day))
	at.DueSoonPlan = plan("due soon", "draft", now.Add(7*day))
	at.CompletedPlan = plan("done", "completed", now.Add(-4*day))

	return at
}
