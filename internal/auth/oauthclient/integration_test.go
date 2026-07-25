//go:build integration

// Integration tests for the slice 188 OAuth client registry
// (internal/auth/oauthclient).
//
// Load-bearing functions + branches covered:
//
//   - Store.Issue: happy path returns (Client, plaintext, nil) with
//     plaintext = 43-char base64url-shaped string; ErrDuplicateName
//     on the UNIQUE (name) collision branch; empty-name guard.
//   - Store.Verify: happy path returns the row; ErrUnknownClient on
//     unknown client_id; ErrUnknownClient on wrong secret;
//     ErrUnknownClient on disabled row; ErrUnknownClient on empty
//     client_id / empty secret (defensive guards).
//   - Store.Lookup: happy path returns the row; ErrUnknownClient on
//     unknown / disabled / empty.
//
// Honors anti-criteria P0-188-3 (plaintext secrets never logged) and
// the RFC 6749 §5.2 error-collapse (Verify never distinguishes
// "unknown client_id" from "wrong secret").
//
// `oauth_clients` is NOT tenant-scoped (see migration header) so we
// do NOT call tenancy.ApplyTenant — the integration tests exercise
// the production path exactly.
//
// Run via: just test-integration  (sets DATABASE_URL_APP).
package oauthclient_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/auth/oauthclient"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// Slice 435 / 742: the inline atlas_app pool boilerplate (openPool /
// DATABASE_URL_APP / pgxpool dial) this file used to re-derive now lives in
// the shared internal/dbtest harness. dbtest.NewAppPool opens the
// RLS-enforcing application-role pool — oauth_clients is not tenant-scoped,
// so no tenant context is applied (production path exercised exactly).

// uniqueName returns a fresh client name so concurrent runs don't
// collide on the UNIQUE constraint.
func uniqueName(prefix string) string {
	return prefix + "-" + uuid.New().String()
}

// TestIssueHappyPath: a freshly-issued client returns a Client with
// the supplied name, a non-empty UUID-shaped ClientID, and a 43-char
// base64url-shaped plaintext.
func TestIssueHappyPath(t *testing.T) {
	pool := dbtest.NewAppPool(t)
	s := oauthclient.New(pool)
	ctx := context.Background()

	name := uniqueName("ut-issue-happy")
	c, plaintext, err := s.Issue(ctx, name)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if c == nil {
		t.Fatal("Issue: returned nil Client on success")
	}
	if c.Name != name {
		t.Errorf("Name = %q, want %q", c.Name, name)
	}
	if c.ClientID == "" {
		t.Error("ClientID is empty")
	}
	if len(plaintext) != 43 {
		t.Errorf("plaintext length = %d, want 43 (base64url of 32 bytes)", len(plaintext))
	}
	if strings.ContainsAny(plaintext, "+/=") {
		t.Errorf("plaintext contains URL-unsafe characters: %q", plaintext)
	}
	if c.DisabledAt != nil {
		t.Errorf("DisabledAt = %v, want nil (freshly issued)", c.DisabledAt)
	}
}

// TestIssueDuplicateNameReturnsSentinel: the UNIQUE (name) collision
// surfaces as ErrDuplicateName.
func TestIssueDuplicateNameReturnsSentinel(t *testing.T) {
	pool := dbtest.NewAppPool(t)
	s := oauthclient.New(pool)
	ctx := context.Background()

	name := uniqueName("ut-issue-dup")
	if _, _, err := s.Issue(ctx, name); err != nil {
		t.Fatalf("Issue 1: %v", err)
	}
	_, _, err := s.Issue(ctx, name)
	if !errors.Is(err, oauthclient.ErrDuplicateName) {
		t.Fatalf("Issue 2: err = %v, want ErrDuplicateName", err)
	}
}

// TestIssueRejectsEmptyName covers the application-layer guard
// (empty name → error before DB call).
func TestIssueRejectsEmptyName(t *testing.T) {
	pool := dbtest.NewAppPool(t)
	s := oauthclient.New(pool)
	ctx := context.Background()

	if _, _, err := s.Issue(ctx, ""); err == nil {
		t.Fatal("Issue(empty name): got nil, want error")
	}
}

// TestVerifyHappyPath: Issue then Verify round-trips with the correct
// plaintext.
func TestVerifyHappyPath(t *testing.T) {
	pool := dbtest.NewAppPool(t)
	s := oauthclient.New(pool)
	ctx := context.Background()

	c, plaintext, err := s.Issue(ctx, uniqueName("ut-verify-happy"))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	got, err := s.Verify(ctx, c.ClientID, plaintext)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got == nil || got.ClientID != c.ClientID {
		t.Errorf("Verify: got = %+v, want ClientID %q", got, c.ClientID)
	}
}

// TestVerifyUnknownClientCollapses: per RFC 6749 §5.2, an unknown
// client_id and a wrong-secret BOTH collapse to ErrUnknownClient. We
// verify both branches return the same sentinel.
func TestVerifyUnknownClientCollapses(t *testing.T) {
	pool := dbtest.NewAppPool(t)
	s := oauthclient.New(pool)
	ctx := context.Background()

	// Branch 1: client_id that never lived.
	_, err := s.Verify(ctx, uuid.NewString(), "test-secret")
	if !errors.Is(err, oauthclient.ErrUnknownClient) {
		t.Fatalf("Verify(unknown): err = %v, want ErrUnknownClient", err)
	}

	// Branch 2: real client_id, wrong secret.
	c, _, err := s.Issue(ctx, uniqueName("ut-verify-wrong-secret"))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	_, err = s.Verify(ctx, c.ClientID, "wrong-secret-not-matching")
	if !errors.Is(err, oauthclient.ErrUnknownClient) {
		t.Fatalf("Verify(wrong secret): err = %v, want ErrUnknownClient", err)
	}
}

// TestVerifyDisabledClientReturnsUnknown: a soft-disabled client is
// indistinguishable from a typo via the Verify path.
func TestVerifyDisabledClientReturnsUnknown(t *testing.T) {
	pool := dbtest.NewAppPool(t)
	s := oauthclient.New(pool)
	ctx := context.Background()

	c, plaintext, err := s.Issue(ctx, uniqueName("ut-verify-disabled"))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// Soft-disable the client.
	if _, err := pool.Exec(ctx,
		`UPDATE oauth_clients SET disabled_at = now() WHERE id = $1`, c.ID,
	); err != nil {
		t.Fatalf("disable client: %v", err)
	}

	_, err = s.Verify(ctx, c.ClientID, plaintext)
	if !errors.Is(err, oauthclient.ErrUnknownClient) {
		t.Fatalf("Verify(disabled): err = %v, want ErrUnknownClient", err)
	}
}

// TestVerifyEmptyArgsReturnsUnknown covers the defensive guard for
// empty client_id / empty plaintext — both collapse to
// ErrUnknownClient without touching the DB.
func TestVerifyEmptyArgsReturnsUnknown(t *testing.T) {
	pool := dbtest.NewAppPool(t)
	s := oauthclient.New(pool)
	ctx := context.Background()

	for _, tc := range []struct {
		name      string
		clientID  string
		plaintext string
	}{
		{"empty client_id", "", "test-secret"},
		{"empty plaintext", "test-client-id", ""},
		{"both empty", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.Verify(ctx, tc.clientID, tc.plaintext)
			if !errors.Is(err, oauthclient.ErrUnknownClient) {
				t.Fatalf("Verify(%s): err = %v, want ErrUnknownClient", tc.name, err)
			}
		})
	}
}

// TestLookupHappyPath: a secret-less lookup returns the registered
// row.
func TestLookupHappyPath(t *testing.T) {
	pool := dbtest.NewAppPool(t)
	s := oauthclient.New(pool)
	ctx := context.Background()

	c, _, err := s.Issue(ctx, uniqueName("ut-lookup-happy"))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	got, err := s.Lookup(ctx, c.ClientID)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got == nil || got.ClientID != c.ClientID {
		t.Errorf("Lookup: got = %+v, want ClientID %q", got, c.ClientID)
	}
}

// TestLookupUnknownReturnsSentinel: a never-registered client_id
// returns ErrUnknownClient.
func TestLookupUnknownReturnsSentinel(t *testing.T) {
	pool := dbtest.NewAppPool(t)
	s := oauthclient.New(pool)
	ctx := context.Background()

	_, err := s.Lookup(ctx, uuid.NewString())
	if !errors.Is(err, oauthclient.ErrUnknownClient) {
		t.Fatalf("Lookup(unknown): err = %v, want ErrUnknownClient", err)
	}
}

// TestLookupDisabledReturnsSentinel: a soft-disabled client is
// indistinguishable from "unknown" via Lookup, matching Verify.
func TestLookupDisabledReturnsSentinel(t *testing.T) {
	pool := dbtest.NewAppPool(t)
	s := oauthclient.New(pool)
	ctx := context.Background()

	c, _, err := s.Issue(ctx, uniqueName("ut-lookup-disabled"))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE oauth_clients SET disabled_at = now() WHERE id = $1`, c.ID,
	); err != nil {
		t.Fatalf("disable client: %v", err)
	}

	_, err = s.Lookup(ctx, c.ClientID)
	if !errors.Is(err, oauthclient.ErrUnknownClient) {
		t.Fatalf("Lookup(disabled): err = %v, want ErrUnknownClient", err)
	}
}

// TestLookupEmptyClientIDReturnsSentinel: the application-layer guard
// fires before the DB query.
func TestLookupEmptyClientIDReturnsSentinel(t *testing.T) {
	pool := dbtest.NewAppPool(t)
	s := oauthclient.New(pool)
	ctx := context.Background()

	_, err := s.Lookup(ctx, "")
	if !errors.Is(err, oauthclient.ErrUnknownClient) {
		t.Fatalf("Lookup(empty): err = %v, want ErrUnknownClient", err)
	}
}

// TestSecretByteLenConstant: a tiny compile-time guard so a future
// refactor that drops the 32-byte entropy floor surfaces in tests.
func TestSecretByteLenConstant(t *testing.T) {
	t.Parallel()
	if oauthclient.SecretByteLen != 32 {
		t.Fatalf("SecretByteLen = %d, want 32 (256-bit secret entropy)", oauthclient.SecretByteLen)
	}
}

// TestVerifyConsistencyMultipleClients: a Verify with the wrong
// client_id + a different client's correct secret still returns
// ErrUnknownClient — confirms the joint (client_id, secret) check is
// the security boundary (RFC 6749 §5.2).
func TestVerifyConsistencyMultipleClients(t *testing.T) {
	pool := dbtest.NewAppPool(t)
	s := oauthclient.New(pool)
	ctx := context.Background()

	cA, plaintextA, err := s.Issue(ctx, uniqueName("ut-verify-cross-a"))
	if err != nil {
		t.Fatalf("Issue A: %v", err)
	}
	cB, _, err := s.Issue(ctx, uniqueName("ut-verify-cross-b"))
	if err != nil {
		t.Fatalf("Issue B: %v", err)
	}

	// Client B's client_id paired with Client A's plaintext → fail.
	_, err = s.Verify(ctx, cB.ClientID, plaintextA)
	if !errors.Is(err, oauthclient.ErrUnknownClient) {
		t.Fatalf("Verify(cross): err = %v, want ErrUnknownClient", err)
	}

	// Sanity: cA still works with its own plaintext.
	got, err := s.Verify(ctx, cA.ClientID, plaintextA)
	if err != nil {
		t.Fatalf("Verify(self-A): %v", err)
	}
	if got == nil || got.ClientID != cA.ClientID {
		t.Errorf("Verify(self-A): got = %+v, want %q", got, cA.ClientID)
	}
}
