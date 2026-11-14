package credstore

import (
	"errors"
	"testing"
	"time"
)

// fakeGate is a UserGate over a fixed set of users allowed to authenticate.
type fakeGate struct {
	allowed map[string]bool
	err     error
	calls   int
}

func (g *fakeGate) UserCanAuthenticate(_, userID string) (bool, error) {
	g.calls++
	if g.err != nil {
		return false, g.err
	}
	return g.allowed[userID], nil
}

const (
	gateTenant = "11111111-1111-1111-1111-111111111111"
	gateUser   = "22222222-2222-2222-2222-222222222222"
)

// userKey issues a key and binds it to gateUser, the shape a key rehydrated
// from an api_keys row with issued_by set has.
func userKey(t *testing.T, s *Store) (Credential, string) {
	t.Helper()
	cred, token, err := s.Issue(gateTenant, "", nil, 0)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if err := s.RebindUserIDForTests(token, gateUser); err != nil {
		t.Fatalf("rebind: %v", err)
	}
	return cred, token
}

// #1915: a key owned by a user the gate refuses gets ErrUnknownKey, even
// though its in-memory record is still active.
func TestAuthenticate_UserGateRefusesDeactivatedOwner(t *testing.T) {
	s := New(time.Hour)
	_, token := userKey(t, s)
	gate := &fakeGate{allowed: map[string]bool{gateUser: true}}
	s.SetUserGate(gate)

	if _, err := s.Authenticate(token); err != nil {
		t.Fatalf("active owner: Authenticate = %v; want nil", err)
	}
	gate.allowed[gateUser] = false
	if _, err := s.Authenticate(token); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("deactivated owner: Authenticate = %v; want ErrUnknownKey", err)
	}
}

// A gate failure fails closed, and is not reported as an unknown key.
func TestAuthenticate_UserGateErrorFailsClosed(t *testing.T) {
	s := New(time.Hour)
	_, token := userKey(t, s)
	s.SetUserGate(&fakeGate{err: errors.New("db down")})

	_, err := s.Authenticate(token)
	if err == nil || errors.Is(err, ErrUnknownKey) {
		t.Fatalf("Authenticate = %v; want a non-ErrUnknownKey error", err)
	}
}

// Keys with no user (UserID is their own id) never consult the gate: the
// env bootstrap key, recovery keys and admin-issued push keys are
// unaffected by any user's status.
func TestAuthenticate_UserGateSkipsKeylessUsers(t *testing.T) {
	s := New(time.Hour)
	gate := &fakeGate{allowed: map[string]bool{}}
	s.SetUserGate(gate)

	_, token, err := s.Issue(gateTenant, "", nil, 0)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	const fixed = "FAKE_TEST_TOKEN_not_a_secret"
	if _, err := s.IssueFixedAdmin(gateTenant, fixed); err != nil {
		t.Fatalf("IssueFixedAdmin: %v", err)
	}
	_, recovery, err := s.IssueRecoveryAdmin(gateTenant, time.Hour, gateUser)
	if err != nil {
		t.Fatalf("IssueRecoveryAdmin: %v", err)
	}
	for name, tok := range map[string]string{"issued": token, "fixed": fixed, "recovery": recovery} {
		if _, err := s.Authenticate(tok); err != nil {
			t.Errorf("%s key: Authenticate = %v; want nil", name, err)
		}
	}
	if gate.calls != 0 {
		t.Fatalf("gate consulted %d times for key-less credentials; want 0", gate.calls)
	}
}

// RevokeUserInMemory revokes only the named user's keys in the named
// tenant, and writes nothing through to a Persister.
func TestRevokeUserInMemory(t *testing.T) {
	s := New(time.Hour)
	_, owned := userKey(t, s)
	_, other, err := s.Issue(gateTenant, "", nil, 0)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if n := s.RevokeUserInMemory("33333333-3333-3333-3333-333333333333", gateUser); n != 0 {
		t.Fatalf("other tenant: revoked %d; want 0", n)
	}
	if n := s.RevokeUserInMemory(gateTenant, gateUser); n != 1 {
		t.Fatalf("revoked %d; want 1", n)
	}
	if _, err := s.Authenticate(owned); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("owned key after revoke: %v; want ErrUnknownKey", err)
	}
	if _, err := s.Authenticate(other); err != nil {
		t.Fatalf("unowned key after revoke: %v; want nil", err)
	}
	if n := s.RevokeUserInMemory(gateTenant, ""); n != 0 {
		t.Fatalf("empty user id revoked %d; want 0", n)
	}
}

func TestOwnedByUser(t *testing.T) {
	cases := map[string]struct {
		c    Credential
		want bool
	}{
		"user uuid":    {Credential{ID: "key_x", UserID: gateUser}, true},
		"own id":       {Credential{ID: "key_x", UserID: "key_x"}, false},
		"empty":        {Credential{ID: "key_x"}, false},
		"non-uuid":     {Credential{ID: "key_x", UserID: "jwt:test"}, false},
		"uuid-form id": {Credential{ID: gateUser, UserID: gateUser}, false},
	}
	for name, tc := range cases {
		if got := OwnedByUser(tc.c); got != tc.want {
			t.Errorf("%s: OwnedByUser = %v; want %v", name, got, tc.want)
		}
	}
}
