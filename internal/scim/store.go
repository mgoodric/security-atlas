package scim

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// pgUniqueViolation is the Postgres SQLSTATE for unique_violation (23505).
const pgUniqueViolation = "23505"

// SCIM audit actions (mirror the scim_audit_log CHECK constraint).
const (
	ActionProvision   = "user.provision"
	ActionReplace     = "user.replace"
	ActionPatch       = "user.patch"
	ActionDeprovision = "user.deprovision"
	ActionReprovision = "user.reprovision"
	ActionDelete      = "user.delete"
)

// ErrUserNotFound is returned when no user matches under the tenant.
var ErrUserNotFound = errors.New("scim: user not found")

// ErrConflict is returned when a Create collides with an existing user
// (duplicate externalId or email in the tenant). RFC 7644 §3.3 maps this to
// 409 with scimType=uniqueness.
var ErrConflict = errors.New("scim: user already exists")

// DomainUser is the platform-side projection of a provisioned user. The
// handler renders this into the SCIM wire User.
type DomainUser struct {
	ID          uuid.UUID
	TenantID    uuid.UUID
	Email       string
	DisplayName string
	ExternalID  string
	Active      bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Store is the DB-backed SCIM provisioning store. Every method runs under
// app.current_tenant RLS (invariant #6 / P0-508-4) — the tenant comes from the
// authenticated SCIM credential, never from the request body.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store bound to the RLS app pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// ProvisionInput is the attribute allow-list for a SCIM Create. NOTE there is
// no Roles field by design (P0-508-3): SCIM provisions identity + active only.
type ProvisionInput struct {
	UserName    string // maps to email
	DisplayName string
	ExternalID  string
	Active      bool
}

// Provision creates a SCIM-managed user (AC-1 Create). It reconciles against
// an existing row: if a user with the same externalId OR email already exists
// in the tenant, it returns ErrConflict (RFC 7644 §3.3 — the IdP should GET/
// PATCH instead). Writes a user.provision audit row (AC-5).
func (s *Store) Provision(ctx context.Context, actorCredentialID uuid.UUID, tenantID string, in ProvisionInput) (DomainUser, error) {
	tIDU, err := uuidToPg(tenantID)
	if err != nil {
		return DomainUser{}, err
	}
	var out DomainUser
	err = s.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		// Reconcile: a matching externalId or email is a conflict.
		if in.ExternalID != "" {
			if _, gErr := q.GetSCIMUserByExternalID(ctx, dbx.GetSCIMUserByExternalIDParams{
				TenantID: tIDU, ScimExternalID: strPtr(in.ExternalID),
			}); gErr == nil {
				return ErrConflict
			} else if !errors.Is(gErr, pgx.ErrNoRows) {
				return gErr
			}
		}
		if _, gErr := q.GetSCIMUserByEmail(ctx, dbx.GetSCIMUserByEmailParams{
			TenantID: tIDU, Email: in.UserName,
		}); gErr == nil {
			return ErrConflict
		} else if !errors.Is(gErr, pgx.ErrNoRows) {
			return gErr
		}

		row, cErr := q.CreateSCIMUser(ctx, dbx.CreateSCIMUserParams{
			ID:             pgtype.UUID{Bytes: uuid.New(), Valid: true},
			TenantID:       tIDU,
			Email:          in.UserName,
			DisplayName:    in.DisplayName,
			ScimExternalID: strPtrOrNil(in.ExternalID),
		})
		if cErr != nil {
			if isUniqueViolation(cErr) {
				return ErrConflict
			}
			return fmt.Errorf("scim: create user: %w", cErr)
		}
		// A Create with active=false provisions then immediately deprovisions.
		if !in.Active {
			row, cErr = q.SetSCIMUserActive(ctx, dbx.SetSCIMUserActiveParams{
				TenantID: tIDU, ID: row.ID, Active: false,
			})
			if cErr != nil {
				return fmt.Errorf("scim: create-inactive: %w", cErr)
			}
		}
		out = domainUserFromRow(row)
		return s.writeAudit(ctx, q, tIDU, actorCredentialID, out.ID, ActionProvision, map[string]any{
			"external_id": in.ExternalID,
			"active":      out.Active,
		})
	})
	if err != nil {
		return DomainUser{}, err
	}
	return out, nil
}

// GetByID returns a SCIM-managed user under the tenant (AC-1 Get).
func (s *Store) GetByID(ctx context.Context, tenantID string, id uuid.UUID) (DomainUser, error) {
	tIDU, err := uuidToPg(tenantID)
	if err != nil {
		return DomainUser{}, err
	}
	var out DomainUser
	err = s.inTxReadOnly(ctx, func(ctx context.Context, q *dbx.Queries) error {
		row, gErr := q.GetUserByID(ctx, dbx.GetUserByIDParams{
			TenantID: tIDU, ID: pgtype.UUID{Bytes: id, Valid: true},
		})
		if gErr != nil {
			if errors.Is(gErr, pgx.ErrNoRows) {
				return ErrUserNotFound
			}
			return gErr
		}
		out = domainUserFromRow(row)
		return nil
	})
	if err != nil {
		return DomainUser{}, err
	}
	return out, nil
}

// List returns a page of tenant users (AC-1 List, no filter). RLS confines to
// the credential's tenant (P0-508-4). Returns the page + total count.
func (s *Store) List(ctx context.Context, tenantID string, limit, offset int) ([]DomainUser, int, error) {
	tIDU, err := uuidToPg(tenantID)
	if err != nil {
		return nil, 0, err
	}
	var users []DomainUser
	var total int
	err = s.inTxReadOnly(ctx, func(ctx context.Context, q *dbx.Queries) error {
		rows, lErr := q.ListSCIMUsers(ctx, dbx.ListSCIMUsersParams{
			TenantID: tIDU, Limit: int32(limit), Offset: int32(offset),
		})
		if lErr != nil {
			return lErr
		}
		for _, r := range rows {
			users = append(users, domainUserFromRow(r))
		}
		cnt, cErr := q.CountSCIMUsers(ctx, tIDU)
		if cErr != nil {
			return cErr
		}
		total = int(cnt)
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return users, total, nil
}

// FindByUserName returns the users matching `filter=userName eq "x"` (AC-1).
// userName maps to email. Empty result is not an error (SCIM returns an empty
// ListResponse).
func (s *Store) FindByUserName(ctx context.Context, tenantID, userName string) ([]DomainUser, error) {
	tIDU, err := uuidToPg(tenantID)
	if err != nil {
		return nil, err
	}
	var users []DomainUser
	err = s.inTxReadOnly(ctx, func(ctx context.Context, q *dbx.Queries) error {
		rows, lErr := q.ListSCIMUsersByUserName(ctx, dbx.ListSCIMUsersByUserNameParams{
			TenantID: tIDU, Email: userName,
		})
		if lErr != nil {
			return lErr
		}
		for _, r := range rows {
			users = append(users, domainUserFromRow(r))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return users, nil
}

// ReplaceInput is the SCIM Replace (PUT) attribute allow-list. No roles.
type ReplaceInput struct {
	UserName    string // email
	DisplayName string
	Active      bool
}

// Replace overwrites the mutable SCIM attributes (AC-1 Replace). When the
// Replace flips active false, sessions are revoked in the same transaction
// (AC-4). Writes a user.replace (or user.deprovision on a false flip) audit
// row.
func (s *Store) Replace(ctx context.Context, actorCredentialID uuid.UUID, tenantID string, id uuid.UUID, in ReplaceInput) (DomainUser, error) {
	tIDU, err := uuidToPg(tenantID)
	if err != nil {
		return DomainUser{}, err
	}
	var out DomainUser
	err = s.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		prev, gErr := q.GetUserByID(ctx, dbx.GetUserByIDParams{
			TenantID: tIDU, ID: pgtype.UUID{Bytes: id, Valid: true},
		})
		if gErr != nil {
			if errors.Is(gErr, pgx.ErrNoRows) {
				return ErrUserNotFound
			}
			return gErr
		}
		row, rErr := q.ReplaceSCIMUser(ctx, dbx.ReplaceSCIMUserParams{
			TenantID:    tIDU,
			ID:          pgtype.UUID{Bytes: id, Valid: true},
			DisplayName: in.DisplayName,
			Email:       in.UserName,
			Active:      in.Active,
		})
		if rErr != nil {
			return fmt.Errorf("scim: replace user: %w", rErr)
		}
		out = domainUserFromRow(row)
		action := ActionReplace
		if prev.Active && !in.Active {
			if rErr := s.revokeSessions(ctx, q, tIDU, id); rErr != nil {
				return rErr
			}
			action = ActionDeprovision
		} else if !prev.Active && in.Active {
			action = ActionReprovision
		}
		return s.writeAudit(ctx, q, tIDU, actorCredentialID, id, action, map[string]any{
			"active": in.Active,
		})
	})
	if err != nil {
		return DomainUser{}, err
	}
	return out, nil
}

// Patch applies a SCIM PatchOp (AC-1 Patch). It honors ONLY the attribute
// allow-list: `active` (the deprovision flip) and `displayName`. A path
// referencing `roles` (or any unknown attribute) is IGNORED — never an error,
// never a role mutation (P0-508-3). A no-path replace value object is read for
// `active` / `displayName` only.
func (s *Store) Patch(ctx context.Context, actorCredentialID uuid.UUID, tenantID string, id uuid.UUID, ops []PatchOperation) (DomainUser, error) {
	tIDU, err := uuidToPg(tenantID)
	if err != nil {
		return DomainUser{}, err
	}
	intent, perr := planPatch(ops)
	if perr != nil {
		return DomainUser{}, perr
	}
	var out DomainUser
	err = s.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		prev, gErr := q.GetUserByID(ctx, dbx.GetUserByIDParams{
			TenantID: tIDU, ID: pgtype.UUID{Bytes: id, Valid: true},
		})
		if gErr != nil {
			if errors.Is(gErr, pgx.ErrNoRows) {
				return ErrUserNotFound
			}
			return gErr
		}
		row := prev
		if intent.setDisplayName {
			row, gErr = q.PatchSCIMUserDisplayName(ctx, dbx.PatchSCIMUserDisplayNameParams{
				TenantID: tIDU, ID: pgtype.UUID{Bytes: id, Valid: true}, DisplayName: intent.displayName,
			})
			if gErr != nil {
				return fmt.Errorf("scim: patch display_name: %w", gErr)
			}
		}
		action := ActionPatch
		if intent.setActive {
			row, gErr = q.SetSCIMUserActive(ctx, dbx.SetSCIMUserActiveParams{
				TenantID: tIDU, ID: pgtype.UUID{Bytes: id, Valid: true}, Active: intent.active,
			})
			if gErr != nil {
				return fmt.Errorf("scim: patch active: %w", gErr)
			}
			if prev.Active && !intent.active {
				if rErr := s.revokeSessions(ctx, q, tIDU, id); rErr != nil {
					return rErr
				}
				action = ActionDeprovision
			} else if !prev.Active && intent.active {
				action = ActionReprovision
			}
		}
		out = domainUserFromRow(row)
		return s.writeAudit(ctx, q, tIDU, actorCredentialID, id, action, map[string]any{
			"set_active":       intent.setActive,
			"active":           intent.active,
			"set_display_name": intent.setDisplayName,
		})
	})
	if err != nil {
		return DomainUser{}, err
	}
	return out, nil
}

// Delete soft-disables the user (AC-4 / P0-508-1): DELETE never hard-deletes.
// It is equivalent to a deprovision — active=false + sessions revoked + the
// row retained so the actor's historical records survive (invariant #2).
// Writes a user.delete audit row.
func (s *Store) Delete(ctx context.Context, actorCredentialID uuid.UUID, tenantID string, id uuid.UUID) error {
	tIDU, err := uuidToPg(tenantID)
	if err != nil {
		return err
	}
	return s.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		if _, gErr := q.GetUserByID(ctx, dbx.GetUserByIDParams{
			TenantID: tIDU, ID: pgtype.UUID{Bytes: id, Valid: true},
		}); gErr != nil {
			if errors.Is(gErr, pgx.ErrNoRows) {
				return ErrUserNotFound
			}
			return gErr
		}
		if _, sErr := q.SetSCIMUserActive(ctx, dbx.SetSCIMUserActiveParams{
			TenantID: tIDU, ID: pgtype.UUID{Bytes: id, Valid: true}, Active: false,
		}); sErr != nil {
			return fmt.Errorf("scim: delete (disable): %w", sErr)
		}
		if rErr := s.revokeSessions(ctx, q, tIDU, id); rErr != nil {
			return rErr
		}
		return s.writeAudit(ctx, q, tIDU, actorCredentialID, id, ActionDelete, map[string]any{
			"soft_disabled": true,
		})
	})
}

// --- internals ---

// patchIntent is the resolved effect of a PatchOp after applying the
// attribute allow-list. Anything outside {active, displayName} is dropped.
type patchIntent struct {
	setActive      bool
	active         bool
	setDisplayName bool
	displayName    string
}

// planPatch walks the operations and extracts ONLY the allow-listed
// attributes. Role/group/unknown paths are silently skipped (P0-508-3). Only
// `add` / `replace` ops mutate; `remove` of `active` is treated as a no-op
// here (deprovision is an explicit active=false, not a remove).
func planPatch(ops []PatchOperation) (patchIntent, error) {
	var intent patchIntent
	for _, op := range ops {
		switch normalizeOp(op.Op) {
		case "add", "replace":
			// fallthrough to value handling
		default:
			// unknown/remove op: ignore (no error — be liberal in what we accept)
			continue
		}
		path := normalizePath(op.Path)
		switch path {
		case "active":
			b, ok := decodeBool(op.Value)
			if !ok {
				return patchIntent{}, fmt.Errorf("scim: PatchOp `active` value must be boolean")
			}
			intent.setActive = true
			intent.active = b
		case "displayname":
			str, ok := decodeString(op.Value)
			if !ok {
				return patchIntent{}, fmt.Errorf("scim: PatchOp `displayName` value must be string")
			}
			intent.setDisplayName = true
			intent.displayName = str
		case "":
			// No-path replace: value is an object of attributes (RFC 7644
			// §3.5.2.3). Read ONLY active + displayName; ignore everything
			// else (notably `roles` — P0-508-3).
			var obj map[string]json.RawMessage
			if err := json.Unmarshal(op.Value, &obj); err != nil {
				// Not an object — nothing allow-listed to apply; skip.
				continue
			}
			for k, raw := range obj {
				switch normalizePath(k) {
				case "active":
					if b, ok := decodeBool(raw); ok {
						intent.setActive = true
						intent.active = b
					}
				case "displayname":
					if str, ok := decodeString(raw); ok {
						intent.setDisplayName = true
						intent.displayName = str
					}
					// any other key (roles, groups, ...) is ignored.
				}
			}
		default:
			// roles / groups / emails / unknown attribute path: ignore.
			continue
		}
	}
	return intent, nil
}

func (s *Store) revokeSessions(ctx context.Context, q *dbx.Queries, tenantID pgtype.UUID, userID uuid.UUID) error {
	if _, err := q.RevokeAllSCIMUserSessions(ctx, dbx.RevokeAllSCIMUserSessionsParams{
		TenantID: tenantID,
		UserID:   pgtype.UUID{Bytes: userID, Valid: true},
	}); err != nil {
		return fmt.Errorf("scim: revoke sessions: %w", err)
	}
	return nil
}

func (s *Store) writeAudit(ctx context.Context, q *dbx.Queries, tenantID pgtype.UUID, actorCredentialID uuid.UUID, targetUserID uuid.UUID, action string, detail map[string]any) error {
	payload, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("scim: marshal audit detail: %w", err)
	}
	if err := q.InsertSCIMAuditLog(ctx, dbx.InsertSCIMAuditLogParams{
		TenantID:          tenantID,
		ActorCredentialID: pgtype.UUID{Bytes: actorCredentialID, Valid: true},
		TargetUserID:      pgtype.UUID{Bytes: targetUserID, Valid: true},
		Action:            action,
		Detail:            payload,
	}); err != nil {
		return fmt.Errorf("scim: insert audit: %w", err)
	}
	return nil
}

func (s *Store) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	if err := fn(ctx, dbx.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) inTxReadOnly(ctx context.Context, fn func(context.Context, *dbx.Queries) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	if err := fn(ctx, dbx.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func domainUserFromRow(row dbx.User) DomainUser {
	d := DomainUser{
		ID:          uuid.UUID(row.ID.Bytes),
		TenantID:    uuid.UUID(row.TenantID.Bytes),
		Email:       row.Email,
		DisplayName: row.DisplayName,
		Active:      row.Active,
	}
	if row.ScimExternalID != nil {
		d.ExternalID = *row.ScimExternalID
	}
	if row.CreatedAt.Valid {
		d.CreatedAt = row.CreatedAt.Time
	}
	if row.UpdatedAt.Valid {
		d.UpdatedAt = row.UpdatedAt.Time
	}
	return d
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation
}

func strPtr(s string) *string { return &s }

func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
