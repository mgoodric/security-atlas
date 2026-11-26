//go:build integration

// #2587 — GET /v1/needs-attention lists a policy waiting for the caller's
// acknowledgement, through the production router on real Postgres under
// RLS, over the attention fixture. Tenant A's Owner holds grc_engineer and
// Other holds admin. The matrix pins:
//
//   - a policy aimed at grc_engineer is Owner's item, not Other's: the
//     admin flag does not make a policy someone's to acknowledge;
//   - a policy aimed at everyone is every member's item;
//   - the fixture's policy, which asks nobody to acknowledge it, and a
//     draft aimed at grc_engineer are nobody's;
//   - an item goes once the caller acknowledges, and a lapsed ack is
//     overdue from the day it lapsed;
//   - with Policies, or its acknowledgements feature, switched off there
//     is no item (#2175).
//
// Requires DATABASE_URL_APP (atlas_app, RLS) and DATABASE_URL (migrate role).
package api_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func (e *naEnv2134) seedAckPolicy2587(t *testing.T, title, status string, roles []string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := e.migrate.Exec(context.Background(), `
		INSERT INTO policies (
			id, tenant_id, title, version, status, effective_date,
			body_md, owner_role, approver_role, created_by,
			published_at, published_by, acknowledgment_required_roles
		)
		VALUES ($1, $2, $3, '1.0.0', $4, CASE WHEN $4 = 'published' THEN now()::date END,
		        '# body', 'ciso', 'ciso', 'tester',
		        CASE WHEN $4 = 'published' THEN now() END,
		        CASE WHEN $4 = 'published' THEN 'tester' END, $5)
	`, id, e.fx.A.ID, title, status, roles); err != nil {
		t.Fatalf("seed policy %s: %v", title, err)
	}
	return id
}

func (e *naEnv2134) ack2587(t *testing.T, policyID, user uuid.UUID, at time.Time) {
	t.Helper()
	if _, err := e.migrate.Exec(context.Background(), `
		INSERT INTO policy_acknowledgments (
			id, tenant_id, policy_id, policy_version_id, user_id, acknowledged_at, ack_token
		)
		VALUES ($1, $2, $3, $3, $4, $5, $6)
	`, uuid.New(), e.fx.A.ID, policyID, user, at, "test-2587-"+uuid.NewString()); err != nil {
		t.Fatalf("seed acknowledgment: %v", err)
	}
}

// ackItems2587 is the caller's for-me policy_ack items, by policy id.
func (e *naEnv2134) ackItems2587(t *testing.T, bearer string) map[string]naItem2134 {
	t.Helper()
	got := e.mustList(t, bearer, "?for=me&limit=100&type=policy_ack")
	out := map[string]naItem2134{}
	for _, it := range got.Items {
		if it.Type != "policy_ack" {
			t.Errorf("type=policy_ack listed a %s", it.Type)
			continue
		}
		out[it.ID] = it
	}
	return out
}

func TestNeedsAttention2587_PolicyAckDue(t *testing.T) {
	e := newNAEnv2134(t)
	engineers := e.seedAckPolicy2587(t, "tenant A policy: for engineers", "published", []string{"grc_engineer"})
	everyone := e.seedAckPolicy2587(t, "tenant A policy: for everyone", "published", []string{"everyone"})
	draft := e.seedAckPolicy2587(t, "tenant A policy: draft for engineers", "draft", []string{"grc_engineer"})
	untargeted := e.fx.A.OverduePolicy

	owner := e.ackItems2587(t, e.owner)
	other := e.ackItems2587(t, e.other)
	for _, c := range []struct {
		name string
		got  map[string]naItem2134
		id   uuid.UUID
		want bool
	}{
		{"Owner, policy for grc_engineer", owner, engineers, true},
		{"Owner, policy for everyone", owner, everyone, true},
		{"Owner, draft", owner, draft, false},
		{"Owner, policy asking nobody", owner, untargeted, false},
		{"Other (admin), policy for grc_engineer", other, engineers, false},
		{"Other (admin), policy for everyone", other, everyone, true},
		{"Other (admin), policy asking nobody", other, untargeted, false},
	} {
		if _, ok := c.got[c.id.String()]; ok != c.want {
			t.Errorf("%s: listed=%v; want %v", c.name, ok, c.want)
		}
	}
	it := owner[engineers.String()]
	if it.Band != "due_soon" || it.DueAt == nil || !it.DueAt.After(time.Now()) || it.Link != "/policies/"+engineers.String() ||
		it.Action != "Acknowledge" || it.Owner == nil || it.Owner.UserID != e.fx.A.Owner.String() {
		t.Errorf("never-acknowledged item = %+v; want due_soon, due in the grace period, linked to the policy, owned by Owner", it)
	}
	if len(e.ackItems2587(t, e.ownerB)) != 0 {
		t.Errorf("tenant B sees tenant A's acknowledgements")
	}

	// Acknowledged: the item goes.
	e.ack2587(t, engineers, e.fx.A.Owner, time.Now().UTC().Add(-time.Hour))
	if _, ok := e.ackItems2587(t, e.owner)[engineers.String()]; ok {
		t.Errorf("Owner acknowledged the engineers' policy; it is still listed")
	}

	// Lapsed: overdue from the day the last ack lapsed.
	lapsedAt := time.Now().UTC().Add(-400 * 24 * time.Hour).Truncate(time.Second)
	e.ack2587(t, everyone, e.fx.A.Other, lapsedAt)
	got, ok := e.ackItems2587(t, e.other)[everyone.String()]
	if want := lapsedAt.Add(365 * 24 * time.Hour); !ok || got.Band != "overdue" || got.DueAt == nil || !got.DueAt.Equal(want) {
		t.Errorf("lapsed ack: listed=%v item %+v; want overdue, due %s", ok, got, want)
	}

	// Switched off, there is nothing to acknowledge here.
	e.setSwitch(t, "policy.acknowledgments", false)
	if n := len(e.ackItems2587(t, e.other)); n != 0 {
		t.Errorf("acknowledgements switched off: %d items; want 0", n)
	}
	e.setSwitch(t, "policy.acknowledgments", true)
	if n := len(e.ackItems2587(t, e.other)); n != 1 {
		t.Errorf("acknowledgements back on: %d items; want 1", n)
	}
	e.setSwitch(t, "policy.enabled", false)
	if n := len(e.ackItems2587(t, e.other)); n != 0 {
		t.Errorf("Policies switched off: %d items; want 0", n)
	}
}

// A re-publish is a new version: an ack of the old version, fresh or
// lapsed, is not an ack of the new one. The new version is never
// acknowledged (due soon, within the grace period), not overdue.
func TestNeedsAttention2587_RepublishNeedsANewAck(t *testing.T) {
	e := newNAEnv2134(t)
	ctx := context.Background()
	v1 := e.seedAckPolicy2587(t, "tenant A policy: v1", "published", []string{"grc_engineer"})
	e.ack2587(t, v1, e.fx.A.Owner, time.Now().UTC().Add(-400*24*time.Hour))
	e.ack2587(t, v1, e.fx.A.Owner, time.Now().UTC().Add(-time.Hour))
	if _, ok := e.ackItems2587(t, e.owner)[v1.String()]; ok {
		t.Fatalf("v1 acknowledged an hour ago is still listed")
	}

	if _, err := e.migrate.Exec(ctx,
		`UPDATE policies SET status = 'superseded', superseded_at = now() WHERE tenant_id = $1 AND id = $2`,
		e.fx.A.ID, v1); err != nil {
		t.Fatalf("supersede v1: %v", err)
	}
	v2 := e.seedAckPolicy2587(t, "tenant A policy: v2", "published", []string{"grc_engineer"})
	if _, err := e.migrate.Exec(ctx,
		`UPDATE policies SET predecessor_id = $3, version = '2.0.0' WHERE tenant_id = $1 AND id = $2`,
		e.fx.A.ID, v2, v1); err != nil {
		t.Fatalf("link v2 to v1: %v", err)
	}

	got := e.ackItems2587(t, e.owner)
	if _, ok := got[v1.String()]; ok {
		t.Errorf("superseded v1 is listed")
	}
	it, ok := got[v2.String()]
	if !ok || it.Band != "due_soon" || it.DueAt == nil || !it.DueAt.After(time.Now()) {
		t.Errorf("v2 after re-publish: listed=%v item %+v; want due_soon, due in the grace period (v1's acks do not count)", ok, it)
	}
}
