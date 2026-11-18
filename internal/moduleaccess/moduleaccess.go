// Package moduleaccess is a member's module access (#2172, spec #2166,
// ADR 0025): which switched-on optional modules a tenant member may work
// in. The role says what a member can do; module access says where.
//
// Each tenant membership holds either "all", a wildcard that includes
// modules switched on later, or an explicit set of optional-module keys
// (featureflag.Modules). No stored row is "all", so new and existing
// members keep every module until an admin narrows them.
//
// Module access applies to grc_engineer, control_owner and viewer only. A
// member holding admin always has every switched-on module, and one holding
// auditor is scoped by audit periods instead; a narrowed value stored on
// either is kept and ignored.
//
// Gate is the access step of the authorization chain: after the module
// gate (a switched-off module answers 404 first, for every caller) and
// before the OPA role decision. A switched-on optional module outside the
// caller's access answers 403 naming the module.
//
// Module access never filters the evidence ledger, control evaluation,
// audit-binding output or saved snapshots (ADR 0025 decision 2).
package moduleaccess

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/featureflag"
	"github.com/mgoodric/security-atlas/internal/identity"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ErrUnknownModule is returned by Normalize for a key that is not an
// optional module. HTTP 400.
var ErrUnknownModule = errors.New("moduleaccess: not an optional module")

// Normalize validates an explicit module set and returns it deduplicated in
// registry order. nil means "all" and is returned as nil; an empty,
// non-nil set (core modules only) is returned empty and non-nil.
func Normalize(keys []string) ([]string, error) {
	if keys == nil {
		return nil, nil
	}
	want := make(map[string]bool, len(keys))
	for _, k := range keys {
		want[k] = true
	}
	out := []string{}
	for _, m := range featureflag.OptionalModules() {
		if want[m.Key] {
			out = append(out, m.Key)
			delete(want, m.Key)
		}
	}
	for k := range want {
		return nil, fmt.Errorf("%w: %q", ErrUnknownModule, k)
	}
	return out, nil
}

// Access is a caller's module access, resolved against their roles.
type Access struct {
	// Exempt is true for a caller module access does not apply to: an
	// admin, an auditor or a super-admin.
	Exempt bool
	// Modules is the stored explicit set; nil means "all".
	Modules []string
}

// Has reports whether the caller may work in the optional module key.
// Whether the module is switched on is a separate question (the module
// gate's).
func (a Access) Has(key string) bool {
	return a.Exempt || a.Modules == nil || slices.Contains(a.Modules, key)
}

// Store reads and writes member_module_access on the RLS-bound atlas_app
// pool; every statement runs under the request's tenant GUC.
type Store struct {
	pool  *pgxpool.Pool
	roles authz.RolesResolver
}

// NewStore constructs a Store. The roles resolver decides who is exempt,
// with the same live user_roles rows the OPA decision reads.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, roles: authz.NewDBRolesResolver(pool)}
}

// Get returns a member's stored module access: nil for "all". userKey is
// the user_roles key (identity.RolesKey).
func (s *Store) Get(ctx context.Context, tenantID uuid.UUID, userKey string) ([]string, error) {
	var out []string
	err := s.inTx(ctx, func(q *dbx.Queries) error {
		row, err := q.GetMemberModuleAccess(ctx, dbx.GetMemberModuleAccessParams{TenantID: pgUUID(tenantID), UserID: userKey})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		out = row.Modules
		return nil
	})
	return out, err
}

// List returns every stored module access in the tenant, keyed by
// user_roles key. A member with no entry has "all".
func (s *Store) List(ctx context.Context, tenantID uuid.UUID) (map[string][]string, error) {
	out := map[string][]string{}
	err := s.inTx(ctx, func(q *dbx.Queries) error {
		rows, err := q.ListMemberModuleAccess(ctx, pgUUID(tenantID))
		if err != nil {
			return err
		}
		for _, r := range rows {
			out[r.UserID] = r.Modules
		}
		return nil
	})
	return out, err
}

// Set stores a member's module access (nil for "all", else a Normalize'd
// set) and writes one member_module_access_audit_log row, before -> after,
// in the same transaction. The caller checks the member belongs to the
// tenant.
func (s *Store) Set(ctx context.Context, tenantID uuid.UUID, userKey string, modules []string, actor string) error {
	if actor == "" {
		return errors.New("moduleaccess: actor is required")
	}
	return s.inTx(ctx, func(q *dbx.Queries) error {
		var before []string
		row, err := q.GetMemberModuleAccessForUpdate(ctx, dbx.GetMemberModuleAccessForUpdateParams{TenantID: pgUUID(tenantID), UserID: userKey})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return fmt.Errorf("moduleaccess: read: %w", err)
		default:
			before = row.Modules
		}
		if err := q.UpsertMemberModuleAccess(ctx, dbx.UpsertMemberModuleAccessParams{
			TenantID: pgUUID(tenantID), UserID: userKey, Modules: modules, UpdatedBy: actor,
		}); err != nil {
			return fmt.Errorf("moduleaccess: upsert: %w", err)
		}
		if err := q.InsertMemberModuleAccessAudit(ctx, dbx.InsertMemberModuleAccessAuditParams{
			TenantID: pgUUID(tenantID), UserID: userKey, Before: before, After: modules, Actor: actor,
		}); err != nil {
			return fmt.Errorf("moduleaccess: audit: %w", err)
		}
		return nil
	})
}

// ForCaller resolves the request's caller's module access. A caller with
// no stored narrowing reads one row and never resolves roles; a narrowed
// caller is exempt when their credential or live user_roles hold admin or
// auditor, or they are a confirmed super-admin.
func (s *Store) ForCaller(r *http.Request) (Access, error) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || cred.TenantID == "" {
		return Access{Exempt: true}, nil
	}
	if c := jwtmw.FromContext(r.Context()); c != nil && c.SuperAdmin {
		return Access{Exempt: true}, nil
	}
	tenantID, err := uuid.Parse(cred.TenantID)
	if err != nil {
		return Access{}, fmt.Errorf("moduleaccess: tenant id: %w", err)
	}
	key := identity.RolesKey(cred.UserID)
	if key == "" {
		return Access{Exempt: true}, nil
	}
	modules, err := s.Get(r.Context(), tenantID, key)
	if err != nil {
		return Access{}, err
	}
	if modules == nil {
		return Access{}, nil
	}
	exempt, err := s.exempt(r, cred)
	if err != nil {
		return Access{}, err
	}
	return Access{Exempt: exempt, Modules: modules}, nil
}

// exempt reports whether the caller holds admin or auditor, from the
// roles their credential derives plus their live user_roles rows: the
// same union the OPA decision evaluates.
func (s *Store) exempt(r *http.Request, cred credstore.Credential) (bool, error) {
	roles := authz.BuildInput(r, nil).User.Roles
	live, err := s.roles.RolesFor(r.Context(), cred.TenantID, cred.UserID)
	if err != nil {
		return false, err
	}
	for _, role := range append(roles, live...) {
		if role == authz.RoleAdmin || role == authz.RoleAuditor {
			return true, nil
		}
	}
	return false, nil
}

func (s *Store) inTx(ctx context.Context, fn func(*dbx.Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("moduleaccess: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	if err := fn(dbx.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func pgUUID(u uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: u, Valid: true} }

// NoAccessBody is the 403 body for a module outside the caller's access.
type NoAccessBody struct {
	Error      string `json:"error"`
	Module     string `json:"module"`
	ModuleName string `json:"module_name"`
}

// GateOption configures Gate.
type GateOption func(*gateConfig)

type gateConfig struct {
	record func(*http.Request, featureflag.Module)
}

// WithDenyRecorder has Gate call record for every request it refuses as
// outside the caller's module access, after the 403 is written (#2466).
// Gate runs before the OPA step, which audits every decision it makes, so
// this is how a refused request leaves a trace.
func WithDenyRecorder(record func(*http.Request, featureflag.Module)) GateOption {
	return func(c *gateConfig) { c.record = record }
}

// Gate is the module access step of the authorization chain. It resolves
// the request path's owning module from the registry and, for an optional
// module outside the caller's access, answers 403 naming the module
// without reaching the OPA decision or the handler. Core routes, and
// callers module access does not apply to, fall through.
//
// Mount it after featureflag.ModuleGate, so a switched-off module answers
// 404 for everyone first (ADR 0025 decision 3), and before authzmw. Like
// the module gate it checks the raw path too, so an encoded "/" cannot
// route past it. A store error fails closed (500): unlike a module switch,
// access is a narrowing of who may see what.
func Gate(store *Store, opts ...GateOption) func(http.Handler) http.Handler {
	var cfg gateConfig
	for _, o := range opts {
		o(&cfg)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var owned []featureflag.Module
			for _, p := range []string{r.URL.Path, r.URL.RawPath} {
				if p == "" {
					continue
				}
				if o, err := featureflag.Resolve(p); err == nil && !o.Module.Core {
					owned = append(owned, o.Module)
				}
			}
			if len(owned) == 0 {
				next.ServeHTTP(w, r)
				return
			}
			access, err := store.ForCaller(r)
			if err != nil {
				httperr.WriteInternal(w, r, "module access", err)
				return
			}
			for _, m := range owned {
				if !access.Has(m.Key) {
					httpresp.WriteJSON(w, http.StatusForbidden, NoAccessBody{
						Error:      "You don't have access to " + m.Name + ". Ask an administrator.",
						Module:     m.Key,
						ModuleName: m.Name,
					})
					if cfg.record != nil {
						cfg.record(r, m)
					}
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
