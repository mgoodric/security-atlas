//go:build integration

// #2609: issuing and revoking a SCIM credential each write a me_audit_log row
// in the same transaction: the actor, the credential id and last4, never the
// token or its hash.
package scim_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/auth/bearer"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/scim"
)

type credAuditRow struct {
	Action    string
	UserID    uuid.UUID
	Principal *string
	Before    map[string]any
	After     map[string]any
	Raw       string
}

// credAuditRows reads tenant's SCIM credential audit rows, oldest first, as
// the app role under the tenant's RLS context.
func credAuditRows(t *testing.T, tenant uuid.UUID) []credAuditRow {
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
		WHERE action IN ($1, $2)
		ORDER BY occurred_at, id`, scim.AuditActionCredentialIssue, scim.AuditActionCredentialRevoke)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []credAuditRow
	for rows.Next() {
		var r credAuditRow
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

func newCredStore(t *testing.T) *scim.CredentialStore {
	t.Helper()
	s := scim.NewCredentialStore(appPool, adminPool, hasher(t))
	s.SetPrefix(bearer.PrefixTest)
	return s
}

func TestCredentialAudit_IssueAndRevokeWriteRows(t *testing.T) {
	tenant := seedTenant(t, "scim-cred-audit")
	ctx := tenantCtx(t, tenant)
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	store := newCredStore(t)
	user := uuid.New()
	by := scim.Actor{UserID: &user, Principal: user.String()}

	cred, plain, err := store.Issue(ctx, tenant.String(), "Okta prod", by)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if err := store.Revoke(ctx, tenant.String(), cred.ID, by); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	// Revoking again changes nothing and writes no row.
	if err := store.Revoke(ctx, tenant.String(), cred.ID, by); err != nil {
		t.Fatalf("second revoke: %v", err)
	}

	rows := credAuditRows(t, tenant)
	if len(rows) != 2 {
		t.Fatalf("audit rows = %d, want 2 (issue, revoke; the repeat revoke writes none)", len(rows))
	}
	issue, revoke := rows[0], rows[1]
	if issue.Action != scim.AuditActionCredentialIssue || revoke.Action != scim.AuditActionCredentialRevoke {
		t.Fatalf("actions = %s, %s", issue.Action, revoke.Action)
	}
	for _, row := range rows {
		if row.UserID != user {
			t.Errorf("%s user_id = %s, want %s", row.Action, row.UserID, user)
		}
		if row.Principal == nil || *row.Principal != user.String() {
			t.Errorf("%s actor_principal = %v, want %s", row.Action, row.Principal, user)
		}
	}
	if len(issue.Before) != 0 {
		t.Errorf("issue before = %v, want {}", issue.Before)
	}
	want := map[string]any{
		"credential_id": cred.ID.String(),
		"last4":         cred.Last4,
		"description":   "Okta prod",
		"revoked":       false,
	}
	assertCredFields(t, "issue after", issue.After, want)
	assertCredFields(t, "revoke before", revoke.Before, want)
	want["revoked"] = true
	assertCredFields(t, "revoke after", revoke.After, want)

	// Never the token, nor its hash.
	hash := hex.EncodeToString(hasher(t).Hash(plain))
	for _, row := range rows {
		if strings.Contains(row.Raw, plain) {
			t.Errorf("%s row contains the bearer token: %s", row.Action, row.Raw)
		}
		if strings.Contains(row.Raw, hash) || strings.Contains(row.Raw, "token") {
			t.Errorf("%s row carries a token or token hash: %s", row.Action, row.Raw)
		}
	}
	if strings.Contains(logs.String(), plain) {
		t.Error("the server log contains the bearer token")
	}
}

// A failed audit write rolls the issue and the revoke back.
func TestCredentialAudit_FailedAuditRollsBack(t *testing.T) {
	tenant := seedTenant(t, "scim-cred-audit-rollback")
	ctx := tenantCtx(t, tenant)
	store := newCredStore(t)

	cred, _, err := store.Issue(ctx, tenant.String(), "kept", scim.Actor{})
	if err != nil {
		t.Fatalf("seed issue: %v", err)
	}

	store.SetAuditInsert(func(context.Context, *dbx.Queries, dbx.InsertMeAuditLogParams) error {
		return errors.New("audit write refused")
	})
	if _, _, err := store.Issue(ctx, tenant.String(), "rolled back", scim.Actor{}); err == nil {
		t.Fatal("issue succeeded with a failing audit write")
	}
	if err := store.Revoke(ctx, tenant.String(), cred.ID, scim.Actor{}); err == nil {
		t.Fatal("revoke succeeded with a failing audit write")
	}

	active, err := store.List(ctx, tenant.String())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(active) != 1 || active[0].ID != cred.ID {
		t.Errorf("active credentials = %+v; want only %s, still unrevoked", active, cred.ID)
	}
	if n := len(credAuditRows(t, tenant)); n != 1 {
		t.Errorf("audit rows = %d, want 1 (the seed issue only)", n)
	}
}

// Another tenant cannot read the audit rows.
func TestCredentialAudit_TenantIsolated(t *testing.T) {
	tenantA := seedTenant(t, "scim-cred-audit-a")
	tenantB := seedTenant(t, "scim-cred-audit-b")
	store := newCredStore(t)
	if _, _, err := store.Issue(tenantCtx(t, tenantA), tenantA.String(), "a", scim.Actor{Principal: "bootstrap-admin"}); err != nil {
		t.Fatalf("issue: %v", err)
	}
	if n := len(credAuditRows(t, tenantA)); n != 1 {
		t.Fatalf("tenant A audit rows = %d, want 1", n)
	}
	if n := len(credAuditRows(t, tenantB)); n != 0 {
		t.Errorf("tenant B sees %d of tenant A's SCIM credential audit rows", n)
	}
}

func assertCredFields(t *testing.T, label string, got, want map[string]any) {
	t.Helper()
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s.%s = %v, want %v", label, k, got[k], v)
		}
	}
}
