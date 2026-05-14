package credstore

import (
	"testing"
	"time"
)

// TestIssueFixedAdmin_AuthenticatesWithSuppliedToken is the slice-037
// regression: the docker-compose self-host bootstrap mints an admin
// credential with a deterministic pre-shared token, then authenticates
// control-bundle uploads with that exact token.
func TestIssueFixedAdmin_AuthenticatesWithSuppliedToken(t *testing.T) {
	s := New(time.Hour)
	const tenant = "11111111-1111-1111-1111-111111111111"
	const token = "FAKE_TEST_TOKEN_not_a_secret"

	cred, err := s.IssueFixedAdmin(tenant, token)
	if err != nil {
		t.Fatalf("IssueFixedAdmin: %v", err)
	}
	if cred.TenantID != tenant {
		t.Fatalf("TenantID = %q; want %q", cred.TenantID, tenant)
	}
	if !cred.IsAdmin {
		t.Fatal("fixed-token credential must be admin-flagged")
	}

	got, err := s.Authenticate(token)
	if err != nil {
		t.Fatalf("Authenticate(suppliedToken): %v", err)
	}
	if got.ID != cred.ID {
		t.Fatalf("Authenticate returned credential ID %q; want %q", got.ID, cred.ID)
	}
	if !got.IsAdmin {
		t.Fatal("authenticated credential lost its admin flag")
	}
}

// TestIssueFixedAdmin_EmptyTokenRejected guards against a misconfigured
// bootstrap (ATLAS_BOOTSTRAP_TOKEN unset but the code path entered).
func TestIssueFixedAdmin_EmptyTokenRejected(t *testing.T) {
	s := New(time.Hour)
	if _, err := s.IssueFixedAdmin("tenant", ""); err == nil {
		t.Fatal("IssueFixedAdmin(\"\") = nil error; want non-nil")
	}
}

// TestIssueFixedAdmin_WrongTokenRejected confirms an unrelated token does
// not authenticate against a fixed-token credential.
func TestIssueFixedAdmin_WrongTokenRejected(t *testing.T) {
	s := New(time.Hour)
	if _, err := s.IssueFixedAdmin("tenant", "FAKE_TEST_TOKEN_a"); err != nil {
		t.Fatalf("IssueFixedAdmin: %v", err)
	}
	if _, err := s.Authenticate("FAKE_TEST_TOKEN_b"); err == nil {
		t.Fatal("Authenticate(wrongToken) = nil error; want ErrUnknownKey")
	}
}
