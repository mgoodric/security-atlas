// Package advisorylock is the registry of every Postgres advisory lock the
// platform takes (#2273).
//
// Every advisory lock uses the two-int form, pg_advisory_xact_lock(ns, key)
// (or its _shared / session variants), where ns is a Namespace registered
// below and key is an int4 within that namespace. Two locks in different
// namespaces can never block each other, whatever their keys. Postgres keeps
// the two-int lock space apart from the single-bigint one, so a stray
// single-bigint lock cannot collide with a registered lock either;
// TestNoUnregisteredAdvisoryLocks keeps that form out of the tree anyway.
//
// To add a lock: register a new Namespace with the next unused value, add it
// to All, and take it through Lock / LockText (Go) or with the literal
// namespace number in SQL. A namespace number is never reused or renumbered:
// two binaries that disagree on a number would not exclude each other.
package advisorylock

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

// Namespace is the first argument of the two-int advisory lock functions.
type Namespace int32

// The registered namespaces. Values are stable; never reuse one.
const (
	// BootstrapFirstInstall serialises OIDC first-installers (#1759,
	// users.Store.BootstrapFirstInstallOrUpsert). Singleton: key 0.
	BootstrapFirstInstall Namespace = 1
	// SuperAdminDemote serialises super_admin demotes so the last one cannot
	// be removed (slice 142, adminsuperadmins). Singleton: key 0.
	SuperAdminDemote Namespace = 2
	// TenantCreateActor serialises one actor's create-tenant requests
	// (slice 143, admintenants). Key: hashtext(actor uuid text).
	TenantCreateActor Namespace = 3
	// TenantCreateInstall serialises tenant creation while the multi-tenant
	// opt-in is off (#2015, admintenants). Singleton: key 0.
	TenantCreateInstall Namespace = 4
	// ThemeVocabulary serialises theme renames/deletes against theme
	// assignments in one tenant (#1811, LockThemeVocabulary[Shared]).
	// Key: hashtext(tenant uuid text).
	ThemeVocabulary Namespace = 5
	// RiskHierarchy serialises hierarchy-link writes in one tenant (#1748,
	// LockRiskHierarchy). Key: hashtext(tenant uuid text).
	RiskHierarchy Namespace = 6
	// AggregationKey serialises evaluations of one rule's meta-risk key
	// (#1861, LockAggregationKey). Key: hashtext(aggregation key).
	AggregationKey Namespace = 7
	// IntegrationTestSeed is for session locks integration tests hold while
	// they wipe and reseed shared fixtures, so test binaries sharing one
	// database serialise. Keys are the TestKey* constants below.
	IntegrationTestSeed Namespace = 8
	// TenantLLMRouting serialises routing writes in one tenant, so each
	// audit row's before value is the state its write replaced (#2329,
	// LockTenantLLMRouting). Key: hashtext(tenant uuid text).
	TenantLLMRouting Namespace = 9
	// FrameworkScopeFreeze serialises framework-scope activation against
	// audit-period freeze on one framework version, so an activation never
	// reads "no freeze" while a freeze commits (#2078,
	// LockFrameworkScopeFreeze). Key: hashtext(tenant uuid || '/' ||
	// framework_version uuid).
	FrameworkScopeFreeze Namespace = 10
	// TenantSSOConfig serialises SSO config writes in one tenant, so each
	// audit row's before value is the state its write replaced (#2609,
	// LockAdminSSO). Key: hashtext(tenant uuid text).
	TenantSSOConfig Namespace = 11
	// ExceptionIdempotencyKey serialises POST /v1/exceptions requests that
	// carry one Idempotency-Key, so a concurrent retry replays the first
	// request instead of filing a second exception (#2618,
	// LockExceptionIdempotencyKey). Key: hashtext(tenant || '|' ||
	// requested_by || '|' || idempotency_key).
	ExceptionIdempotencyKey Namespace = 12
)

// Keys within IntegrationTestSeed. Distinct values; never reuse one.
const (
	TestKeyEvidenceIngest    int32 = 1
	TestKeyEvidenceStreambuf int32 = 2
	TestKeyPolicyAcks        int32 = 3
	TestKeyPolicyAckRate     int32 = 4
	TestKeyControlAttest     int32 = 5
)

// Entry names one registered namespace.
type Entry struct {
	Namespace Namespace
	Name      string
}

// All lists every registered namespace. TestRegistry proves the values and
// names are distinct.
func All() []Entry {
	return []Entry{
		{BootstrapFirstInstall, "bootstrap_first_install"},
		{SuperAdminDemote, "super_admin_demote"},
		{TenantCreateActor, "tenant_create_actor"},
		{TenantCreateInstall, "tenant_create_install"},
		{ThemeVocabulary, "theme_vocabulary"},
		{RiskHierarchy, "risk_hierarchy"},
		{AggregationKey, "aggregation_key"},
		{IntegrationTestSeed, "integration_test_seed"},
		{TenantLLMRouting, "tenant_llm_routing"},
		{FrameworkScopeFreeze, "framework_scope_freeze"},
		{TenantSSOConfig, "tenant_sso_config"},
		{ExceptionIdempotencyKey, "exception_idempotency_key"},
	}
}

// TestKeys lists the IntegrationTestSeed keys, for the distinctness test.
func TestKeys() []int32 {
	return []int32{
		TestKeyEvidenceIngest,
		TestKeyEvidenceStreambuf,
		TestKeyPolicyAcks,
		TestKeyPolicyAckRate,
		TestKeyControlAttest,
	}
}

// Execer is the Exec method shared by pgx.Tx, *pgxpool.Conn and *pgx.Conn.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Lock takes the transaction-scoped lock (ns, key). It blocks until granted
// and is released at commit or rollback.
func Lock(ctx context.Context, ex Execer, ns Namespace, key int32) error {
	if _, err := ex.Exec(ctx, `SELECT pg_advisory_xact_lock($1::int4, $2::int4)`, int32(ns), key); err != nil {
		return fmt.Errorf("advisorylock: lock %d/%d: %w", ns, key, err)
	}
	return nil
}

// LockText takes the transaction-scoped lock (ns, hashtext(key)). A hash
// collision inside one namespace only over-serialises, never under-.
func LockText(ctx context.Context, ex Execer, ns Namespace, key string) error {
	if _, err := ex.Exec(ctx, `SELECT pg_advisory_xact_lock($1::int4, hashtext($2::text))`, int32(ns), key); err != nil {
		return fmt.Errorf("advisorylock: lock %d/%q: %w", ns, key, err)
	}
	return nil
}

// SessionLock takes the session-scoped lock (ns, key) on a dedicated
// connection; release it with SessionUnlock on the same connection.
func SessionLock(ctx context.Context, ex Execer, ns Namespace, key int32) error {
	if _, err := ex.Exec(ctx, `SELECT pg_advisory_lock($1::int4, $2::int4)`, int32(ns), key); err != nil {
		return fmt.Errorf("advisorylock: session lock %d/%d: %w", ns, key, err)
	}
	return nil
}

// SessionUnlock releases a lock SessionLock took on the same connection.
func SessionUnlock(ctx context.Context, ex Execer, ns Namespace, key int32) error {
	if _, err := ex.Exec(ctx, `SELECT pg_advisory_unlock($1::int4, $2::int4)`, int32(ns), key); err != nil {
		return fmt.Errorf("advisorylock: session unlock %d/%d: %w", ns, key, err)
	}
	return nil
}
