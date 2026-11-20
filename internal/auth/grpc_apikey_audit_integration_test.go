//go:build integration

// #2652: the gRPC AdminCredentialsService issues, rotates and revokes keys
// through the in-memory credstore and its apikeystore.Persister. Each writes
// the same me_audit_log row as the HTTP path (#2609), in the same
// transaction as the change, and never the token or its hash.
package auth_test

import (
	"context"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	adminv1 "github.com/mgoodric/security-atlas/gen/proto/admin/v1"
	"github.com/mgoodric/security-atlas/internal/api/admin"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/auth/apikeystore"
)

// grpcAdmin returns the gRPC admin service over a persisted credstore, and
// that store. pre runs on the store before persistence is attached, which is
// how the boot path mints its memory-only bootstrap credentials.
func grpcAdmin(t *testing.T, pre func(*credstore.Store)) (*admin.Service, *credstore.Store) {
	t.Helper()
	store := credstore.New(time.Minute)
	if pre != nil {
		pre(store)
	}
	if _, err := store.AttachPersistence(apikeystore.NewPersister(appPool, adminPool, testHasher)); err != nil {
		t.Fatalf("AttachPersistence: %v", err)
	}
	return admin.New(store), store
}

func adminCaller(tenant uuid.UUID, userID string) context.Context {
	return authctx.WithCredential(context.Background(), credstore.Credential{
		ID:       "key_grpc-caller",
		TenantID: tenant.String(),
		UserID:   userID,
		IsAdmin:  true,
	})
}

func TestGRPCAdminKeyAudit_IssueRotateRevokeWriteRows(t *testing.T) {
	tenant, user := uuid.New(), uuid.New()
	cleanupKeys(t, tenant)
	var bootID string
	svc, _ := grpcAdmin(t, func(s *credstore.Store) {
		c, _, err := s.IssueAdmin(tenant.String(), 0)
		if err != nil {
			t.Fatalf("IssueAdmin: %v", err)
		}
		bootID = c.ID
	})
	ctx := adminCaller(tenant, user.String())

	issued, err := svc.Issue(ctx, &adminv1.IssueRequest{Kinds: []string{"sast.scan_result"}})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	pred := issued.GetHandle()
	rotated, err := svc.Rotate(ctx, &adminv1.RotateRequest{Id: pred.GetId()})
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	succ := rotated.GetHandle()
	if _, err := svc.Revoke(ctx, &adminv1.RevokeRequest{Id: succ.GetId()}); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	// Revoking again changes nothing and writes no row.
	if _, err := svc.Revoke(ctx, &adminv1.RevokeRequest{Id: succ.GetId()}); err != nil {
		t.Fatalf("second revoke: %v", err)
	}
	// A memory-only bootstrap key has no api_keys row; revoking it is
	// still recorded.
	if _, err := svc.Revoke(ctx, &adminv1.RevokeRequest{Id: bootID}); err != nil {
		t.Fatalf("revoke bootstrap key: %v", err)
	}

	rows := keyAuditRows(t, tenant)
	wantActions := []string{apikeystore.AuditActionKeyIssue, apikeystore.AuditActionKeyRotate, apikeystore.AuditActionKeyRevoke, apikeystore.AuditActionKeyRevoke}
	if len(rows) != len(wantActions) {
		t.Fatalf("audit rows = %d, want %d (issue, rotate, revoke, bootstrap revoke)", len(rows), len(wantActions))
	}
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
		"key_id":      pred.GetId(),
		"last4":       pred.GetLast_4(),
		"is_admin":    false,
		"is_approver": false,
		"revoked":     false,
	})

	rotate := rows[1]
	assertKeyFields(t, "rotate before.predecessor", sub(t, rotate.Before, "predecessor"), map[string]any{"key_id": pred.GetId(), "last4": pred.GetLast_4()})
	if _, ok := sub(t, rotate.Before, "predecessor")["retires_at"]; ok {
		t.Error("rotate before.predecessor carries retires_at; the key was not retiring yet")
	}
	if _, ok := sub(t, rotate.After, "predecessor")["retires_at"]; !ok {
		t.Error("rotate after.predecessor has no retires_at")
	}
	assertKeyFields(t, "rotate after.successor", sub(t, rotate.After, "successor"), map[string]any{
		"key_id":       succ.GetId(),
		"last4":        succ.GetLast_4(),
		"rotated_from": pred.GetId(),
	})

	assertKeyFields(t, "revoke before", rows[2].Before, map[string]any{"key_id": succ.GetId(), "revoked": false})
	assertKeyFields(t, "revoke after", rows[2].After, map[string]any{"key_id": succ.GetId(), "last4": succ.GetLast_4(), "revoked": true})
	assertKeyFields(t, "bootstrap revoke after", rows[3].After, map[string]any{"key_id": bootID, "is_admin": true, "revoked": true})

	// Never a token, nor a token hash.
	for _, plain := range []string{issued.GetBearerToken(), rotated.GetBearerToken()} {
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

// A failed audit write rolls the issue, rotate and revoke back, in the
// database and in memory. The write fails for real: an actor_principal
// longer than its CHECK allows.
func TestGRPCAdminKeyAudit_FailedAuditRollsBack(t *testing.T) {
	tenant := uuid.New()
	cleanupKeys(t, tenant)
	svc, store := grpcAdmin(t, nil)

	seed, err := svc.Issue(adminCaller(tenant, uuid.NewString()), &adminv1.IssueRequest{})
	if err != nil {
		t.Fatalf("seed issue: %v", err)
	}
	seedID := seed.GetHandle().GetId()

	bad := adminCaller(tenant, strings.Repeat("x", 300))
	if _, err := svc.Issue(bad, &adminv1.IssueRequest{}); err == nil {
		t.Error("issue succeeded with a failing audit write")
	}
	if _, err := svc.Rotate(bad, &adminv1.RotateRequest{Id: seedID}); err == nil {
		t.Error("rotate succeeded with a failing audit write")
	}
	if _, err := svc.Revoke(bad, &adminv1.RevokeRequest{Id: seedID}); err == nil {
		t.Error("revoke succeeded with a failing audit write")
	}

	active, err := newAuditKeyStore().List(withTenant(t, tenant), tenant.String())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(active) != 1 || active[0].ID != seedID || !active[0].RetiresAt.IsZero() {
		t.Errorf("active api_keys = %+v; want only %s, neither retiring nor revoked", active, seedID)
	}
	if mem := store.List(tenant.String()); len(mem) != 1 || mem[0].ID != seedID {
		t.Errorf("in-memory keys = %+v; want only %s", mem, seedID)
	}
	if _, err := store.Authenticate(seed.GetBearerToken()); err != nil {
		t.Errorf("seed no longer authenticates: %v", err)
	}
	if n := len(keyAuditRows(t, tenant)); n != 1 {
		t.Errorf("audit rows = %d, want 1 (the seed issue only)", n)
	}
}
