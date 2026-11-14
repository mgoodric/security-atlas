// Package credstore is the credential store backing the AdminCredentials
// service. Bearer tokens are hashed at rest; the plaintext is only returned
// at Issue or Rotate time. The Store type is the only surface callers need.
//
// The store serves lookups from in-memory maps. By default that is ALL it
// does — which is what slice 356b's chaos experiment measured as gap G-1
// (OE-435): a process restart silently and permanently invalidated every
// credential ever issued. Production wiring now attaches a Persister
// (cmd/atlas → apikeystore.Persister over the slice-034 `api_keys` table):
// issues, rotations and revocations write through to the table, and
// AttachPersistence rehydrates the maps at boot, so a credential issued
// before a restart authenticates after it. Stores without a Persister
// (unit tests, in-memory servers) behave exactly as before.
package credstore

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
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
	// Purpose marks a key minted for a specific job rather than an
	// ordinary issue. PurposeBootstrapRecovery is the only value: the admin
	// key reset-bootstrap mints. Persisted in api_keys.purpose, so the
	// marker survives a restart; a Rotate successor inherits it.
	Purpose string
	// MintedBy is the users(id) uuid of whoever minted the key, for
	// attribution only (api_keys.minted_by). It is NOT the key's identity:
	// UserID is. Nothing authorizes on it. Set on recovery keys (#1892);
	// a Rotate successor inherits it.
	MintedBy string
}

// PurposeBootstrapRecovery marks the admin key POST
// /v1/admin/install/reset-bootstrap mints (#1892). RevokePurpose finds
// these by the marker and id, never by bearer.
const PurposeBootstrapRecovery = "bootstrap-recovery"

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

// ExpiresAt is IssuedAt + TTL, or the zero time for a credential with no
// TTL (it never expires).
func (c Credential) ExpiresAt() time.Time {
	if c.TTL <= 0 {
		return time.Time{}
	}
	return c.IssuedAt.Add(c.TTL)
}

// expired reports whether c's TTL has run out at now. The api_keys
// expires_at column is the same IssuedAt + TTL, so a key expires in memory
// at the moment LoadActive would stop reloading it (#1892).
func (c Credential) expired(now time.Time) bool {
	exp := c.ExpiresAt()
	return !exp.IsZero() && now.After(exp)
}

// Persister is the optional durability backend for a Store (OE-435). When
// attached, every issue/rotate/revoke writes through to it and
// AttachPersistence rehydrates the in-memory maps from it at boot, so
// credentials survive a process restart. The production implementation is
// apikeystore.Persister over the slice-034 `api_keys` table (tenant-scoped,
// RLS-enforced, HMAC-SHA256 token hashes per ADR 0002); this interface
// exists so credstore does not import the DB stack.
type Persister interface {
	// NewID returns a fresh credential id ("key_..." form) whose suffix the
	// backend can round-trip through its storage — the id a credential is
	// issued under MUST be the id it reloads under.
	NewID() (string, error)
	// HashToken maps a bearer plaintext to the hex-encoded at-rest hash the
	// backend persists. The Store keys its in-memory lookup map with the
	// same value so loaded rows and live issues share one hash scheme.
	HashToken(token string) string
	// Insert durably records a newly issued credential.
	Insert(rec PersistedCredential) error
	// Rotate durably records a rotation: insert the successor and set the
	// predecessor's retirement deadline, atomically.
	Rotate(successor PersistedCredential, predecessorID, tenantID string, retiresAt time.Time) error
	// Revoke durably marks the credential revoked.
	Revoke(id, tenantID string) error
	// LoadActive returns every credential that should authenticate right
	// now: not revoked, not expired, not past its rotation grace.
	LoadActive() ([]PersistedCredential, error)
}

// PersistedCredential is the wire shape between the Store and a Persister.
// TokenHashHex is the Persister.HashToken output — never a plaintext.
type PersistedCredential struct {
	Cred         Credential
	TokenHashHex string
	// RetiresAt is non-zero on a rotated-out predecessor still inside its
	// grace window.
	RetiresAt time.Time
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
	// persisted marks records the attached Persister holds a row for.
	// Memory-only records (anything issued before AttachPersistence — the
	// bootstrap credentials — plus every IssueFixedAdmin credential) skip
	// the write-through on Rotate/Revoke.
	persisted bool
}

// Store is the credential store: in-memory maps, optionally write-through
// persisted via an attached Persister.
type Store struct {
	mu            sync.Mutex
	byID          map[string]*record
	byTokenHash   map[string]*record
	rotationGrace time.Duration
	persist       Persister
	gate          UserGate
}

// UserGate reports whether the user a credential belongs to may still use
// it (#1915). Authenticate consults it for every credential owned by a user,
// including ones served from memory, so a deactivated user's key is refused
// even if its in-memory record outlived the revocation. Credentials with no
// user (UserID is their own "key_..." id: the env bootstrap key, recovery
// keys, admin-issued push keys) skip it.
type UserGate interface {
	UserCanAuthenticate(tenantID, userID string) (bool, error)
}

// SetUserGate wires the user-status check. Nil disables it.
func (s *Store) SetUserGate(g UserGate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gate = g
}

// RevokeUserInMemory marks every in-memory credential owned by userID in
// tenantID revoked, WITHOUT writing through to the Persister. It is the
// after-commit step of deactivation (#1915): the durable revocation already
// committed in the transaction that disabled the user. Returns the number of
// records revoked.
func (s *Store) RevokeUserInMemory(tenantID, userID string) int {
	if userID == "" {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range s.byID {
		if r.state != stateActive || r.cred.TenantID != tenantID || r.cred.UserID != userID {
			continue
		}
		r.state = stateRevoked
		n++
	}
	return n
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

// AttachPersistence wires a Persister and rehydrates the in-memory maps
// from it (load-at-boot). Returns the number of credentials loaded. From
// this point on, issues/rotations/revocations write through to p and fail
// closed on persistence errors — a credential that would silently die at
// the next restart is worse than a failed issuance. Records already in
// memory (bootstrap credentials issued before the attach) are left as
// memory-only and keep authenticating via the legacy hash fallback.
// Attach at most once, before the store serves traffic.
func (s *Store) AttachPersistence(p Persister) (int, error) {
	if p == nil {
		return 0, errors.New("credstore: nil persister")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.persist != nil {
		return 0, errors.New("credstore: persistence already attached")
	}
	rows, err := p.LoadActive()
	if err != nil {
		return 0, fmt.Errorf("credstore: load persisted credentials: %w", err)
	}
	s.persist = p
	loaded := 0
	for _, pc := range rows {
		if _, exists := s.byID[pc.Cred.ID]; exists {
			continue
		}
		if _, exists := s.byTokenHash[pc.TokenHashHex]; exists {
			continue
		}
		r := &record{
			cred:      pc.Cred,
			tokenHash: pc.TokenHashHex,
			state:     stateActive,
			retiresAt: pc.RetiresAt,
			persisted: true,
		}
		s.byID[pc.Cred.ID] = r
		s.byTokenHash[r.tokenHash] = r
		loaded++
	}
	return loaded, nil
}

// hash maps a bearer plaintext to the store's lookup-map key: the
// Persister's at-rest hash when one is attached (HMAC-SHA256 per ADR 0002
// in production), the legacy unkeyed SHA-256 otherwise.
func (s *Store) hash(token string) string {
	if s.persist != nil {
		return s.persist.HashToken(token)
	}
	return hashToken(token)
}

// lookupTokenLocked resolves a plaintext to its record. Caller holds s.mu.
// With persistence attached it first tries the Persister's hash scheme,
// then falls back to the legacy SHA-256 keys — records issued before
// AttachPersistence (the cmd/atlas bootstrap credentials) live under the
// legacy scheme and must keep authenticating.
func (s *Store) lookupTokenLocked(token string) (*record, bool) {
	if r, ok := s.byTokenHash[s.hash(token)]; ok {
		return r, true
	}
	if s.persist != nil {
		if r, ok := s.byTokenHash[hashToken(token)]; ok {
			return r, true
		}
	}
	return nil, false
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

// IssueRecoveryAdmin mints the admin key reset-bootstrap writes to the
// bootstrap-token file (#1892): IssueAdmin, marked
// PurposeBootstrapRecovery, with the minting user recorded in MintedBy.
// mintedBy is a users(id) uuid or empty. It is attribution only: the
// key's UserID stays its own credential id, so the key never acts as, or
// counts as a key of, the user who minted it. ttl must be positive: a
// recovery key always expires.
func (s *Store) IssueRecoveryAdmin(tenantID string, ttl time.Duration, mintedBy string) (Credential, string, error) {
	if ttl <= 0 {
		return Credential{}, "", errors.New("credstore: recovery admin key needs a positive ttl")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cred, token, r, err := s.buildLocked(tenantID, "", nil, ttl, "", true, true, nil)
	if err != nil {
		return Credential{}, "", err
	}
	cred.Purpose = PurposeBootstrapRecovery
	cred.MintedBy = mintedBy
	r.cred = cred
	if err := s.commitLocked(cred, r); err != nil {
		return Credential{}, "", err
	}
	return cred, token, nil
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
//
// OE-435: fixed-admin credentials are NEVER written through to an attached
// Persister. The token is re-minted from ATLAS_BOOTSTRAP_TOKEN on every
// boot (it already survives restarts by construction), and persisting the
// same deterministic token each boot would collide with the api_keys
// token-hash uniqueness constraint.
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
	r := &record{cred: cred, tokenHash: s.hash(token), state: stateActive}
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

	var rec *record
	successor, bearer, rec, err = s.buildLocked(pred.cred.TenantID, pred.cred.ScopePredicate, pred.cred.Kinds, pred.cred.TTL, id, pred.cred.IsAdmin, pred.cred.IsApprover, pred.cred.OwnerRoles)
	if err != nil {
		return
	}
	// A rotated recovery key stays a recovery key, so rotation cannot
	// strip the marker the next reset and sign-in revoke by.
	successor.Purpose = pred.cred.Purpose
	rec.cred.Purpose = pred.cred.Purpose
	successor.MintedBy = pred.cred.MintedBy
	rec.cred.MintedBy = pred.cred.MintedBy

	predecessorExpiresAt = time.Now().UTC().Add(s.rotationGrace)
	if s.persist != nil {
		pc := PersistedCredential{Cred: successor, TokenHashHex: rec.tokenHash}
		if pred.persisted {
			if perr := s.persist.Rotate(pc, id, pred.cred.TenantID, predecessorExpiresAt); perr != nil {
				err = fmt.Errorf("credstore: persist rotate: %w", perr)
				return
			}
		} else {
			// The predecessor has no api_keys row (memory-only bootstrap
			// credential), so its retirement is memory-only too and the
			// successor's rotated_from must be cleared — the FK it names
			// does not exist.
			pc.Cred.RotatedFrom = ""
			if perr := s.persist.Insert(pc); perr != nil {
				err = fmt.Errorf("credstore: persist rotate successor: %w", perr)
				return
			}
		}
		rec.persisted = true
	}
	s.byID[successor.ID] = rec
	s.byTokenHash[rec.tokenHash] = rec
	pred.retiresAt = predecessorExpiresAt
	return
}

// Revoke invalidates the key immediately. Subsequent Authenticate calls
// with its bearer return ErrUnknownKey. Fails closed when the durable
// revocation cannot be recorded — an in-memory-only revoke would silently
// resurrect the key at the next restart.
func (s *Store) Revoke(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.byID[id]
	if !ok {
		return ErrUnknownKey
	}
	if s.persist != nil && r.persisted {
		if err := s.persist.Revoke(id, r.cred.TenantID); err != nil {
			return fmt.Errorf("credstore: persist revoke: %w", err)
		}
	}
	r.state = stateRevoked
	return nil
}

// RevokePurpose revokes every live credential, in any tenant, marked with
// purpose, except the one whose id is except (empty spares none). It
// matches on the marker and the id only, never on a bearer. Returns the
// revoked ids. Stops at the first durable-revoke failure and returns the
// ids revoked so far with the error.
func (s *Store) RevokePurpose(purpose, except string) ([]string, error) {
	if purpose == "" {
		return nil, errors.New("credstore: RevokePurpose needs a purpose")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var revoked []string
	for id, r := range s.byID {
		if r.state != stateActive || r.cred.Purpose != purpose || id == except {
			continue
		}
		if s.persist != nil && r.persisted {
			if err := s.persist.Revoke(id, r.cred.TenantID); err != nil {
				return revoked, fmt.Errorf("credstore: persist revoke: %w", err)
			}
		}
		r.state = stateRevoked
		revoked = append(revoked, id)
	}
	return revoked, nil
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
		if r.cred.expired(time.Now().UTC()) {
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
	r, ok := s.lookupTokenLocked(token)
	if !ok || r.state == stateRevoked {
		return ErrUnknownKey
	}
	r.cred.UserID = userID
	return nil
}

// Authenticate resolves a plaintext bearer to its credential. Updates
// LastUsedAt on success (in-memory only — the durable last_used_at column
// is a cosmetic mirror the auth hot path deliberately does not touch).
//
// A credential owned by a user is also checked against the UserGate, outside
// the lock so a slow DB never stalls other lookups. A deactivated owner
// refuses with ErrUnknownKey; a gate error fails closed.
func (s *Store) Authenticate(token string) (Credential, error) {
	cred, gate, err := s.authenticateLocked(token)
	if err != nil {
		return Credential{}, err
	}
	if gate != nil && OwnedByUser(cred) {
		ok, gerr := gate.UserCanAuthenticate(cred.TenantID, cred.UserID)
		if gerr != nil {
			return Credential{}, fmt.Errorf("credstore: user gate: %w", gerr)
		}
		if !ok {
			return Credential{}, ErrUnknownKey
		}
	}
	return cred, nil
}

func (s *Store) authenticateLocked(token string) (Credential, UserGate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.lookupTokenLocked(token)
	if !ok || r.state == stateRevoked {
		return Credential{}, nil, ErrUnknownKey
	}
	if !r.retiresAt.IsZero() && time.Now().UTC().After(r.retiresAt) {
		return Credential{}, nil, ErrUnknownKey
	}
	if r.cred.expired(time.Now().UTC()) {
		return Credential{}, nil, ErrUnknownKey
	}
	r.cred.LastUsedAt = time.Now().UTC()
	return r.cred, s.gate, nil
}

// OwnedByUser reports whether c belongs to a user: its UserID is a users(id)
// uuid rather than its own "key_..." credential id.
func OwnedByUser(c Credential) bool {
	if c.UserID == "" || c.UserID == c.ID {
		return false
	}
	_, err := uuid.Parse(c.UserID)
	return err == nil
}

// issueLocked inserts a new credential, writing through to the Persister
// when one is attached. Caller must hold s.mu.
func (s *Store) issueLocked(tenantID, scope string, kinds []string, ttl time.Duration, rotatedFrom string, isAdmin, isApprover bool, ownerRoles []string) (Credential, string, error) {
	cred, token, r, err := s.buildLocked(tenantID, scope, kinds, ttl, rotatedFrom, isAdmin, isApprover, ownerRoles)
	if err != nil {
		return Credential{}, "", err
	}
	if err := s.commitLocked(cred, r); err != nil {
		return Credential{}, "", err
	}
	return cred, token, nil
}

// commitLocked writes a built credential through to the Persister (when
// attached) and then makes it visible in the maps. Caller must hold s.mu.
func (s *Store) commitLocked(cred Credential, r *record) error {
	if s.persist != nil {
		if err := s.persist.Insert(PersistedCredential{Cred: cred, TokenHashHex: r.tokenHash}); err != nil {
			return fmt.Errorf("credstore: persist issue: %w", err)
		}
		r.persisted = true
	}
	s.byID[cred.ID] = r
	s.byTokenHash[r.tokenHash] = r
	return nil
}

// buildLocked constructs a credential + record WITHOUT inserting it into
// the maps or the Persister — callers (issueLocked, Rotate) own the commit
// so the write-through can happen before the record becomes visible.
// Caller must hold s.mu.
func (s *Store) buildLocked(tenantID, scope string, kinds []string, ttl time.Duration, rotatedFrom string, isAdmin, isApprover bool, ownerRoles []string) (Credential, string, *record, error) {
	var credID string
	if s.persist != nil {
		// Persister-issued ids round-trip through the backing table, so the
		// id a credential authenticates under today is the id it reloads
		// under after a restart.
		id, err := s.persist.NewID()
		if err != nil {
			return Credential{}, "", nil, err
		}
		credID = id
	} else {
		id, err := randomHex(16)
		if err != nil {
			return Credential{}, "", nil, err
		}
		credID = "key_" + id
	}
	token, err := randomHex(32)
	if err != nil {
		return Credential{}, "", nil, err
	}

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
	r := &record{cred: cred, tokenHash: s.hash(token), state: stateActive}
	return cred, token, r, nil
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
