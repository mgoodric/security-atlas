//go:build integration

package policy_test

// #2762 — the ack-roster predicate lives in four SQL copies: the gate
// (IsUserOnAckRoster), the pending list (the EXISTS in
// ListPendingAcksForUser), the rate denominator
// (CountRequiredRoleUsersForVersion) and the list view's denominator
// (ListPoliciesWithAckRate). On one fixture, for every (user, version) pair,
// the gate and pending-list membership must agree, and every denominator must
// equal the number of users the gate admits. An expected roster per policy
// pins the set itself, so the four cannot drift together either.

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/policy"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func TestAckRoster2762_FourCopiesAgree(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	tenantID := uuid.MustParse(tenant)
	t.Cleanup(func() { cleanupParityUsers(t, admin, tenant) })
	ctrl := seedControl(t, admin, tenant)
	ctx := ctxFor(t, tenant)

	// Users. Principals are bare uuids unless noted.
	viewer := seedParityUser(t, admin, tenant, "viewer", "active")
	multi := seedParityUser(t, admin, tenant, "multi", "active")       // viewer + auditor: counted once
	owner := seedParityUser(t, admin, tenant, "owner", "active")       // control_owner
	prefixed := seedParityUser(t, admin, tenant, "prefixed", "active") // auditor as "user:<uuid>"
	disabled := seedParityUser(t, admin, tenant, "disabled", "disabled")
	noRole := seedParityUser(t, admin, tenant, "norole", "active")
	grantParityRole(t, admin, tenant, viewer.String(), "viewer")
	grantParityRole(t, admin, tenant, multi.String(), "viewer")
	grantParityRole(t, admin, tenant, multi.String(), "auditor")
	grantParityRole(t, admin, tenant, owner.String(), "control_owner")
	grantParityRole(t, admin, tenant, "user:"+prefixed.String(), "auditor")
	grantParityRole(t, admin, tenant, disabled.String(), "viewer")
	// A principal naming no users row (a key, bootstrap-admin) is not a
	// person and is on no roster.
	grantParityRole(t, admin, tenant, "key_"+uuid.NewString(), "viewer")
	users := []uuid.UUID{viewer, multi, owner, prefixed, disabled, noRole}

	member := []uuid.UUID{viewer, multi, owner, prefixed}
	cases := []struct {
		name     string
		audience []string
		roster   []uuid.UUID
	}{
		{"viewers", []string{"viewer"}, []uuid.UUID{viewer, multi}},
		{"auditors-owners", []string{"auditor", "control_owner"}, []uuid.UUID{multi, owner, prefixed}},
		{"everyone", []string{policy.AudienceEveryone}, member},
		{"everyone-and-viewer", []string{policy.AudienceEveryone, "viewer"}, member},
		{"engineers", []string{"grc_engineer"}, nil},
		{"untargeted", []string{}, nil},
	}
	policyIDs := make(map[string]uuid.UUID, len(cases))
	for _, c := range cases {
		policyIDs[c.name] = publishParityPolicy(t, app, ctx, ctrl, "2762 "+c.name, c.audience)
	}

	tx, err := app.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		t.Fatalf("ApplyTenant: %v", err)
	}
	q := dbx.New(tx)
	pgTenant := pgtype.UUID{Bytes: tenantID, Valid: true}

	pending := make(map[uuid.UUID]map[uuid.UUID]bool, len(users))
	for _, u := range users {
		rows, err := q.ListPendingAcksForUser(ctx, dbx.ListPendingAcksForUserParams{
			TenantID: pgTenant,
			UserID:   pgtype.UUID{Bytes: u, Valid: true},
		})
		if err != nil {
			t.Fatalf("ListPendingAcksForUser(%s): %v", u, err)
		}
		pending[u] = make(map[uuid.UUID]bool, len(rows))
		for _, r := range rows {
			pending[u][uuid.UUID(r.ID.Bytes)] = true
		}
	}

	listRows, err := q.ListPoliciesWithAckRate(ctx, dbx.ListPoliciesWithAckRateParams{
		TenantID:        pgTenant,
		FreshnessCutoff: pgtype.Timestamptz{Time: time.Now().Add(-policy.AcknowledgmentFreshness), Valid: true},
	})
	if err != nil {
		t.Fatalf("ListPoliciesWithAckRate: %v", err)
	}
	listDenom := make(map[uuid.UUID]*int64, len(listRows))
	for _, r := range listRows {
		listDenom[uuid.UUID(r.ID.Bytes)] = r.AckDenominator
	}

	for _, c := range cases {
		id := policyIDs[c.name]
		var admitted []uuid.UUID
		for _, u := range users {
			onRoster, err := q.IsUserOnAckRoster(ctx, dbx.IsUserOnAckRosterParams{
				TenantID:      pgTenant,
				UserID:        pgtype.UUID{Bytes: u, Valid: true},
				RequiredRoles: c.audience,
			})
			if err != nil {
				t.Fatalf("IsUserOnAckRoster(%s, %s): %v", c.name, u, err)
			}
			if onRoster != pending[u][id] {
				t.Errorf("%s, user %s: gate = %v, in pending list = %v", c.name, u, onRoster, pending[u][id])
			}
			if onRoster {
				admitted = append(admitted, u)
			}
		}
		if !sameUUIDs(admitted, c.roster) {
			t.Errorf("%s: gate admits %v; want %v", c.name, admitted, c.roster)
		}

		denom, err := q.CountRequiredRoleUsersForVersion(ctx, dbx.CountRequiredRoleUsersForVersionParams{
			TenantID:      pgTenant,
			RequiredRoles: c.audience,
		})
		if err != nil {
			t.Fatalf("CountRequiredRoleUsersForVersion(%s): %v", c.name, err)
		}
		if denom != int64(len(admitted)) {
			t.Errorf("%s: CountRequiredRoleUsersForVersion = %d; gate admits %d", c.name, denom, len(admitted))
		}
		ld, ok := listDenom[id]
		switch {
		case !ok:
			t.Errorf("%s: missing from ListPoliciesWithAckRate", c.name)
		case ld == nil:
			t.Errorf("%s: ListPoliciesWithAckRate denominator is null for a published policy", c.name)
		case *ld != int64(len(admitted)):
			t.Errorf("%s: ListPoliciesWithAckRate denominator = %d; gate admits %d", c.name, *ld, len(admitted))
		}
	}
}

// TestAckRecord2762_RosterBeforeState: an off-roster caller is refused with
// ErrAckNotRequired (403) before Record reveals whether the version is
// current or published (409). An on-roster caller still gets the 409s.
func TestAckRecord2762_RosterBeforeState(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	t.Cleanup(func() { cleanupParityUsers(t, admin, tenant) })
	ctrl := seedControl(t, admin, tenant)
	ctx := ctxFor(t, tenant)

	onRoster := seedParityUser(t, admin, tenant, "viewer", "active")
	offRoster := seedParityUser(t, admin, tenant, "auditor", "active")
	grantParityRole(t, admin, tenant, onRoster.String(), "viewer")
	grantParityRole(t, admin, tenant, offRoster.String(), "auditor")

	in := validCreate([]uuid.UUID{ctrl}, "key_author")
	in.Title = "2762 Draft"
	in.AcknowledgmentRequiredRoles = []string{"viewer"}
	draft, err := policy.NewStore(app).Create(ctx, in)
	if err != nil {
		t.Fatalf("Create draft: %v", err)
	}
	published := publishParityPolicy(t, app, ctx, ctrl, "2762 Published", []string{"viewer"})
	wrongPin := uuid.New()

	acks := policy.NewAckStore(app)
	for _, c := range []struct {
		name   string
		user   uuid.UUID
		policy uuid.UUID
		pin    *uuid.UUID
		want   error
	}{
		{"off roster, draft", offRoster, draft.ID, nil, policy.ErrAckNotRequired},
		{"off roster, wrong pin", offRoster, published, &wrongPin, policy.ErrAckNotRequired},
		{"on roster, draft", onRoster, draft.ID, nil, policy.ErrAckPolicyNotPublished},
		{"on roster, wrong pin", onRoster, published, &wrongPin, policy.ErrAckVersionMismatch},
	} {
		_, err := acks.Record(ctx, policy.RecordInput{
			PolicyID:        c.policy,
			Caller:          policy.AckCaller{UserID: c.user.String()},
			PolicyVersionID: c.pin,
		})
		if !errors.Is(err, c.want) {
			t.Errorf("%s: Record err = %v; want %v", c.name, err, c.want)
		}
	}
}

// cleanupParityUsers drops the users and user_roles rows these tests seed;
// freshTenant's cleanup covers policies and controls only.
func cleanupParityUsers(t *testing.T, admin *pgxpool.Pool, tenant string) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []string{
		`DELETE FROM user_roles WHERE tenant_id = $1`,
		`DELETE FROM users WHERE tenant_id = $1`,
	} {
		if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
			t.Logf("cleanup %s: %v", stmt, err)
		}
	}
}

func seedParityUser(t *testing.T, admin *pgxpool.Pool, tenant, label, status string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	email := label + "-2762-" + id.String()[:8] + "@test"
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO users (id, tenant_id, email, display_name, status)
		VALUES ($1, $2, $3, $4, $5)
	`, id, tenant, email, label, status); err != nil {
		t.Fatalf("seed user %s: %v", label, err)
	}
	return id
}

func grantParityRole(t *testing.T, admin *pgxpool.Pool, tenant, principal, role string) {
	t.Helper()
	if _, err := admin.Exec(context.Background(),
		`INSERT INTO user_roles (tenant_id, user_id, role, granted_by) VALUES ($1, $2, $3, 'test-2762')`,
		tenant, principal, role); err != nil {
		t.Fatalf("grant %s to %s: %v", role, principal, err)
	}
}

func publishParityPolicy(t *testing.T, app *pgxpool.Pool, ctx context.Context, ctrl uuid.UUID, title string, audience []string) uuid.UUID {
	t.Helper()
	store := policy.NewStore(app)
	in := validCreate([]uuid.UUID{ctrl}, "key_author")
	in.Title = title
	in.AcknowledgmentRequiredRoles = audience
	created, err := store.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create %s: %v", title, err)
	}
	if _, err := store.SubmitForReview(ctx, created.ID, submitterPerson); err != nil {
		t.Fatalf("SubmitForReview %s: %v", title, err)
	}
	if _, err := store.Approve(ctx, created.ID, approverPerson); err != nil {
		t.Fatalf("Approve %s: %v", title, err)
	}
	now := time.Now().UTC().Truncate(24 * time.Hour)
	published, err := store.Publish(ctx, created.ID, policy.PublishInput{
		NewVersion:    "1.0.0",
		EffectiveDate: &now,
		PublishedBy:   "key_approver",
	})
	if err != nil {
		t.Fatalf("Publish %s: %v", title, err)
	}
	return published.ID
}

func sameUUIDs(a, b []uuid.UUID) bool {
	if len(a) != len(b) {
		return false
	}
	as := make([]string, len(a))
	bs := make([]string, len(b))
	for i := range a {
		as[i], bs[i] = a[i].String(), b[i].String()
	}
	sort.Strings(as)
	sort.Strings(bs)
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}
