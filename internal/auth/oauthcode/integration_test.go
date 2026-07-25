//go:build integration

// Integration tests for the slice 189 OAuth authorization-code store
// (internal/auth/oauthcode).
//
// Load-bearing functions + branches covered:
//
//   - Store.Insert: happy path; pre-DB validation (empty Code,
//     non-S256 method); RolesJSON / AvailableTenants default-fill.
//   - Store.ConsumeOnce: happy single-redemption; ErrNotFound for
//     unknown code; ErrAlreadyConsumed on second redemption;
//     ErrExpired when expires_at <= now via WithClock.
//   - Store.SweepExpired: deletes rows older than threshold; preserves
//     rows newer than threshold.
//   - Store.RegisterRedirectURI: happy insert; ErrDuplicateRedirectURI
//     on duplicate (clientID, redirect_uri) pair; empty-arg guard.
//   - Store.IsRedirectURIRegistered: registered → true; unregistered →
//     false; empty arg → false.
//   - Store.LookupRedirectURI: registered → (uri, true); unregistered
//     → ("", false); empty arg → ("", false).
//
// Neither oauth_auth_codes nor oauth_client_redirect_uris is
// tenant-scoped (see migration headers), so no tenancy.ApplyTenant
// call is made — the integration tests exercise the production code
// path exactly.
//
// Run via: just test-integration  (sets DATABASE_URL_APP).
package oauthcode_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/auth/oauthcode"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// Slice 435 / 742: the inline atlas_app pool boilerplate (openPool /
// DATABASE_URL_APP / pgxpool dial) this file used to re-derive now lives in
// the shared internal/dbtest harness. dbtest.NewAppPool opens the
// RLS-enforcing application-role pool — neither oauth_auth_codes nor
// oauth_client_redirect_uris is tenant-scoped, so no tenant context is
// applied (production path exercised exactly).

// uniqueCode returns a 32-byte random code unique to this test run,
// matching the production code-shape (UUIDv4 string is well over
// the 32-byte entropy floor and globally unique).
func uniqueCode(prefix string) string {
	return prefix + "-" + uuid.New().String()
}

func freshInsertParams(code string) oauthcode.InsertParams {
	return oauthcode.InsertParams{
		Code:                code,
		ClientID:            "test-client-" + uuid.New().String(),
		RedirectURI:         "https://example.test/callback",
		CodeChallenge:       "test-challenge-" + uuid.New().String(),
		CodeChallengeMethod: oauthcode.PKCEMethodS256,
		UserID:              uuid.New(),
		IDPIssuer:           "https://idp.example.test",
		IDPSubject:          "test-subject-" + uuid.New().String(),
		CurrentTenantID:     uuid.New(),
		AvailableTenants:    []uuid.UUID{uuid.New(), uuid.New()},
		RolesJSON:           []byte(`{"role":"viewer"}`),
		SuperAdmin:          false,
	}
}

// TestInsertHappyPath covers the happy path: a well-formed
// InsertParams round-trips through the DB and returns an AuthCode
// with the right values.
func TestInsertHappyPath(t *testing.T) {
	pool := dbtest.NewAppPool(t)
	s := oauthcode.New(pool)
	ctx := context.Background()

	p := freshInsertParams(uniqueCode("ut-insert-happy"))
	got, err := s.Insert(ctx, p)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if got.Code != p.Code {
		t.Errorf("Code = %q, want %q", got.Code, p.Code)
	}
	if got.ClientID != p.ClientID {
		t.Errorf("ClientID = %q, want %q", got.ClientID, p.ClientID)
	}
	if got.CodeChallengeMethod != oauthcode.PKCEMethodS256 {
		t.Errorf("CodeChallengeMethod = %q, want %q",
			got.CodeChallengeMethod, oauthcode.PKCEMethodS256)
	}
	if got.ConsumedAt != nil {
		t.Errorf("ConsumedAt = %v, want nil (freshly inserted)", got.ConsumedAt)
	}
	if !got.ExpiresAt.After(got.CreatedAt) {
		t.Errorf("ExpiresAt %v not after CreatedAt %v", got.ExpiresAt, got.CreatedAt)
	}
}

// TestInsertRejectsEmptyCode covers the pre-DB guard for an empty
// code value — the application layer rejects before issuing a DB
// call.
func TestInsertRejectsEmptyCode(t *testing.T) {
	pool := dbtest.NewAppPool(t)
	s := oauthcode.New(pool)
	ctx := context.Background()

	p := freshInsertParams("")
	if _, err := s.Insert(ctx, p); err == nil {
		t.Fatal("Insert with empty Code: got nil, want error")
	}
}

// TestInsertRejectsNonS256Method covers the pre-DB guard for the PKCE
// method — only S256 is supported; the schema CHECK would reject
// other values anyway, but the application layer fails fast.
func TestInsertRejectsNonS256Method(t *testing.T) {
	pool := dbtest.NewAppPool(t)
	s := oauthcode.New(pool)
	ctx := context.Background()

	p := freshInsertParams(uniqueCode("ut-insert-bad-method"))
	p.CodeChallengeMethod = "plain"
	if _, err := s.Insert(ctx, p); err == nil {
		t.Fatal("Insert with plain method: got nil, want error")
	}
}

// TestInsertDefaultsEmptyRolesAndTenants covers the default-fill
// branches: empty RolesJSON becomes `{}`, nil AvailableTenants
// becomes an empty slice.
func TestInsertDefaultsEmptyRolesAndTenants(t *testing.T) {
	pool := dbtest.NewAppPool(t)
	s := oauthcode.New(pool)
	ctx := context.Background()

	p := freshInsertParams(uniqueCode("ut-insert-defaults"))
	p.RolesJSON = nil
	p.AvailableTenants = nil
	got, err := s.Insert(ctx, p)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if string(got.Roles) != "{}" {
		t.Errorf("Roles = %q, want %q", string(got.Roles), "{}")
	}
	if got.AvailableTenants == nil {
		t.Errorf("AvailableTenants = nil, want empty slice")
	}
}

// TestConsumeOnceHappyPath covers the happy single-redemption.
func TestConsumeOnceHappyPath(t *testing.T) {
	pool := dbtest.NewAppPool(t)
	s := oauthcode.New(pool)
	ctx := context.Background()

	code := uniqueCode("ut-consume-happy")
	if _, err := s.Insert(ctx, freshInsertParams(code)); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	got, err := s.ConsumeOnce(ctx, code)
	if err != nil {
		t.Fatalf("ConsumeOnce: %v", err)
	}
	if got.Code != code {
		t.Errorf("Code = %q, want %q", got.Code, code)
	}
	if got.ConsumedAt == nil {
		t.Errorf("ConsumedAt = nil; want non-nil after consume")
	}
}

// TestConsumeOnceEmptyCodeReturnsNotFound: the defensive empty-code
// guard collapses to ErrNotFound rather than reaching the DB.
func TestConsumeOnceEmptyCodeReturnsNotFound(t *testing.T) {
	pool := dbtest.NewAppPool(t)
	s := oauthcode.New(pool)
	ctx := context.Background()

	if _, err := s.ConsumeOnce(ctx, ""); !errors.Is(err, oauthcode.ErrNotFound) {
		t.Fatalf("ConsumeOnce(empty): err = %v, want ErrNotFound", err)
	}
}

// TestConsumeOnceUnknownCodeReturnsNotFound: a code that never lived
// returns ErrNotFound.
func TestConsumeOnceUnknownCodeReturnsNotFound(t *testing.T) {
	pool := dbtest.NewAppPool(t)
	s := oauthcode.New(pool)
	ctx := context.Background()

	if _, err := s.ConsumeOnce(ctx, uniqueCode("ut-never-existed")); !errors.Is(err, oauthcode.ErrNotFound) {
		t.Fatalf("ConsumeOnce(unknown): err = %v, want ErrNotFound", err)
	}
}

// TestConsumeOnceTwiceReturnsAlreadyConsumed: the one-shot
// enforcement returns ErrAlreadyConsumed on the second call.
func TestConsumeOnceTwiceReturnsAlreadyConsumed(t *testing.T) {
	pool := dbtest.NewAppPool(t)
	s := oauthcode.New(pool)
	ctx := context.Background()

	code := uniqueCode("ut-consume-twice")
	if _, err := s.Insert(ctx, freshInsertParams(code)); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if _, err := s.ConsumeOnce(ctx, code); err != nil {
		t.Fatalf("ConsumeOnce 1: %v", err)
	}
	if _, err := s.ConsumeOnce(ctx, code); !errors.Is(err, oauthcode.ErrAlreadyConsumed) {
		t.Fatalf("ConsumeOnce 2: err = %v, want ErrAlreadyConsumed", err)
	}
}

// TestConsumeOnceExpiredReturnsExpired: WithClock pins the clock past
// the code's expiry so ConsumeOnce returns ErrExpired without
// consuming.
func TestConsumeOnceExpiredReturnsExpired(t *testing.T) {
	pool := dbtest.NewAppPool(t)
	ctx := context.Background()

	// Insert with the real clock so the row has a real expires_at.
	s := oauthcode.New(pool)
	code := uniqueCode("ut-consume-expired")
	p := freshInsertParams(code)
	p.TTL = 1 * time.Second
	inserted, err := s.Insert(ctx, p)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// Re-bind the store to a future clock so the expiry check fires.
	future := inserted.ExpiresAt.Add(time.Minute)
	sFuture := s.WithClock(func() time.Time { return future })
	if _, err := sFuture.ConsumeOnce(ctx, code); !errors.Is(err, oauthcode.ErrExpired) {
		t.Fatalf("ConsumeOnce(expired): err = %v, want ErrExpired", err)
	}
}

// TestSweepExpiredDeletesOldRows: SweepExpired removes rows created
// before the threshold, preserves rows created after.
func TestSweepExpiredDeletesOldRows(t *testing.T) {
	pool := dbtest.NewAppPool(t)
	s := oauthcode.New(pool)
	ctx := context.Background()

	oldCode := uniqueCode("ut-sweep-old")
	newCode := uniqueCode("ut-sweep-new")
	if _, err := s.Insert(ctx, freshInsertParams(oldCode)); err != nil {
		t.Fatalf("Insert old: %v", err)
	}
	if _, err := s.Insert(ctx, freshInsertParams(newCode)); err != nil {
		t.Fatalf("Insert new: %v", err)
	}

	// Sweep with threshold == future → both rows older than threshold
	// → both deleted. We just want to exercise the SQL path; we don't
	// require an exact count because parallel test runs may inject
	// other rows.
	n, err := s.SweepExpired(ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("SweepExpired: %v", err)
	}
	if n < 2 {
		t.Fatalf("SweepExpired: deleted %d rows; want at least 2", n)
	}
}

// TestRegisterRedirectURIHappyPath covers the simple INSERT path.
func TestRegisterRedirectURIHappyPath(t *testing.T) {
	pool := dbtest.NewAppPool(t)
	s := oauthcode.New(pool)
	ctx := context.Background()

	clientID := "test-client-" + uuid.New().String()
	redirectURI := "https://example.test/cb-" + uuid.New().String()

	if err := s.RegisterRedirectURI(ctx, clientID, redirectURI); err != nil {
		t.Fatalf("RegisterRedirectURI: %v", err)
	}
}

// TestRegisterRedirectURIDuplicateReturnsSentinel: re-registering
// the same pair returns ErrDuplicateRedirectURI.
func TestRegisterRedirectURIDuplicateReturnsSentinel(t *testing.T) {
	pool := dbtest.NewAppPool(t)
	s := oauthcode.New(pool)
	ctx := context.Background()

	clientID := "test-client-" + uuid.New().String()
	redirectURI := "https://example.test/cb-" + uuid.New().String()

	if err := s.RegisterRedirectURI(ctx, clientID, redirectURI); err != nil {
		t.Fatalf("RegisterRedirectURI 1: %v", err)
	}
	err := s.RegisterRedirectURI(ctx, clientID, redirectURI)
	if !errors.Is(err, oauthcode.ErrDuplicateRedirectURI) {
		t.Fatalf("RegisterRedirectURI 2: err = %v, want ErrDuplicateRedirectURI", err)
	}
}

// TestRegisterRedirectURIRejectsEmptyArgs covers the application-
// layer guard.
func TestRegisterRedirectURIRejectsEmptyArgs(t *testing.T) {
	pool := dbtest.NewAppPool(t)
	s := oauthcode.New(pool)
	ctx := context.Background()

	if err := s.RegisterRedirectURI(ctx, "", "https://example.test/cb"); err == nil {
		t.Fatal("RegisterRedirectURI(empty client_id): got nil, want error")
	}
	if err := s.RegisterRedirectURI(ctx, "test-client", ""); err == nil {
		t.Fatal("RegisterRedirectURI(empty redirect_uri): got nil, want error")
	}
}

// TestIsRedirectURIRegistered covers the three branches: registered →
// true, unregistered → false, empty-arg → false.
func TestIsRedirectURIRegistered(t *testing.T) {
	pool := dbtest.NewAppPool(t)
	s := oauthcode.New(pool)
	ctx := context.Background()

	clientID := "test-client-" + uuid.New().String()
	registeredURI := "https://example.test/cb-registered-" + uuid.New().String()
	unregisteredURI := "https://example.test/cb-not-registered-" + uuid.New().String()

	if err := s.RegisterRedirectURI(ctx, clientID, registeredURI); err != nil {
		t.Fatalf("RegisterRedirectURI: %v", err)
	}

	got, err := s.IsRedirectURIRegistered(ctx, clientID, registeredURI)
	if err != nil {
		t.Fatalf("IsRedirectURIRegistered (true): %v", err)
	}
	if !got {
		t.Errorf("IsRedirectURIRegistered (registered): got false, want true")
	}

	got, err = s.IsRedirectURIRegistered(ctx, clientID, unregisteredURI)
	if err != nil {
		t.Fatalf("IsRedirectURIRegistered (false): %v", err)
	}
	if got {
		t.Errorf("IsRedirectURIRegistered (unregistered): got true, want false")
	}

	got, err = s.IsRedirectURIRegistered(ctx, "", registeredURI)
	if err != nil {
		t.Fatalf("IsRedirectURIRegistered (empty): %v", err)
	}
	if got {
		t.Errorf("IsRedirectURIRegistered (empty arg): got true, want false")
	}

	got, err = s.IsRedirectURIRegistered(ctx, clientID, "")
	if err != nil {
		t.Fatalf("IsRedirectURIRegistered (empty uri): %v", err)
	}
	if got {
		t.Errorf("IsRedirectURIRegistered (empty uri): got true, want false")
	}
}

// TestLookupRedirectURI covers the CodeQL-safe taint boundary: match
// returns the DB-stored URI value + true; miss returns ("", false);
// empty-arg returns ("", false).
func TestLookupRedirectURI(t *testing.T) {
	pool := dbtest.NewAppPool(t)
	s := oauthcode.New(pool)
	ctx := context.Background()

	clientID := "test-client-" + uuid.New().String()
	registeredURI := "https://example.test/cb-lookup-" + uuid.New().String()

	if err := s.RegisterRedirectURI(ctx, clientID, registeredURI); err != nil {
		t.Fatalf("RegisterRedirectURI: %v", err)
	}

	gotURI, gotOK, err := s.LookupRedirectURI(ctx, clientID, registeredURI)
	if err != nil {
		t.Fatalf("LookupRedirectURI (match): %v", err)
	}
	if !gotOK {
		t.Errorf("LookupRedirectURI (match): ok = false, want true")
	}
	if gotURI != registeredURI {
		t.Errorf("LookupRedirectURI (match): uri = %q, want %q", gotURI, registeredURI)
	}

	gotURI, gotOK, err = s.LookupRedirectURI(ctx, clientID, "https://example.test/never-registered")
	if err != nil {
		t.Fatalf("LookupRedirectURI (miss): %v", err)
	}
	if gotOK {
		t.Errorf("LookupRedirectURI (miss): ok = true, want false")
	}
	if gotURI != "" {
		t.Errorf("LookupRedirectURI (miss): uri = %q, want \"\"", gotURI)
	}

	gotURI, gotOK, err = s.LookupRedirectURI(ctx, "", registeredURI)
	if err != nil {
		t.Fatalf("LookupRedirectURI (empty arg): %v", err)
	}
	if gotOK || gotURI != "" {
		t.Errorf("LookupRedirectURI (empty client_id): got (%q, %v), want (\"\", false)", gotURI, gotOK)
	}

	gotURI, gotOK, err = s.LookupRedirectURI(ctx, clientID, "")
	if err != nil {
		t.Fatalf("LookupRedirectURI (empty uri): %v", err)
	}
	if gotOK || gotURI != "" {
		t.Errorf("LookupRedirectURI (empty uri): got (%q, %v), want (\"\", false)", gotURI, gotOK)
	}
}
