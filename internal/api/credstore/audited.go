package credstore

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrAuditUnavailable is returned by the audited issue, rotate and revoke
// when the attached Persister cannot write the audit row. They fail closed
// rather than make an unaudited change.
var ErrAuditUnavailable = errors.New("credstore: persister cannot audit key changes")

// AuditedPersister is a Persister that writes the admin audit row for an
// issue, rotate or revoke in the same transaction as the change, so the
// change cannot commit without it (#2652). by is the caller who asked for
// the change. apikeystore.Persister implements it over me_audit_log.
type AuditedPersister interface {
	// InsertAudited records a newly issued credential and its issue row.
	InsertAudited(ctx context.Context, rec PersistedCredential, by Credential) error
	// RotateAudited records a rotation and its rotate row. When
	// predecessorPersisted is false the predecessor has no api_keys row, so
	// only the successor is inserted.
	RotateAudited(ctx context.Context, successor PersistedCredential, predecessor Credential, predecessorPersisted bool, retiresAt time.Time, by Credential) error
	// RevokeAudited records a revocation and its revoke row. When persisted
	// is false the credential has no api_keys row and only the row is
	// written. A key whose row is already revoked writes nothing.
	RevokeAudited(ctx context.Context, cred Credential, persisted bool, by Credential) error
}

// auditor returns the attached Persister as an AuditedPersister. With no
// Persister there is no database to audit into and ok is true with a nil
// auditor: the change stays memory-only, as every change on such a store
// does. A Persister that cannot audit is ErrAuditUnavailable.
func (s *Store) auditor() (AuditedPersister, error) {
	if s.persist == nil {
		return nil, nil
	}
	a, ok := s.persist.(AuditedPersister)
	if !ok {
		return nil, ErrAuditUnavailable
	}
	return a, nil
}

// IssueMintedAudited is IssueMinted for an admin caller, with the minter
// taken from by and the api_key_issue audit row written in the same
// transaction as the key (#2652). The gRPC AdminCredentialsService.Issue
// path.
func (s *Store) IssueMintedAudited(ctx context.Context, by Credential, tenantID, scope string, kinds []string, ttl time.Duration) (Credential, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.auditor()
	if err != nil {
		return Credential{}, "", err
	}
	cred, token, r, err := s.buildLocked(tenantID, scope, kinds, ttl, "", false, false, nil)
	if err != nil {
		return Credential{}, "", err
	}
	cred.MintedBy = MinterUserID(by)
	r.cred = cred
	if a != nil {
		if err := a.InsertAudited(ctx, PersistedCredential{Cred: cred, TokenHashHex: r.tokenHash}, by); err != nil {
			return Credential{}, "", fmt.Errorf("credstore: persist issue: %w", err)
		}
		r.persisted = true
	}
	s.byID[cred.ID] = r
	s.byTokenHash[r.tokenHash] = r
	return cred, token, nil
}

// RotateAudited is Rotate with the api_key_rotate audit row written in the
// same transaction as the rotation (#2652).
func (s *Store) RotateAudited(ctx context.Context, by Credential, id string) (successor Credential, bearer string, predecessorExpiresAt time.Time, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.auditor()
	if err != nil {
		return Credential{}, "", time.Time{}, err
	}
	return s.rotateLocked(id, func(pc PersistedCredential, pred *record, retiresAt time.Time) error {
		if perr := a.RotateAudited(ctx, pc, pred.cred, pred.persisted, retiresAt, by); perr != nil {
			return fmt.Errorf("credstore: persist rotate: %w", perr)
		}
		return nil
	})
}

// RevokeAudited is Revoke with the api_key_revoke audit row written in the
// same transaction as the revocation (#2652). Revoking a key already
// revoked here changes nothing and writes no row.
func (s *Store) RevokeAudited(ctx context.Context, by Credential, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.auditor()
	if err != nil {
		return err
	}
	r, ok := s.byID[id]
	if !ok {
		return ErrUnknownKey
	}
	if r.state == stateRevoked {
		return nil
	}
	if a != nil {
		if err := a.RevokeAudited(ctx, r.cred, r.persisted, by); err != nil {
			return fmt.Errorf("credstore: persist revoke: %w", err)
		}
	}
	r.state = stateRevoked
	return nil
}
