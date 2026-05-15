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
	// IsApprover marks the credential as authorized to approve
	// audit-binding artifacts — specifically the FrameworkScope predicate
	// (slice 018, PATCH /v1/framework-scopes/{id}/approve). Like IsAdmin
	// it is tenant-scoped: an approver for tenant A cannot approve
	// scopes for tenant B. IsAdmin implies IsApprover (admins can do
	// anything an approver can) but the converse is not true.
	//
	// In v1 the approver role is minted via IssueApprover. Slice 035
	// will graduate this to full OPA-driven RBAC; until then the
	// flag-on-credential pattern matches slice 014's IsAdmin shape.
	IsApprover bool
	// UserID is a free-form identifier — for v1 it is the credential id
	// itself ("key_…") and the FrameworkScope approve handler records
	// it onto the approver_user_id column. Slice 034 (OIDC RP + local
	// users) will replace this with a real user id from the IdP claims.
	UserID string
	// OwnerRoles is the list of control-owner roles this credential
	// holds. Slice 011 introduces this as the gate for
	// POST /v1/controls/{id}/attestations — the caller's OwnerRoles must
	// include the control's `owner_role` (declared in the slice-009
	// bundle manifest). IsAdmin acts as a wildcard: an admin holds every
	// owner role implicitly. Slice 035 graduates this to OPA-driven RBAC.
	OwnerRoles []string
}

// HasOwnerRole reports whether c can attest a control whose `owner_role`
// equals role. Admin is a wildcard; otherwise c must list the role
// verbatim in OwnerRoles. Empty role argument returns false so a control
// bundle that forgot to declare an owner_role cannot be attested by
// anyone but an admin.
func (c Credential) HasOwnerRole(role string) bool {
	if role == "" {
		return c.IsAdmin
	}
	if c.IsAdmin {
		return true
	}
	for _, r := range c.OwnerRoles {
		if r == role {
			return true
		}
	}
	return false
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
	return s.issueLocked(tenantID, scope, kinds, ttl, "", false, false, nil)
}

// IssueAdmin mints an admin-flagged credential for tenantID. Admin
// credentials are still tenant-scoped — they unlock admin-only actions
// (e.g. POST /v1/schemas) for THIS tenant only. There is no global admin
// in the v1 design. Admin implies approver — schema-registry admins are
// trusted with audit-binding sign-off too.
func (s *Store) IssueAdmin(tenantID string, ttl time.Duration) (Credential, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.issueLocked(tenantID, "", nil, ttl, "", true, true, nil)
}

// IssueFixedAdmin mints an admin-flagged credential for tenantID whose
// bearer token is the caller-supplied `token` rather than a freshly
// generated random one. Slice 037 uses this so the offline
// atlas-bootstrap container can authenticate control-bundle uploads with
// a deterministic pre-shared token (ATLAS_BOOTSTRAP_TOKEN) — the existing
// random-token issuance prints the token to stderr only, which an
// unattended one-shot container cannot consume.
//
// This is a self-host bootstrap convenience, not a production auth path:
// the token is operator-supplied and the .env.example flags it as a
// must-rotate value. Returns an error if token is empty.
func (s *Store) IssueFixedAdmin(tenantID, token string) (Credential, error) {
	if token == "" {
		return Credential{}, errors.New("credstore: fixed admin token must not be empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id, err := randomHex(16)
	if err != nil {
		return Credential{}, err
	}
	credID := "key_" + id
	cred := Credential{
		ID:         credID,
		TenantID:   tenantID,
		IssuedAt:   time.Now().UTC(),
		Last4:      token[len(token)-min(4, len(token)):],
		IsAdmin:    true,
		IsApprover: true,
		UserID:     credID,
	}
	r := &record{cred: cred, tokenHash: hashToken(token), state: stateActive}
	s.byID[cred.ID] = r
	s.byTokenHash[r.tokenHash] = r
	return cred, nil
}

// IssueApprover mints an approver-flagged credential for tenantID. The
// approver role gates audit-binding sign-off — most notably the slice-018
// FrameworkScope `approve` transition (POST /v1/framework-scopes/{id}/approve).
// Approver credentials cannot register schemas (that remains admin-only) so
// the boundary between "auditor sign-off" and "platform admin" stays clean.
//
// Tenant-scoped: an approver for tenant A cannot approve scopes for tenant B.
// Slice 035 graduates this to OPA-driven RBAC; until then the flag-on-credential
// pattern matches slice 014's IsAdmin shape.
func (s *Store) IssueApprover(tenantID string, ttl time.Duration) (Credential, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.issueLocked(tenantID, "", nil, ttl, "", false, true, nil)
}

// IssueOwner mints a credential carrying the supplied OwnerRoles for
// tenantID. Slice 011 uses this to gate the manual-control attestation
// endpoint: the bearer must hold the control's `owner_role` to attest.
// Admin and approver flags are off — an owner cannot register schemas
// or approve framework scopes. Tenant-scoped like every other issuer:
// an owner for tenant A cannot attest controls for tenant B.
func (s *Store) IssueOwner(tenantID string, roles []string, ttl time.Duration) (Credential, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.issueLocked(tenantID, "", nil, ttl, "", false, false, roles)
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

	successor, bearer, err = s.issueLocked(pred.cred.TenantID, pred.cred.ScopePredicate, pred.cred.Kinds, pred.cred.TTL, id, pred.cred.IsAdmin, pred.cred.IsApprover, pred.cred.OwnerRoles)
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

// RebindUserIDForTests overrides the UserID field on the credential
// keyed by the supplied bearer plaintext. Test-only helper for slice
// 023's integration tests, which need cred.UserID to equal a real
// users(id) UUID so the policy_acknowledgments composite FK passes.
//
// In production, slice 034's OIDC-callback path sets UserID at issue
// time from the IdP's `sub` claim. Until that path is exercised in
// integration tests, this hook bridges bootstrap creds (which default
// UserID to their own credential id) to seeded users rows.
//
// Returns ErrUnknownKey if the bearer doesn't authenticate.
func (s *Store) RebindUserIDForTests(token, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.byTokenHash[hashToken(token)]
	if !ok || r.state == stateRevoked {
		return ErrUnknownKey
	}
	r.cred.UserID = userID
	return nil
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
func (s *Store) issueLocked(tenantID, scope string, kinds []string, ttl time.Duration, rotatedFrom string, isAdmin, isApprover bool, ownerRoles []string) (Credential, string, error) {
	id, err := randomHex(16)
	if err != nil {
		return Credential{}, "", err
	}
	token, err := randomHex(32)
	if err != nil {
		return Credential{}, "", err
	}

	credID := "key_" + id
	cred := Credential{
		ID:             credID,
		TenantID:       tenantID,
		ScopePredicate: scope,
		Kinds:          append([]string(nil), kinds...),
		TTL:            ttl,
		IssuedAt:       time.Now().UTC(),
		RotatedFrom:    rotatedFrom,
		Last4:          token[len(token)-4:],
		IsAdmin:        isAdmin,
		IsApprover:     isApprover,
		UserID:         credID,
		OwnerRoles:     append([]string(nil), ownerRoles...),
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
