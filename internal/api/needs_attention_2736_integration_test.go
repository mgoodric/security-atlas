//go:build integration

// #2736: a policy never acknowledged is due soon for policy.AckGraceDays
// after it took effect and overdue after, due on the effective date plus
// the grace. GET /v1/needs-attention and GET /v1/me/acknowledgments band
// and date it the same way.
package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/policy"
)

func TestNeedsAttention2736_NeverAcknowledgedEscalates(t *testing.T) {
	e := newNAEnv2134(t)
	ctx := context.Background()
	// Effective dates are written as UTC dates so the expected due date
	// does not depend on the database's time zone.
	today := time.Now().UTC().Truncate(24 * time.Hour)
	seed := func(title string, effective time.Time) uuid.UUID {
		t.Helper()
		id := e.seedAckPolicy2587(t, title, "published", []string{"grc_engineer"})
		if _, err := e.migrate.Exec(ctx,
			`UPDATE policies SET effective_date = $3::date WHERE tenant_id = $1 AND id = $2`,
			e.fx.A.ID, id, effective.Format("2006-01-02")); err != nil {
			t.Fatalf("set effective date: %v", err)
		}
		return id
	}
	// Due today: the due day has not ended, so still due soon. Due
	// yesterday: overdue.
	dueToday := seed("tenant A policy: grace ends today", today.AddDate(0, 0, -policy.AckGraceDays))
	dueYesterday := seed("tenant A policy: grace ended yesterday", today.AddDate(0, 0, -policy.AckGraceDays-1))
	ignored := seed("tenant A policy: ignored two years", today.AddDate(-2, 0, 0))

	want := map[uuid.UUID]struct {
		band, status string
		due          time.Time
	}{
		dueToday:     {"due_soon", "due_soon", today},
		dueYesterday: {"overdue", "overdue", today.AddDate(0, 0, -1)},
		ignored:      {"overdue", "overdue", today.AddDate(-2, 0, policy.AckGraceDays)},
	}

	items := e.ackItems2587(t, e.owner)
	got := e.do(t, http.MethodGet, "/v1/me/acknowledgments", e.owner, nil)
	if got.status != http.StatusOK {
		t.Fatalf("GET /v1/me/acknowledgments: status %d body %s", got.status, got.body)
	}
	var mine struct {
		Pending []struct {
			PolicyID string     `json:"policy_id"`
			Status   string     `json:"status"`
			DueAt    *time.Time `json:"due_at"`
		} `json:"pending"`
	}
	if err := json.Unmarshal([]byte(got.body), &mine); err != nil {
		t.Fatalf("decode /v1/me/acknowledgments: %v", err)
	}
	pending := map[string]int{}
	for i, p := range mine.Pending {
		pending[p.PolicyID] = i
	}

	for id, w := range want {
		it, ok := items[id.String()]
		if !ok || it.Band != w.band || it.DueAt == nil || !it.DueAt.Equal(w.due) {
			t.Errorf("needs-attention %s: listed=%v item %+v; want %s, due %s", id, ok, it, w.band, w.due)
		}
		i, ok := pending[id.String()]
		if !ok {
			t.Errorf("/v1/me/acknowledgments: %s not pending", id)
			continue
		}
		p := mine.Pending[i]
		if p.Status != w.status || p.DueAt == nil || !p.DueAt.Equal(w.due) {
			t.Errorf("/v1/me/acknowledgments %s: status %q due %v; want %s, due %s", id, p.Status, p.DueAt, w.status, w.due)
		}
	}

	// Doing nothing no longer ranks better than lapsing: the ignored
	// policy is in the overdue band, ahead of anything due soon.
	ranked := e.mustList(t, e.owner, "?for=me&limit=100&type=policy_ack").Items
	pos := map[string]int{}
	for i, it := range ranked {
		pos[it.ID] = i
	}
	if pos[ignored.String()] > pos[dueToday.String()] {
		t.Errorf("ignored policy ranks below one still in its grace period: %+v", ranked)
	}
}
