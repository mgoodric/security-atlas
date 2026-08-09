// Unit tests for the OE-435 persistence write-through: a fake in-memory
// Persister stands in for the api_keys-backed apikeystore.Persister so
// every new branch is exercised without Postgres (slice-353 Q-2 fast
// loop). The real backend's round-trip is covered by the integration
// suite in internal/auth/credpersist_integration_test.go.
package credstore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"
)

// fakePersister records write-throughs and serves LoadActive from them,
// mimicking a durable table across "restarts" (two Stores sharing one
// fake).
type fakePersister struct {
	nextID    int
	inserted  map[string]PersistedCredential // by cred id
	retired   map[string]time.Time           // predecessor id -> retiresAt
	revoked   map[string]bool
	insertErr error
	revokeErr error
	rotateErr error
}

func newFakePersister() *fakePersister {
	return &fakePersister{
		inserted: map[string]PersistedCredential{},
		retired:  map[string]time.Time{},
		revoked:  map[string]bool{},
	}
}

func (f *fakePersister) NewID() (string, error) {
	f.nextID++
	return fmt.Sprintf("key_fake-%04d", f.nextID), nil
}

// HashToken uses a distinct scheme from the store's legacy SHA-256 so the
// tests can tell the two hash spaces apart (production uses HMAC here).
func (f *fakePersister) HashToken(token string) string {
	sum := sha256.Sum256([]byte("fake-keyed:" + token))
	return hex.EncodeToString(sum[:])
}

func (f *fakePersister) Insert(rec PersistedCredential) error {
	if f.insertErr != nil {
		return f.insertErr
	}
	f.inserted[rec.Cred.ID] = rec
	return nil
}

func (f *fakePersister) Rotate(successor PersistedCredential, predecessorID, tenantID string, retiresAt time.Time) error {
	if f.rotateErr != nil {
		return f.rotateErr
	}
	f.inserted[successor.Cred.ID] = successor
	f.retired[predecessorID] = retiresAt
	return nil
}

func (f *fakePersister) Revoke(id, tenantID string) error {
	if f.revokeErr != nil {
		return f.revokeErr
	}
	f.revoked[id] = true
	return nil
}

func (f *fakePersister) LoadActive() ([]PersistedCredential, error) {
	now := time.Now().UTC()
	out := []PersistedCredential{}
	for id, rec := range f.inserted {
		if f.revoked[id] {
			continue
		}
		if at, ok := f.retired[id]; ok {
			if now.After(at) {
				continue
			}
			rec.RetiresAt = at
		}
		out = append(out, rec)
	}
	return out, nil
}

func attach(t *testing.T, s *Store, p Persister) int {
	t.Helper()
	n, err := s.AttachPersistence(p)
	if err != nil {
		t.Fatalf("AttachPersistence: %v", err)
	}
	return n
}

func TestPersist_IssueSurvivesRestart(t *testing.T) {
	fake := newFakePersister()

	s1 := New(time.Hour)
	attach(t, s1, fake)
	cred, token, err := s1.Issue("tenant-a", `{"env":"prod"}`, []string{"sast.scan_result"}, 0)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, ok := fake.inserted[cred.ID]; !ok {
		t.Fatalf("issue did not write through to the persister")
	}

	// "Restart": a fresh store rehydrated from the same backend.
	s2 := New(time.Hour)
	if n := attach(t, s2, fake); n != 1 {
		t.Fatalf("loaded %d credentials, want 1", n)
	}
	got, err := s2.Authenticate(token)
	if err != nil {
		t.Fatalf("Authenticate after restart: %v", err)
	}
	if got.ID != cred.ID || got.TenantID != "tenant-a" || got.ScopePredicate != `{"env":"prod"}` {
		t.Fatalf("rehydrated credential mismatch: got %+v want id=%s", got, cred.ID)
	}
}

func TestPersist_PreAttachCredentialStillAuthenticates(t *testing.T) {
	s := New(time.Hour)
	// Issued BEFORE persistence attaches — the bootstrap-credential shape.
	// Keyed under the legacy SHA-256 scheme.
	cred, token, err := s.Issue("tenant-a", "", nil, 0)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	fake := newFakePersister()
	attach(t, s, fake)
	if _, ok := fake.inserted[cred.ID]; ok {
		t.Fatalf("pre-attach credential must not be retroactively persisted")
	}
	got, err := s.Authenticate(token)
	if err != nil {
		t.Fatalf("legacy-hash fallback failed: %v", err)
	}
	if got.ID != cred.ID {
		t.Fatalf("got id %s want %s", got.ID, cred.ID)
	}
	// And the test-only rebind hook rides the same fallback.
	if err := s.RebindUserIDForTests(token, "user-1"); err != nil {
		t.Fatalf("RebindUserIDForTests via legacy hash: %v", err)
	}
}

func TestPersist_FixedAdminNeverPersisted(t *testing.T) {
	fake := newFakePersister()
	s := New(time.Hour)
	attach(t, s, fake)

	cred, err := s.IssueFixedAdmin("tenant-a", "deterministic-bootstrap-token")
	if err != nil {
		t.Fatalf("IssueFixedAdmin: %v", err)
	}
	if len(fake.inserted) != 0 {
		t.Fatalf("fixed-admin credential leaked into the persister")
	}
	got, err := s.Authenticate("deterministic-bootstrap-token")
	if err != nil || got.ID != cred.ID {
		t.Fatalf("fixed admin must still authenticate: cred=%+v err=%v", got, err)
	}
}

func TestPersist_InsertErrorFailsClosed(t *testing.T) {
	fake := newFakePersister()
	fake.insertErr = errors.New("boom")
	s := New(time.Hour)
	attach(t, s, fake)

	if _, _, err := s.Issue("tenant-a", "", nil, 0); err == nil {
		t.Fatalf("Issue must fail when persistence fails")
	}
	// Nothing half-issued: the store must not hold a credential the
	// backend never recorded.
	if got := s.List("tenant-a"); len(got) != 0 {
		t.Fatalf("failed issue left %d credential(s) in memory", len(got))
	}
}

func TestPersist_RevokeWritesThroughAndFailsClosed(t *testing.T) {
	fake := newFakePersister()
	s1 := New(time.Hour)
	attach(t, s1, fake)
	cred, token, err := s1.Issue("tenant-a", "", nil, 0)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	fake.revokeErr = errors.New("db down")
	if err := s1.Revoke(cred.ID); err == nil {
		t.Fatalf("Revoke must fail closed on persistence error")
	}
	if _, err := s1.Authenticate(token); err != nil {
		t.Fatalf("failed revoke must leave the credential active: %v", err)
	}

	fake.revokeErr = nil
	if err := s1.Revoke(cred.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if !fake.revoked[cred.ID] {
		t.Fatalf("revoke did not write through")
	}

	// Restart: the revocation must hold.
	s2 := New(time.Hour)
	if n := attach(t, s2, fake); n != 0 {
		t.Fatalf("revoked credential was rehydrated (%d loaded)", n)
	}
	if _, err := s2.Authenticate(token); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("revoked credential authenticated after restart: %v", err)
	}
}

func TestPersist_RotateSurvivesRestart(t *testing.T) {
	fake := newFakePersister()
	s1 := New(time.Hour)
	attach(t, s1, fake)
	pred, predToken, err := s1.Issue("tenant-a", "", nil, 0)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	successor, succToken, retires, err := s1.Rotate(pred.ID)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if got, ok := fake.retired[pred.ID]; !ok || !got.Equal(retires) {
		t.Fatalf("rotate did not retire the predecessor durably: %v %v", got, ok)
	}
	if fake.inserted[successor.ID].Cred.RotatedFrom != pred.ID {
		t.Fatalf("successor's rotated_from not persisted")
	}

	s2 := New(time.Hour)
	if n := attach(t, s2, fake); n != 2 {
		t.Fatalf("loaded %d credentials, want successor + in-grace predecessor", n)
	}
	if _, err := s2.Authenticate(succToken); err != nil {
		t.Fatalf("successor rejected after restart: %v", err)
	}
	// Predecessor still inside its 1h grace window.
	if _, err := s2.Authenticate(predToken); err != nil {
		t.Fatalf("in-grace predecessor rejected after restart: %v", err)
	}
}

func TestPersist_RotateOfMemoryOnlyPredecessor(t *testing.T) {
	s := New(time.Hour)
	// Predecessor issued pre-attach: memory-only.
	pred, _, err := s.Issue("tenant-a", "", nil, 0)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	fake := newFakePersister()
	attach(t, s, fake)

	successor, _, _, err := s.Rotate(pred.ID)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	rec, ok := fake.inserted[successor.ID]
	if !ok {
		t.Fatalf("successor of memory-only predecessor was not persisted")
	}
	if rec.Cred.RotatedFrom != "" {
		t.Fatalf("rotated_from %q persisted for a predecessor with no row (FK would fail)", rec.Cred.RotatedFrom)
	}
	if len(fake.retired) != 0 {
		t.Fatalf("memory-only predecessor must not be durably retired")
	}
}

func TestPersist_AttachTwiceRejected(t *testing.T) {
	s := New(time.Hour)
	attach(t, s, newFakePersister())
	if _, err := s.AttachPersistence(newFakePersister()); err == nil {
		t.Fatalf("second AttachPersistence must fail")
	}
	if _, err := s.AttachPersistence(nil); err == nil {
		t.Fatalf("nil persister must be rejected")
	}
}
