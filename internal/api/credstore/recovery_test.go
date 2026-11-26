package credstore

import (
	"errors"
	"testing"
	"time"
)

const recoveryTenant = "22222222-2222-2222-2222-222222222222"

// #1892: TTL is now enforced on the in-memory path too. ttl=0 still means
// no expiry, which every bootstrap, fixed-admin and CI-issued key relies on.
func TestAuthenticate_TTLZeroNeverExpires(t *testing.T) {
	s := New(0)
	cred, bearer, err := s.Issue(recoveryTenant, "", nil, 0)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if !cred.ExpiresAt().IsZero() {
		t.Fatalf("ExpiresAt = %v for ttl=0; want zero", cred.ExpiresAt())
	}
	// Push IssuedAt far into the past: a ttl=0 key must still authenticate.
	s.byID[cred.ID].cred.IssuedAt = time.Now().Add(-10 * 365 * 24 * time.Hour)
	if _, err := s.Authenticate(bearer); err != nil {
		t.Fatalf("ttl=0 key stopped authenticating: %v", err)
	}
	fixed, err := s.IssueFixedAdmin(recoveryTenant, "FAKE_FIXED_TOKEN_not_a_secret")
	if err != nil {
		t.Fatalf("IssueFixedAdmin: %v", err)
	}
	s.byID[fixed.ID].cred.IssuedAt = time.Now().Add(-10 * 365 * 24 * time.Hour)
	if _, err := s.Authenticate("FAKE_FIXED_TOKEN_not_a_secret"); err != nil {
		t.Fatalf("fixed admin key stopped authenticating: %v", err)
	}
}

// #1892: a key past IssuedAt + TTL no longer authenticates or lists, the
// same moment the api_keys expires_at filter would drop it at boot.
func TestAuthenticate_ExpiredKeyRejected(t *testing.T) {
	s := New(0)
	cred, bearer, err := s.Issue(recoveryTenant, "", nil, time.Hour)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := s.Authenticate(bearer); err != nil {
		t.Fatalf("fresh key: %v", err)
	}
	s.byID[cred.ID].cred.IssuedAt = time.Now().Add(-2 * time.Hour)
	if _, err := s.Authenticate(bearer); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("expired key: err = %v; want ErrUnknownKey", err)
	}
	if got := s.List(recoveryTenant); len(got) != 0 {
		t.Fatalf("List returned %d expired key(s); want 0", len(got))
	}
}

func TestIssueRecoveryAdmin_MarkedExpiringAdmin(t *testing.T) {
	s := New(0)
	const issuer = "33333333-3333-3333-3333-333333333333"
	cred, bearer, err := s.IssueRecoveryAdmin(recoveryTenant, 24*time.Hour, issuer)
	if err != nil {
		t.Fatalf("IssueRecoveryAdmin: %v", err)
	}
	got, err := s.Authenticate(bearer)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if !got.IsAdmin || got.Purpose != PurposeBootstrapRecovery || got.TTL != 24*time.Hour {
		t.Fatalf("credential = %+v; want admin, recovery purpose, 24h ttl", got)
	}
	// The minter is attribution only. The key's identity stays its own id,
	// so it never acts as the user who minted it.
	if got.MintedBy != issuer || got.UserID != got.ID {
		t.Fatalf("MintedBy=%q UserID=%q; want MintedBy=%s and UserID=%s (own id)", got.MintedBy, got.UserID, issuer, got.ID)
	}
	if cred.ExpiresAt().IsZero() {
		t.Fatalf("recovery key has no expiry")
	}

	// No minter: nothing recorded, identity is still the credential id.
	cred2, _, err := s.IssueRecoveryAdmin(recoveryTenant, time.Hour, "")
	if err != nil {
		t.Fatalf("IssueRecoveryAdmin: %v", err)
	}
	if cred2.UserID != cred2.ID || cred2.MintedBy != "" {
		t.Fatalf("UserID=%q MintedBy=%q; want the credential id and no minter", cred2.UserID, cred2.MintedBy)
	}
}

func TestIssueRecoveryAdmin_RefusesNonExpiring(t *testing.T) {
	s := New(0)
	for _, ttl := range []time.Duration{0, -time.Second} {
		if _, _, err := s.IssueRecoveryAdmin(recoveryTenant, ttl, ""); err == nil {
			t.Fatalf("ttl=%v: want an error", ttl)
		}
	}
	if got := s.List(recoveryTenant); len(got) != 0 {
		t.Fatalf("refused issue left %d credential(s)", len(got))
	}
}

// #1892: RevokePurpose touches only marked keys, spares the excepted id,
// and never touches the fixed (env) admin key or an ordinary admin key.
func TestRevokePurpose_OnlyMarkedKeys(t *testing.T) {
	s := New(0)
	const fixedToken = "FAKE_ENV_BOOTSTRAP_not_a_secret"
	if _, err := s.IssueFixedAdmin(recoveryTenant, fixedToken); err != nil {
		t.Fatalf("IssueFixedAdmin: %v", err)
	}
	_, adminBearer, _ := s.IssueAdmin(recoveryTenant, 0)
	old, oldBearer, _ := s.IssueRecoveryAdmin(recoveryTenant, time.Hour, "")
	other, otherBearer, _ := s.IssueRecoveryAdmin("44444444-4444-4444-4444-444444444444", time.Hour, "")
	keep, keepBearer, _ := s.IssueRecoveryAdmin(recoveryTenant, time.Hour, "")

	revoked, err := s.RevokePurpose(PurposeBootstrapRecovery, keep.ID)
	if err != nil {
		t.Fatalf("RevokePurpose: %v", err)
	}
	if len(revoked) != 2 {
		t.Fatalf("revoked %v; want %s and %s", revoked, old.ID, other.ID)
	}
	for _, b := range []string{oldBearer, otherBearer} {
		if _, err := s.Authenticate(b); !errors.Is(err, ErrUnknownKey) {
			t.Fatalf("earlier recovery key still authenticates: %v", err)
		}
	}
	for name, b := range map[string]string{"fixed env key": fixedToken, "ordinary admin": adminBearer, "excepted recovery key": keepBearer} {
		if _, err := s.Authenticate(b); err != nil {
			t.Fatalf("%s was revoked: %v", name, err)
		}
	}
	if _, err := s.RevokePurpose("", ""); err == nil {
		t.Fatalf("RevokePurpose with empty purpose: want an error")
	}
}

// A rotated recovery key keeps its marker, so rotating cannot launder it
// out of reach of the next reset or sign-in.
func TestRotate_RecoveryKeyKeepsPurpose(t *testing.T) {
	s := New(time.Hour)
	pred, _, _ := s.IssueRecoveryAdmin(recoveryTenant, time.Hour, "")
	succ, succBearer, _, err := s.Rotate(pred.ID)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if succ.Purpose != PurposeBootstrapRecovery {
		t.Fatalf("successor Purpose = %q; want the recovery marker", succ.Purpose)
	}
	if _, err := s.RevokePurpose(PurposeBootstrapRecovery, ""); err != nil {
		t.Fatalf("RevokePurpose: %v", err)
	}
	if _, err := s.Authenticate(succBearer); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("rotated recovery key survived RevokePurpose: %v", err)
	}
}
