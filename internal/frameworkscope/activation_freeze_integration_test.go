//go:build integration

// Integration tests for #2078: a frozen audit period pins the framework-scope
// revision in force at its freeze.
//
//   - Activate refuses an effective_from earlier than the latest frozen
//     audit period's frozen_at on the framework version (HTTP 409).
//   - activated_at records the activation. General as-of reads still
//     honour a backdated effective_from where no freeze is crossed.
//
// The SSP side (a frozen period's re-export is unchanged after a later
// activation) is in internal/oscal/excluded_requirements_ssp_integration_test.go.

package frameworkscope_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/audit/period"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/frameworkscope"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// seedAuditPeriod inserts an audit period on the framework version, frozen
// at frozenAt when frozen is true, and removes it at cleanup.
func seedAuditPeriod(t *testing.T, admin *pgxpool.Pool, tenant string, fvID uuid.UUID, frozen bool, frozenAt time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	ctx := context.Background()
	if _, err := admin.Exec(ctx,
		`INSERT INTO audit_periods (id, tenant_id, name, framework_version_id, period_start, period_end, status, created_by)
		 VALUES ($1, $2, $6, $3, $4, $5, 'open', 'tester')`,
		id, tenant, fvID, frozenAt.Add(-90*24*time.Hour), frozenAt, "period "+id.String()); err != nil {
		t.Fatalf("seed audit period: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), `DELETE FROM audit_periods WHERE id = $1`, id); err != nil {
			t.Logf("cleanup audit period: %v", err)
		}
	})
	if !frozen {
		return id
	}
	if _, err := admin.Exec(ctx,
		`UPDATE audit_periods SET status='frozen', frozen_at=$2, frozen_by='tester',
		        frozen_hash=decode('00','hex') WHERE id=$1`, id, frozenAt); err != nil {
		t.Fatalf("freeze audit period: %v", err)
	}
	return id
}

// seedOtherFrameworkVersion is seedFrameworkVersion under a slug of its own,
// for a second framework version in the same tenant.
func seedOtherFrameworkVersion(t *testing.T, admin *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	fwID, fvID := uuid.New(), uuid.New()
	if err := withAdminTenant(admin, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO frameworks (id, tenant_id, name, slug, issuer)
			VALUES ($1, $2, 'Other Framework', $3, 'tester')`, fwID, tenant, "fw-"+fwID.String()); err != nil {
			return fmt.Errorf("insert framework: %w", err)
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
			VALUES ($1, $2, $3, '2022', 'current')`, fvID, tenant, fwID)
		return err
	}); err != nil {
		t.Fatalf("seed other framework_version: %v", err)
	}
	return fvID
}

// mustApprove takes a fresh revision to `approved`.
func mustApprove(t *testing.T, ctx context.Context, store *frameworkscope.Store, fvID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	d, err := store.Create(ctx, frameworkscope.CreateRequest{
		FrameworkVersionID: fvID, Name: name, Predicate: []byte(`{"op":"true"}`),
	})
	if err != nil {
		t.Fatalf("Create %s: %v", name, err)
	}
	if _, err := store.Submit(ctx, d.ID, "key_test", "key_test"); err != nil {
		t.Fatalf("Submit %s: %v", name, err)
	}
	if _, err := store.Approve(ctx, frameworkscope.ApproveRequest{ID: d.ID, ApproverUserID: uuid.NewString()}); err != nil {
		t.Fatalf("Approve %s: %v", name, err)
	}
	return d.ID
}

// TestHTTP_ActivateRefusesEffectiveFromBeforeFreeze: 409 for an
// effective_from earlier than the latest frozen period's freeze; the
// freeze itself and later are accepted. An open period, or a frozen period
// on another framework version, does not block.
func TestHTTP_ActivateRefusesEffectiveFromBeforeFreeze(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	fvID := seedFrameworkVersion(t, admin, tenant)
	otherFV := seedOtherFrameworkVersion(t, admin, tenant)
	store := frameworkscope.NewStore(app)
	ctx, _ := tenancy.WithTenant(context.Background(), tenant)

	older := time.Now().UTC().Add(-72 * time.Hour).Truncate(time.Second)
	latest := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Second)
	seedAuditPeriod(t, admin, tenant, fvID, true, older)
	seedAuditPeriod(t, admin, tenant, fvID, true, latest)
	seedAuditPeriod(t, admin, tenant, fvID, false, time.Now().UTC().Add(-time.Hour))
	seedAuditPeriod(t, admin, tenant, otherFV, true, time.Now().UTC().Add(-time.Hour))

	ts, bearer, _ := setupHTTPServer(t, tenant)
	activate := func(id uuid.UUID, effective time.Time) (int, string) {
		t.Helper()
		resp, body := doJSON(t, http.MethodPatch, fmt.Sprintf("%s/v1/framework-scopes/%s/activate", ts.URL, id),
			bearer, fmt.Sprintf(`{"effective_from":%q}`, effective.Format(time.RFC3339)))
		return resp.StatusCode, string(body)
	}

	// Between the two freezes: earlier than the latest one, refused.
	refused := mustApprove(t, ctx, store, fvID, "backdated")
	if code, body := activate(refused, latest.Add(-time.Hour)); code != http.StatusConflict {
		t.Fatalf("effective_from = latest freeze - 1h: status %d, want 409; body %s", code, body)
	}
	if got, err := store.Get(ctx, refused); err != nil || got.State != frameworkscope.StateApproved {
		t.Fatalf("refused revision: state %q err %v, want still approved", got.State, err)
	}
	if _, err := store.Activate(ctx, refused, "key_test", latest.Add(-time.Second)); !errors.Is(err, frameworkscope.ErrEffectiveBeforeFreeze) {
		t.Fatalf("store Activate before freeze: err = %v, want ErrEffectiveBeforeFreeze", err)
	}

	// At the latest freeze: refused too, so an as_of at frozen_at never
	// picks a revision activated after it.
	if code, body := activate(refused, latest); code != http.StatusConflict {
		t.Fatalf("effective_from = latest freeze: status %d, want 409; body %s", code, body)
	}

	// After the latest freeze: accepted.
	if code, body := activate(refused, latest.Add(time.Second)); code != http.StatusOK {
		t.Fatalf("effective_from = latest freeze + 1s: status %d, want 200; body %s", code, body)
	}

	// The other framework version's freeze is an hour ago; this one has none.
	other := mustApprove(t, ctx, store, otherFV, "other version")
	if code, body := activate(other, time.Now().UTC().Add(-48*time.Hour)); code != http.StatusConflict {
		t.Fatalf("other version, before its freeze: status %d, want 409; body %s", code, body)
	}
	unfrozenFV := seedOtherFrameworkVersion(t, admin, tenant)
	free := mustApprove(t, ctx, store, unfrozenFV, "no freeze")
	if code, body := activate(free, time.Now().UTC().Add(-48*time.Hour)); code != http.StatusOK {
		t.Fatalf("version with no frozen period, backdated: status %d, want 200; body %s", code, body)
	}
}

// TestAsOf_BackdatingWithoutFreeze: with no frozen period, backdating is
// allowed, activated_at records the activation itself, and the live and
// as-of views honour the backdated effective_from.
func TestAsOf_BackdatingWithoutFreeze(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	fvID := seedFrameworkVersion(t, admin, tenant)
	store := frameworkscope.NewStore(app)
	ctx, _ := tenancy.WithTenant(context.Background(), tenant)

	id := mustApprove(t, ctx, store, fvID, "backdated")
	if _, err := store.Activate(ctx, id, "key_test", time.Now().UTC().Add(-48*time.Hour)); err != nil {
		t.Fatalf("Activate backdated: %v", err)
	}

	var activatedAt time.Time
	if err := admin.QueryRow(context.Background(),
		`SELECT activated_at FROM framework_scopes WHERE id = $1`, id).Scan(&activatedAt); err != nil {
		t.Fatalf("read activated_at: %v", err)
	}
	if time.Since(activatedAt) > time.Minute {
		t.Fatalf("activated_at = %v, want the activation time (now)", activatedAt)
	}

	if live, err := store.Activated(ctx, fvID); err != nil || live.ID != id {
		t.Fatalf("Activated: %v %v, want %s", live.ID, err, id)
	}
	if got, err := store.AsOf(ctx, fvID, time.Now().UTC().Add(-24*time.Hour)); err != nil || got.ID != id {
		t.Fatalf("AsOf a day ago: %v %v, want %s", got.ID, err, id)
	}
}

// TestSoAExport_AsOfFrozenAt: an activation effective at the freeze is
// refused, so the SoA at ?as_of=frozen_at keeps the revision in force then.
func TestSoAExport_AsOfFrozenAt(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	fvID := seedFrameworkVersion(t, admin, tenant)
	store := frameworkscope.NewStore(app)
	ctx, _ := tenancy.WithTenant(context.Background(), tenant)

	revA := mustApprove(t, ctx, store, fvID, "rev A")
	if _, err := store.Activate(ctx, revA, "key_test", time.Now().UTC().Add(-48*time.Hour)); err != nil {
		t.Fatalf("Activate rev A: %v", err)
	}
	frozenAt := time.Now().UTC().Truncate(time.Second)
	seedAuditPeriod(t, admin, tenant, fvID, true, frozenAt)

	revB := mustApprove(t, ctx, store, fvID, "rev B")
	if _, err := store.Activate(ctx, revB, "key_test", frozenAt); !errors.Is(err, frameworkscope.ErrEffectiveBeforeFreeze) {
		t.Fatalf("Activate rev B at frozen_at: err = %v, want ErrEffectiveBeforeFreeze", err)
	}

	exp, err := store.SoAExport(ctx, fvID, frameworkscope.SoASelector{AsOf: &frozenAt})
	if err != nil {
		t.Fatalf("SoAExport as_of frozen_at: %v", err)
	}
	if exp.Applicability.Scope == nil || exp.Applicability.Scope.ID != revA {
		t.Fatalf("SoA as_of frozen_at scope = %+v, want rev A %s", exp.Applicability.Scope, revA)
	}
}

// holdFreezeLock takes the (tenant, framework version) activation/freeze
// lock in an admin transaction and returns it; the caller ends it.
func holdFreezeLock(t *testing.T, admin *pgxpool.Pool, tenant string, fvID uuid.UUID) pgx.Tx {
	t.Helper()
	tx, err := admin.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	if _, err := tx.Exec(context.Background(),
		`SELECT pg_advisory_xact_lock(10, hashtext($1::uuid::text || '/' || $2::uuid::text))`, tenant, fvID); err != nil {
		t.Fatalf("take freeze lock: %v", err)
	}
	return tx
}

// stillBlocked fails the test if done delivers within the grace period.
func stillBlocked[T any](t *testing.T, done <-chan T, what string) {
	t.Helper()
	select {
	case v := <-done:
		t.Fatalf("%s returned while the freeze lock was held: %v", what, v)
	case <-time.After(300 * time.Millisecond):
	}
}

// TestActivate_SerialisedAgainstFreeze forces the interleaving: a freeze
// holds the lock and commits while Activate waits. Activate must read the
// freeze after it commits and refuse; without the lock it would read "no
// freeze" and activate across it.
func TestActivate_SerialisedAgainstFreeze(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	fvID := seedFrameworkVersion(t, admin, tenant)
	store := frameworkscope.NewStore(app)
	ctx, _ := tenancy.WithTenant(context.Background(), tenant)
	periodID := seedAuditPeriod(t, admin, tenant, fvID, false, time.Now().UTC())
	id := mustApprove(t, ctx, store, fvID, "racing")

	freeze := holdFreezeLock(t, admin, tenant, fvID)
	done := make(chan error, 1)
	go func() {
		_, err := store.Activate(ctx, id, "key_test", time.Now().UTC().Add(-time.Hour))
		done <- err
	}()
	stillBlocked(t, done, "Activate")

	if _, err := freeze.Exec(context.Background(),
		`UPDATE audit_periods SET status='frozen', frozen_at=now(), frozen_by='tester',
		        frozen_hash=decode('00','hex') WHERE id=$1`, periodID); err != nil {
		t.Fatalf("freeze: %v", err)
	}
	if err := freeze.Commit(context.Background()); err != nil {
		t.Fatalf("commit freeze: %v", err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, frameworkscope.ErrEffectiveBeforeFreeze) {
			t.Fatalf("Activate after the freeze committed: err = %v, want ErrEffectiveBeforeFreeze", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Activate did not return after the lock was released")
	}
}

// TestFreeze_TakesFreezeLock: period.Store.Freeze waits on the same lock.
func TestFreeze_TakesFreezeLock(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	fvID := seedFrameworkVersion(t, admin, tenant)
	ctx, _ := tenancy.WithTenant(context.Background(), tenant)
	periodID := seedAuditPeriod(t, admin, tenant, fvID, false, time.Now().UTC())
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DELETE FROM audit_period_audit_log WHERE tenant_id = $1`, tenant)
	})

	lock := holdFreezeLock(t, admin, tenant, fvID)
	done := make(chan error, 1)
	go func() {
		_, err := period.NewStore(app).Freeze(ctx, periodID, "tester", time.Time{})
		done <- err
	}()
	stillBlocked(t, done, "Freeze")

	if err := lock.Rollback(context.Background()); err != nil {
		t.Fatalf("release lock: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Freeze after the lock was released: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Freeze did not return after the lock was released")
	}
}
