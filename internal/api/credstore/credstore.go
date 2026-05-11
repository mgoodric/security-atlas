// Package credstore is an in-memory credential store backing the
// AdminCredentials service. Bearer tokens are hashed at rest; the plaintext
// is only returned at Issue or Rotate time. The Store type is the only
// surface callers need.
package credstore

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrUnknownKey indicates a lookup or operation referenced a key that
// is not present, has been revoked, or is past its rotation grace.
var ErrUnknownKey = errors.New("credstore: unknown key")

// Credential is the metadata view of an API key. Never includes the bearer
// token plaintext.
type Credential struct {
	ID             string
	TenantID       string
	ScopePredicate string
	Kinds          []string
	TTL            time.Duration
	IssuedAt       time.Time
	LastUsedAt     time.Time
	// RotatedFrom is the predecessor's id when this key was issued via
	// Rotate; empty for original Issue.
	RotatedFrom string
	// Last4 is the last four characters of the bearer token. Safe to
	// surface; cannot be used to authenticate.
	Last4 string
	// IsAdmin marks the credential as authorized to perform
	// admin-only actions — most notably POST /v1/schemas (slice 014).
	// Standard tenant credentials issued via Issue() have IsAdmin=false.
	// Admin credentials are minted via IssueAdmin() and are themselves
	// tenant-scoped: an admin for tenant A cannot register schemas for
	// tenant B (anti-criterion: no cross-tenant private-kind leak).
	IsAdmin bool
}

type state int

const (
	stateActive state = iota
	stateRevoked
)

type record struct {
	cred      Credential
	tokenHash string
	state     state
	// retiresAt is non-zero on a predecessor after Rotate. The key
	// authenticates until this timestamp; after, Authenticate rejects.
	retiresAt time.Time
}

// Store is the in-memory credential store.
type Store struct {
	mu            sync.Mutex
	byID          map[string]*record
	byTokenHash   map[string]*record
	rotationGrace time.Duration
}

// New returns a Store. rotationGrace controls how long a rotated-out
// predecessor stays valid; pass 0 for "no grace" (tests).
func New(rotationGrace time.Duration) *Store {
	return &Store{
		byID:          map[string]*record{},
		byTokenHash:   map[string]*record{},
		rotationGrace: rotationGrace,
	}
}

// Issue creates a new credential and returns the bearer plaintext. The
// caller must surface the plaintext exactly once and discard it.
func (s *Store) Issue(tenantID, scope string, kinds []string, ttl time.Duration) (Credential, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.issueLocked(tenantID, scope, kinds, ttl, "", false)
}

// IssueAdmin mints an admin-flagged credential for tenantID. Admin
// credentials are still tenant-scoped — they unlock admin-only actions
// (e.g. POST /v1/schemas) for THIS tenant only. There is no global admin
// in the v1 design.
func (s *Store) IssueAdmin(tenantID string, ttl time.Duration) (Credential, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.issueLocked(tenantID, "", nil, ttl, "", true)
}

// Rotate issues a successor credential. The predecessor remains valid until
// now + rotationGrace; the return value carries that grace deadline. Holds
// the store lock for the duration so a concurrent Revoke or duplicate
// Rotate of the same id cannot interleave.
func (s *Store) Rotate(id string) (successor Credential, bearer string, predecessorExpiresAt time.Time, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	pred, ok := s.byID[id]
	if !ok || pred.state != stateActive {
		err = ErrUnknownKey
		return
	}
	if !pred.retiresAt.IsZero() {
		err = ErrUnknownKey
		return
	}

	successor, bearer, err = s.issueLocked(pred.cred.TenantID, pred.cred.ScopePredicate, pred.cred.Kinds, pred.cred.TTL, id, pred.cred.IsAdmin)
	if err != nil {
		return
	}

	predecessorExpiresAt = time.Now().UTC().Add(s.rotationGrace)
	pred.retiresAt = predecessorExpiresAt
	return
}

// Revoke invalidates the key immediately. Subsequent Authenticate calls
// with its bearer return ErrUnknownKey.
func (s *Store) Revoke(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.byID[id]
	if !ok {
		return ErrUnknownKey
	}
	r.state = stateRevoked
	return nil
}

// List returns active credentials for a tenant. Metadata only.
func (s *Store) List(tenantID string) []Credential {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Credential, 0)
	for _, r := range s.byID {
		if r.state != stateActive || r.cred.TenantID != tenantID {
			continue
		}
		if !r.retiresAt.IsZero() && time.Now().UTC().After(r.retiresAt) {
			continue
		}
		out = append(out, r.cred)
	}
	return out
}

// Authenticate resolves a plaintext bearer to its credential. Updates
// LastUsedAt on success.
func (s *Store) Authenticate(token string) (Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.byTokenHash[hashToken(token)]
	if !ok || r.state == stateRevoked {
		return Credential{}, ErrUnknownKey
	}
	if !r.retiresAt.IsZero() && time.Now().UTC().After(r.retiresAt) {
		return Credential{}, ErrUnknownKey
	}
	r.cred.LastUsedAt = time.Now().UTC()
	return r.cred, nil
}

// issueLocked inserts a new credential. Caller must hold s.mu.
func (s *Store) issueLocked(tenantID, scope string, kinds []string, ttl time.Duration, rotatedFrom string, isAdmin bool) (Credential, string, error) {
	id, err := randomHex(16)
	if err != nil {
		return Credential{}, "", err
	}
	token, err := randomHex(32)
	if err != nil {
		return Credential{}, "", err
	}

	cred := Credential{
		ID:             "key_" + id,
		TenantID:       tenantID,
		ScopePredicate: scope,
		Kinds:          append([]string(nil), kinds...),
		TTL:            ttl,
		IssuedAt:       time.Now().UTC(),
		RotatedFrom:    rotatedFrom,
		Last4:          token[len(token)-4:],
		IsAdmin:        isAdmin,
	}
	r := &record{cred: cred, tokenHash: hashToken(token), state: stateActive}
	s.byID[cred.ID] = r
	s.byTokenHash[r.tokenHash] = r
	return cred, token, nil
}

func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("credstore: random: %w", err)
	}
	return hex.EncodeToString(b), nil
}
