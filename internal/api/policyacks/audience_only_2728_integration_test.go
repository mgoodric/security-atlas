//go:build integration

package policyacks_test

// #2728 — only the version's roster can acknowledge. A super admin and a
// member outside the audience are refused (403) and see nothing pending; a
// member of the audience acknowledges; a super admin who also holds a live
// audience role is on the roster and acknowledges like anyone else. The
// acknowledge gate, the pending list and the rate are one set.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

func TestAcknowledge2728_AudienceOnly(t *testing.T) {
	s := setup(t)
	viewers := createAndPublishPolicy(t, s, "2728 Viewers", "1", []string{"viewer"}, s.controlID)
	everyone := createAndPublishPolicy(t, s, "2728 Everyone", "1", []string{"everyone"}, s.controlID)

	pendingIDs := func(bearer string) []string {
		t.Helper()
		raw := s.getJSON(t, "/v1/me/acknowledgments", bearer, http.StatusOK)
		var body struct {
			Pending []struct {
				PolicyID string `json:"policy_id"`
			} `json:"pending"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("unmarshal pending: %v body=%s", err, raw)
		}
		var out []string
		for _, p := range body.Pending {
			out = append(out, p.PolicyID)
		}
		return out
	}

	// The setup admin: is_admin key and super-admin JWT, no user_roles row.
	// Before #2728 the admin wildcard recorded acks the rate never counted.
	if got := pendingIDs(s.adminBear); len(got) != 0 {
		t.Errorf("super admin pending = %v; want none", got)
	}
	for _, id := range []string{viewers, everyone} {
		s.postJSON(t, "/v1/policies/"+id+"/acknowledge", []byte("{}"), s.adminBear, http.StatusForbidden)
	}

	// An auditor is a member (on the everyone roster) but outside the
	// viewer audience.
	_, auditor := s.makeUserAndBearer(t, "auditor-2728@test", []string{"auditor"}, false)
	if got := pendingIDs(auditor); len(got) != 1 || got[0] != everyone {
		t.Errorf("auditor pending = %v; want [%s] (everyone only)", got, everyone)
	}
	s.postJSON(t, "/v1/policies/"+viewers+"/acknowledge", []byte("{}"), auditor, http.StatusForbidden)

	// A viewer is in both audiences.
	_, viewer := s.makeUserAndBearer(t, "viewer-2728@test", []string{"viewer"}, false)
	s.postJSON(t, "/v1/policies/"+viewers+"/acknowledge", []byte("{}"), viewer, http.StatusCreated)

	// A super admin who holds a live viewer row is on the roster.
	adminViewer, adminViewerBear := s.makeUserAndBearer(t, "admin-viewer-2728@test", nil, true)
	seedLiveRoles(t, s.admin, s.tenant, adminViewer, []string{"viewer"})
	s.postJSON(t, "/v1/policies/"+viewers+"/acknowledge", []byte("{}"), adminViewerBear, http.StatusCreated)

	// The rate counts exactly the two accepted acks, out of the two viewers.
	raw := s.getJSON(t, "/v1/policies/"+viewers+"/acknowledgment-rate", s.adminBear, http.StatusOK)
	var rate struct {
		Numerator   int64 `json:"numerator"`
		Denominator int64 `json:"denominator"`
	}
	if err := json.Unmarshal(raw, &rate); err != nil {
		t.Fatalf("unmarshal rate: %v body=%s", err, raw)
	}
	if rate.Numerator != 2 || rate.Denominator != 2 {
		t.Errorf("viewer rate = %d/%d; want 2/2", rate.Numerator, rate.Denominator)
	}
	var recorded int
	if err := s.admin.QueryRow(t.Context(),
		`SELECT count(*) FROM policy_acknowledgments WHERE tenant_id = $1 AND policy_id = $2`,
		s.tenant, viewers).Scan(&recorded); err != nil {
		t.Fatalf("count acks: %v", err)
	}
	if recorded != 2 {
		t.Errorf("recorded acks = %d; want 2 (no off-roster rows)", recorded)
	}
}

// TestGetPolicyForAcknowledge2728_ForShare pins the row lock Record takes:
// while a transaction holds the policy row through GetPolicyForAcknowledge,
// an UPDATE of that row (what a publish does to the version it supersedes)
// waits, so it cannot land between the ack's status check and its insert.
func TestGetPolicyForAcknowledge2728_ForShare(t *testing.T) {
	s := setup(t)
	id := createAndPublishPolicy(t, s, "2728 Lock", "1", []string{"viewer"}, s.controlID)
	ctx := context.Background()

	tx, err := s.admin.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID := uuid.MustParse(s.tenant)
	policyID := uuid.MustParse(id)
	if _, err := dbx.New(tx).GetPolicyForAcknowledge(ctx, dbx.GetPolicyForAcknowledgeParams{
		TenantID: pgUUIDFor(tenantID),
		ID:       pgUUIDFor(policyID),
	}); err != nil {
		t.Fatalf("GetPolicyForAcknowledge: %v", err)
	}

	other, err := s.admin.Begin(ctx)
	if err != nil {
		t.Fatalf("begin other: %v", err)
	}
	defer func() { _ = other.Rollback(ctx) }()
	if _, err := other.Exec(ctx, `SET LOCAL lock_timeout = '200ms'`); err != nil {
		t.Fatalf("lock_timeout: %v", err)
	}
	_, err = other.Exec(ctx, `UPDATE policies SET status = 'superseded' WHERE id = $1`, policyID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
		t.Fatalf("UPDATE under the ack's share lock: err = %v; want lock_not_available (55P03)", err)
	}
}

func pgUUIDFor(u uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: u, Valid: true} }
