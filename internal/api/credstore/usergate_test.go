package credstore

import (
	"errors"
	"testing"
	"time"
)

// fakeGate is a UserGate over a fixed set of users allowed to authenticate.
// Keys named in revokedKeys are refused whatever their owner's status, the
// way the production gate refuses a key whose api_keys row is revoked.
type fakeGate struct {
	allowed     map[string]bool
	revokedKeys map[string]bool
	err         error
	calls       int
	keyIDs      []string
	// keyCalls counts KeyCanAuthenticate, the check for persisted keys
	// with no user; calls counts only UserCanAuthenticate.
	keyCalls int
}

func (g *fakeGate) UserCanAuthenticate(_, userID, keyID string) (bool, error) {
	g.calls++
	g.keyIDs = append(g.keyIDs, keyID)
	if g.err != nil {
		return false, g.err
	}
	if g.revokedKeys[keyID] {
		return false, nil
	}
	return g.allowed[userID], nil
}

func (g *fakeGate) KeyCanAuthenticate(_, keyID string) (bool, error) {
	g.keyCalls++
	if g.err != nil {
		return false, g.err
	}
	return !g.revokedKeys[keyID], nil
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

// #1947: the gate gets the key id of a persisted key, so a key revoked
// durably by another replica is refused here even though this replica's
// in-memory record is active and the owner is active again. A memory-only
// key has no row to check and passes an empty key id.
func TestAuthenticate_UserGateChecksPersistedKeyRevocation(t *testing.T) {
	s := New(time.Hour)
	_, memOnly := userKey(t, s)
	if _, err := s.AttachPersistence(newFakePersister()); err != nil {
		t.Fatalf("AttachPersistence: %v", err)
	}
	persisted, persistedTok := userKey(t, s)
	gate := &fakeGate{allowed: map[string]bool{gateUser: true}, revokedKeys: map[string]bool{}}
	s.SetUserGate(gate)

	for name, tok := range map[string]string{"memory-only": memOnly, "persisted": persistedTok} {
		if _, err := s.Authenticate(tok); err != nil {
			t.Fatalf("%s key, live row: Authenticate = %v; want nil", name, err)
		}
	}
	if len(gate.keyIDs) != 2 {
		t.Fatalf("gate consulted %d times; want 2", len(gate.keyIDs))
	}
	got := map[string]bool{gate.keyIDs[0]: true, gate.keyIDs[1]: true}
	if !got[""] || !got[persisted.ID] {
		t.Fatalf("gate key ids = %q; want one empty and %q", gate.keyIDs, persisted.ID)
	}

	gate.revokedKeys[persisted.ID] = true
	if _, err := s.Authenticate(persistedTok); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("persisted key, revoked row: Authenticate = %v; want ErrUnknownKey", err)
	}
	if _, err := s.Authenticate(memOnly); err != nil {
		t.Fatalf("memory-only key: Authenticate = %v; want nil", err)
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
	if gate.calls != 0 || gate.keyCalls != 0 {
		t.Fatalf("gate consulted %d user / %d key times for memory-only key-less credentials; want 0, 0", gate.calls, gate.keyCalls)
	}
}

// #1964: a persisted key with no user (admin-minted, push) is checked
// against its api_keys row on every authentication, so a revocation
// committed on another replica refuses it here even though this replica's
// in-memory record is still active. A gate error fails closed.
func TestAuthenticate_KeyGateChecksPersistedMachineKeyRevocation(t *testing.T) {
	s := New(time.Hour)
	const fixed = "FAKE_TEST_TOKEN_not_a_secret"
	if _, err := s.IssueFixedAdmin(gateTenant, fixed); err != nil {
		t.Fatalf("IssueFixedAdmin: %v", err)
	}
	if _, err := s.AttachPersistence(newFakePersister()); err != nil {
		t.Fatalf("AttachPersistence: %v", err)
	}
	machine, machineTok, err := s.Issue(gateTenant, "", nil, 0)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	gate := &fakeGate{allowed: map[string]bool{}, revokedKeys: map[string]bool{}}
	s.SetUserGate(gate)

	if _, err := s.Authenticate(machineTok); err != nil {
		t.Fatalf("live row: Authenticate = %v; want nil", err)
	}
	if gate.keyCalls != 1 || gate.calls != 0 {
		t.Fatalf("gate consulted %d key / %d user times; want 1, 0", gate.keyCalls, gate.calls)
	}

	gate.revokedKeys[machine.ID] = true
	if _, err := s.Authenticate(machineTok); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("revoked row: Authenticate = %v; want ErrUnknownKey", err)
	}

	// The memory-only bootstrap key has no row and never consults the gate.
	before := gate.keyCalls
	if _, err := s.Authenticate(fixed); err != nil {
		t.Fatalf("bootstrap key: Authenticate = %v; want nil", err)
	}
	if gate.keyCalls != before {
		t.Fatalf("bootstrap key consulted the key gate")
	}

	gate.err = errors.New("db down")
	if _, err := s.Authenticate(machineTok); err == nil || errors.Is(err, ErrUnknownKey) {
		t.Fatalf("gate error: Authenticate = %v; want a non-ErrUnknownKey error", err)
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

func TestMinterUserID(t *testing.T) {
	cases := map[string]struct {
		c    Credential
		want string
	}{
		"jwt user":     {Credential{ID: "jwt:x", UserID: "user:" + gateUser}, gateUser},
		"user key":     {Credential{ID: "key_x", UserID: gateUser}, gateUser},
		"machine key":  {Credential{ID: "key_x", UserID: "key_x"}, ""},
		"uuid-form id": {Credential{ID: gateUser, UserID: gateUser}, ""},
		"empty":        {Credential{ID: "key_x"}, ""},
	}
	for name, tc := range cases {
		if got := MinterUserID(tc.c); got != tc.want {
			t.Errorf("%s: MinterUserID = %q; want %q", name, got, tc.want)
		}
	}
}

// #1947: IssueMinted records the minter for attribution only. The key stays
// a machine key, so the user gate never consults the minter's status.
func TestIssueMinted_RecordsMinterWithoutOwnership(t *testing.T) {
	s := New(time.Hour)
	p := newFakePersister()
	if _, err := s.AttachPersistence(p); err != nil {
		t.Fatalf("AttachPersistence: %v", err)
	}
	gate := &fakeGate{allowed: map[string]bool{}}
	s.SetUserGate(gate)

	cred, token, err := s.IssueMinted(gateTenant, "", nil, 0, gateUser)
	if err != nil {
		t.Fatalf("IssueMinted: %v", err)
	}
	if cred.MintedBy != gateUser || cred.UserID != cred.ID {
		t.Fatalf("cred MintedBy=%q UserID=%q; want minter %q and own id", cred.MintedBy, cred.UserID, gateUser)
	}
	if got := p.inserted[cred.ID].Cred.MintedBy; got != gateUser {
		t.Fatalf("persisted MintedBy = %q; want %q", got, gateUser)
	}
	if _, err := s.Authenticate(token); err != nil {
		t.Fatalf("Authenticate with minter disallowed = %v; want nil", err)
	}
	if gate.calls != 0 {
		t.Fatalf("user gate consulted %d times for a machine key; want 0", gate.calls)
	}
	if gate.keyCalls != 1 {
		t.Fatalf("key gate consulted %d times for a persisted machine key; want 1", gate.keyCalls)
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
