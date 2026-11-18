//go:build integration

// #2609: issuing, rotating and revoking an API key each write a me_audit_log
// row in the same transaction: the actor, the key id, last4, flags and roles,
// never the token or its hash.
package auth_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/auth/apikeystore"
	"github.com/mgoodric/security-atlas/internal/auth/bearer"
)

type keyAuditRow struct {
	Action    string
	UserID    uuid.UUID
	Principal *string
	Before    map[string]any
	After     map[string]any
	Raw       string
}

// keyAuditRows reads tenant's API key audit rows, oldest first, as the app
// role under the tenant's RLS context.
func keyAuditRows(t *testing.T, tenant uuid.UUID) []keyAuditRow {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, err := appPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.current_tenant', $1, true)`, tenant.String()); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(ctx, `
		SELECT action, user_id, actor_principal, before, after, to_jsonb(me_audit_log)::text
		FROM me_audit_log
		WHERE action IN ($1, $2, $3)
		ORDER BY occurred_at, id`,
		apikeystore.AuditActionKeyIssue, apikeystore.AuditActionKeyRotate, apikeystore.AuditActionKeyRevoke)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []keyAuditRow
	for rows.Next() {
		var r keyAuditRow
		var before, after []byte
		if err := rows.Scan(&r.Action, &r.UserID, &r.Principal, &before, &after, &r.Raw); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(before, &r.Before); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(after, &r.After); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// cleanupKeys removes the tenants' api_keys rows after the test. The audit
// rows stay: me_audit_log is append-only.
func cleanupKeys(t *testing.T, tenants ...uuid.UUID) {
	t.Helper()
	t.Cleanup(func() {
		if adminPool == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, tn := range tenants {
			_, _ = adminPool.Exec(ctx, "DELETE FROM api_keys WHERE tenant_id = $1", tn)
		}
	})
}

func newAuditKeyStore() *apikeystore.Store {
	s := apikeystore.NewStore(appPool, adminPool, testHasher, 0)
	s.SetPrefix(bearer.PrefixTest)
	return s
}

func TestAPIKeyAudit_IssueRotateRevokeWriteRows(t *testing.T) {
	tenant, user := uuid.New(), uuid.New()
	cleanupKeys(t, tenant)
	ctx := withTenant(t, tenant)
	store := newAuditKeyStore()
	by := apikeystore.Actor{UserID: user, Principal: user.String()}

	pred, predPlain, err := store.Issue(ctx, tenant.String(), apikeystore.IssueInput{
		IsAdmin:      true,
		OwnerRoles:   []string{"auditor"},
		AllowedKinds: []string{"sast.scan_result"},
		TTL:          time.Hour,
	}, by)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	predUUID := uuid.MustParse(strings.TrimPrefix(pred.ID, "key_"))
	succ, succPlain, _, err := store.Rotate(ctx, tenant.String(), predUUID, by)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	succUUID := uuid.MustParse(strings.TrimPrefix(succ.ID, "key_"))
	if err := store.Revoke(ctx, tenant.String(), succUUID, by); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	// Revoking again changes nothing and writes no row.
	if err := store.Revoke(ctx, tenant.String(), succUUID, by); err != nil {
		t.Fatalf("second revoke: %v", err)
	}

	rows := keyAuditRows(t, tenant)
	if len(rows) != 3 {
		t.Fatalf("audit rows = %d, want 3 (issue, rotate, revoke)", len(rows))
	}
	wantActions := []string{apikeystore.AuditActionKeyIssue, apikeystore.AuditActionKeyRotate, apikeystore.AuditActionKeyRevoke}
	for i, row := range rows {
		if row.Action != wantActions[i] {
			t.Errorf("row %d action = %s, want %s", i, row.Action, wantActions[i])
		}
		if row.UserID != user {
			t.Errorf("%s user_id = %s, want %s", row.Action, row.UserID, user)
		}
		if row.Principal == nil || *row.Principal != user.String() {
			t.Errorf("%s actor_principal = %v, want %s", row.Action, row.Principal, user)
		}
	}

	issue := rows[0]
	if len(issue.Before) != 0 {
		t.Errorf("issue before = %v, want {}", issue.Before)
	}
	assertKeyFields(t, "issue after", issue.After, map[string]any{
		"key_id":      pred.ID,
		"last4":       pred.Last4,
		"is_admin":    true,
		"is_approver": false,
		"revoked":     false,
		"ttl_seconds": float64(3600),
	})
	if got, _ := json.Marshal(issue.After["owner_roles"]); string(got) != `["auditor"]` {
		t.Errorf("issue after.owner_roles = %s", got)
	}
	if got, _ := json.Marshal(issue.After["allowed_kinds"]); string(got) != `["sast.scan_result"]` {
		t.Errorf("issue after.allowed_kinds = %s", got)
	}

	rotate := rows[1]
	assertKeyFields(t, "rotate before.predecessor", sub(t, rotate.Before, "predecessor"), map[string]any{"key_id": pred.ID, "last4": pred.Last4})
	if _, ok := sub(t, rotate.Before, "predecessor")["retires_at"]; ok {
		t.Error("rotate before.predecessor carries retires_at; the key was not retiring yet")
	}
	if _, ok := sub(t, rotate.After, "predecessor")["retires_at"]; !ok {
		t.Error("rotate after.predecessor has no retires_at")
	}
	assertKeyFields(t, "rotate after.successor", sub(t, rotate.After, "successor"), map[string]any{
		"key_id":       succ.ID,
		"last4":        succ.Last4,
		"is_admin":     true,
		"rotated_from": pred.ID,
	})

	revoke := rows[2]
	assertKeyFields(t, "revoke before", revoke.Before, map[string]any{"key_id": succ.ID, "revoked": false})
	assertKeyFields(t, "revoke after", revoke.After, map[string]any{"key_id": succ.ID, "last4": succ.Last4, "revoked": true})

	// Never a token, nor a token hash.
	for _, plain := range []string{predPlain, succPlain} {
		hash := hex.EncodeToString(testHasher.Hash(plain))
		for _, row := range rows {
			if strings.Contains(row.Raw, plain) || strings.Contains(row.Raw, hash) {
				t.Errorf("%s row contains a bearer token or its hash: %s", row.Action, row.Raw)
			}
			if strings.Contains(row.Raw, "token") {
				t.Errorf("%s row carries a token key: %s", row.Action, row.Raw)
			}
		}
	}
}

// A failed audit write rolls the issue, rotate and revoke back. The write is
// made to fail for real: actor_principal longer than its CHECK allows.
func TestAPIKeyAudit_FailedAuditRollsBack(t *testing.T) {
	tenant := uuid.New()
	cleanupKeys(t, tenant)
	ctx := withTenant(t, tenant)
	store := newAuditKeyStore()

	seed, _, err := store.Issue(ctx, tenant.String(), apikeystore.IssueInput{}, apikeystore.Actor{Principal: "seed"})
	if err != nil {
		t.Fatalf("seed issue: %v", err)
	}
	seedUUID := uuid.MustParse(strings.TrimPrefix(seed.ID, "key_"))

	bad := apikeystore.Actor{Principal: strings.Repeat("x", 300)}
	if _, _, err := store.Issue(ctx, tenant.String(), apikeystore.IssueInput{}, bad); err == nil {
		t.Error("issue succeeded with a failing audit write")
	}
	if _, _, _, err := store.Rotate(ctx, tenant.String(), seedUUID, bad); err == nil {
		t.Error("rotate succeeded with a failing audit write")
	}
	if err := store.Revoke(ctx, tenant.String(), seedUUID, bad); err == nil {
		t.Error("revoke succeeded with a failing audit write")
	}

	active, err := store.List(ctx, tenant.String())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(active) != 1 || active[0].ID != seed.ID || !active[0].RetiresAt.IsZero() {
		t.Errorf("active keys = %+v; want only %s, neither retiring nor revoked", active, seed.ID)
	}
	if n := len(keyAuditRows(t, tenant)); n != 1 {
		t.Errorf("audit rows = %d, want 1 (the seed issue only)", n)
	}
}

// Another tenant cannot read the audit rows.
func TestAPIKeyAudit_TenantIsolated(t *testing.T) {
	tenantA, tenantB := uuid.New(), uuid.New()
	cleanupKeys(t, tenantA, tenantB)
	store := newAuditKeyStore()
	if _, _, err := store.Issue(withTenant(t, tenantA), tenantA.String(), apikeystore.IssueInput{}, apikeystore.Actor{Principal: "bootstrap-admin"}); err != nil {
		t.Fatalf("issue: %v", err)
	}
	if n := len(keyAuditRows(t, tenantA)); n != 1 {
		t.Fatalf("tenant A audit rows = %d, want 1", n)
	}
	if n := len(keyAuditRows(t, tenantB)); n != 0 {
		t.Errorf("tenant B sees %d of tenant A's API key audit rows", n)
	}
}

func sub(t *testing.T, m map[string]any, key string) map[string]any {
	t.Helper()
	v, ok := m[key].(map[string]any)
	if !ok {
		t.Fatalf("%q is %T, want an object: %v", key, m[key], m)
	}
	return v
}

func assertKeyFields(t *testing.T, label string, got, want map[string]any) {
	t.Helper()
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s.%s = %v, want %v", label, k, got[k], v)
		}
	}
}
