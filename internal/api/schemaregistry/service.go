package schemaregistry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/evidence/redact"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Service is the DB-backed registry. It pairs an InMemory cache (hot path
// for IsRegistered) with sqlc queries for persistence and a JSON Schema
// validator for payload checking. Slice 014 introduces the type;
// future slices (013, 015+) call it the same way.
type Service struct {
	pool  *pgxpool.Pool
	cache *InMemory

	mu        sync.RWMutex
	compiled  map[string]*jsonschema.Schema    // kind|semver -> compiled validator (global rows)
	byKey     map[string]storedSchema          // kind|semver -> last-known stored schema (global rows)
	tenantSch map[string]map[string]*tenantRow // tenantID -> kind|semver -> compiled+stored
	// Slice 015: per-(kind|semver) redaction rules extracted from each
	// schema's x-redaction-rules extension key at load time. Empty when
	// the schema declares no rules. Read on the push hot path so we
	// avoid re-parsing the schema body per record.
	redaction map[string][]string
}

type storedSchema struct {
	id                pgtype.UUID
	kind              string
	semver            string
	schemaJSON        []byte
	owner             string
	defaultSCFAnchors []string
}

type tenantRow struct {
	compiled  *jsonschema.Schema
	stored    storedSchema
	redaction []string
}

// NewService constructs a DB-backed Service. The pool is the app-role
// pool — RLS is enforced for tenant-scoped writes through the
// `app.current_tenant` GUC, which the caller (HTTP/gRPC layer) sets
// per request.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{
		pool:      pool,
		cache:     New(nil),
		compiled:  map[string]*jsonschema.Schema{},
		byKey:     map[string]storedSchema{},
		tenantSch: map[string]map[string]*tenantRow{},
		redaction: map[string][]string{},
	}
}

// IsRegistered reports whether (kind, semver) is in the global cache. The
// gRPC evidence service uses this on the hot path; cache is loaded at boot
// by ImportPlatformSchemas + LoadFromDB.
func (s *Service) IsRegistered(kind, version string) bool {
	return s.cache.IsRegistered(kind, version)
}

// ImportPlatformSchemas walks the embedded schemas FS, inserts every
// (kind, semver) that is not already present in the DB as a global row,
// and seeds the in-memory cache. Idempotent — re-running with the same
// FS is a no-op. Must run as a BYPASSRLS connection (atlas_migrate) because
// global rows have tenant_id NULL and atlas_app's RLS does not permit
// INSERT with tenant_id NULL.
//
// Returns the inserted count + the total count after import. The caller
// (cmd/atlas/main.go) logs both at boot.
func (s *Service) ImportPlatformSchemas(ctx context.Context, root fs.FS) (inserted, total int, err error) {
	schemas, err := LoadPlatformSchemas(root)
	if err != nil {
		return 0, 0, fmt.Errorf("load platform schemas: %w", err)
	}
	q := dbx.New(s.pool)
	for _, ps := range schemas {
		sem, err := ParseSemver(ps.Semver)
		if err != nil {
			return inserted, 0, fmt.Errorf("schema %s/%s: %w", ps.Kind, ps.Semver, err)
		}
		// Skip if already present.
		_, err = q.GetEvidenceKindSchemaGlobal(ctx, dbx.GetEvidenceKindSchemaGlobalParams{
			Kind:   ps.Kind,
			Semver: ps.Semver,
		})
		if err == nil {
			continue
		}
		// Compile up-front so a malformed bundled schema is caught at boot.
		if _, err := compileSchema(ps.SchemaJSON); err != nil {
			return inserted, 0, fmt.Errorf("compile %s/%s: %w", ps.Kind, ps.Semver, err)
		}
		anchors := ps.DefaultSCFAnchors
		if anchors == nil {
			anchors = []string{}
		}
		_, err = q.InsertEvidenceKindSchema(ctx, dbx.InsertEvidenceKindSchemaParams{
			ID:                pgtype.UUID{Bytes: uuid.New(), Valid: true},
			TenantID:          pgtype.UUID{Valid: false},
			Kind:              ps.Kind,
			Semver:            ps.Semver,
			Major:             int32(sem.Major),
			Minor:             int32(sem.Minor),
			Patch:             int32(sem.Patch),
			SchemaJson:        ps.SchemaJSON,
			Owner:             ps.Owner,
			DefaultScfAnchors: anchors,
			CreatedBy:         "platform-bundled",
		})
		if err != nil {
			return inserted, 0, fmt.Errorf("insert %s/%s: %w", ps.Kind, ps.Semver, err)
		}
		inserted++
	}
	// Refresh cache from DB so subsequent IsRegistered/Validate calls see
	// every global row (including ones we just inserted).
	if err := s.LoadFromDB(ctx); err != nil {
		return inserted, 0, err
	}
	s.mu.RLock()
	total = len(s.byKey)
	s.mu.RUnlock()
	return inserted, total, nil
}

// LoadFromDB hydrates the in-memory cache + compiled-schema map from the
// `evidence_kind_schemas` table. Reads global rows only — tenant rows are
// loaded on demand inside the request path via getTenantRow.
func (s *Service) LoadFromDB(ctx context.Context) error {
	q := dbx.New(s.pool)
	rows, err := q.ListEvidenceKindSchemasGlobal(ctx, dbx.ListEvidenceKindSchemasGlobalParams{
		Limit:  10000,
		Offset: 0,
	})
	if err != nil {
		return fmt.Errorf("list global schemas: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.compiled = map[string]*jsonschema.Schema{}
	s.byKey = map[string]storedSchema{}
	s.redaction = map[string][]string{}
	s.cache = New(nil)
	for _, r := range rows {
		compiled, err := compileSchema(r.SchemaJson)
		if err != nil {
			return fmt.Errorf("compile %s/%s: %w", r.Kind, r.Semver, err)
		}
		key := cacheKey(r.Kind, r.Semver)
		s.compiled[key] = compiled
		s.byKey[key] = storedSchema{
			id:                r.ID,
			kind:              r.Kind,
			semver:            r.Semver,
			schemaJSON:        r.SchemaJson,
			owner:             r.Owner,
			defaultSCFAnchors: r.DefaultScfAnchors,
		}
		// Slice 015: extract x-redaction-rules. A malformed list is
		// fatal at load — we'd rather refuse to start than silently
		// fail-open on secret redaction.
		rules, rerr := redact.ExtractRulesFromSchema(r.SchemaJson)
		if rerr != nil {
			return fmt.Errorf("redaction rules %s/%s: %w", r.Kind, r.Semver, rerr)
		}
		if len(rules) > 0 {
			s.redaction[key] = rules
		}
		s.cache.Register(r.Kind, r.Semver)
	}
	return nil
}

// RedactionRulesFor implements ingest.RedactionLookup (slice 015). Returns
// the JSONPath rule list for (tenantID, kind, semver) — tenant-private
// rows shadow global rows. Empty slice + nil error means "no rules".
func (s *Service) RedactionRulesFor(ctx context.Context, tenantID, kind, version string) ([]string, error) {
	if tenantID != "" {
		s.mu.RLock()
		if m, ok := s.tenantSch[tenantID]; ok {
			if row, ok := m[cacheKey(kind, version)]; ok {
				out := append([]string(nil), row.redaction...)
				s.mu.RUnlock()
				return out, nil
			}
		}
		s.mu.RUnlock()
	}
	s.mu.RLock()
	rules, ok := s.redaction[cacheKey(kind, version)]
	s.mu.RUnlock()
	if !ok {
		// Tenant-private kinds might exist that haven't been hydrated
		// into the tenant cache yet. Defer to lookupCompiled to load
		// the row, then re-check the tenant cache. We re-use the
		// existing slow path rather than duplicating it.
		if tenantID != "" {
			if _, err := s.lookupCompiled(ctx, tenantID, kind, version); err == nil {
				s.mu.RLock()
				defer s.mu.RUnlock()
				if m, ok := s.tenantSch[tenantID]; ok {
					if row, ok := m[cacheKey(kind, version)]; ok {
						return append([]string(nil), row.redaction...), nil
					}
				}
			}
		}
		return nil, nil
	}
	return append([]string(nil), rules...), nil
}

// ValidatePayload checks `payload` (the raw JSON bytes of an evidence
// record's payload field) against the JSON Schema for (kind, semver).
// Slice 013 calls this on the push hot path. Tenant-private kinds win
// over a global kind of the same (kind, semver). Returns nil on conform;
// ErrUnknownKind if the (kind, semver) is not registered; or a wrapped
// validation error.
func (s *Service) ValidatePayload(ctx context.Context, tenantID, kind, version string, payload []byte) error {
	compiled, err := s.lookupCompiled(ctx, tenantID, kind, version)
	if err != nil {
		return err
	}

	var v any
	if err := json.Unmarshal(payload, &v); err != nil {
		return fmt.Errorf("decode payload: %w", err)
	}
	if err := compiled.Validate(v); err != nil {
		return fmt.Errorf("schema %s/%s rejected payload: %w", kind, version, err)
	}
	return nil
}

// ErrUnknownKind is returned by ValidatePayload / Get when (kind, semver)
// is not registered.
var ErrUnknownKind = errors.New("schemaregistry: unknown (kind, semver)")

// ErrUnauthorized is returned by Register when the caller's credential is
// not flagged admin. Translates to HTTP 403 at the handler.
var ErrUnauthorized = errors.New("schemaregistry: admin credential required")

// ErrEmptyOwner is returned by Register when the request omits owner.
var ErrEmptyOwner = errors.New("schemaregistry: owner is required")

// ErrAnonymous is returned by Register when no credential is present in
// context. Translates to HTTP 401 at the handler.
var ErrAnonymous = errors.New("schemaregistry: registration requires an authenticated credential")

// RegisterRequest is the input to Service.Register.
type RegisterRequest struct {
	// TenantID is the tenant the private kind belongs to. Empty string is
	// rejected — admin-only registration of GLOBAL kinds is handled via
	// the bundled-schema importer, not the HTTP API. Anti-criterion: no
	// admin can silently inject into the global namespace at runtime.
	TenantID   string
	Kind       string
	Semver     string
	SchemaJSON []byte
	Owner      string
	// IsAdmin is true when the calling credential is marked admin. The
	// handler (api/schemaregistry HTTP) extracts this from the credential
	// in context; Service.Register treats !IsAdmin as ErrUnauthorized.
	IsAdmin       bool
	HasCredential bool
	CreatedBy     string
	// DefaultSCFAnchors is the optional list of SCF anchor IDs the
	// platform should default to when this evidence kind is referenced.
	DefaultSCFAnchors []string
}

// RegisteredSchema is what Register / Get return.
type RegisteredSchema struct {
	ID                string
	TenantID          string // empty when global
	Kind              string
	Semver            string
	SchemaJSON        json.RawMessage
	Owner             string
	DefaultSCFAnchors []string
	CreatedBy         string
}

// Register inserts a tenant-private (kind, semver). Enforces:
//
//   - HasCredential = false → ErrAnonymous (no anonymous registration)
//   - IsAdmin = false → ErrUnauthorized
//   - Owner empty → ErrEmptyOwner
//   - TenantID empty → ErrUnauthorized (global registration not via HTTP)
//   - Semver not parseable → semver error
//   - AC-5 semver rules → SemverConflict
//   - Schema fails to compile (malformed JSON Schema) → compile error
//   - Minor bump requires additive over the prior highest (kind, major.X)
//     → additive error
//   - DB UNIQUE violation on (tenant_id, kind, semver) → wrapped duplicate
func (s *Service) Register(ctx context.Context, req RegisterRequest) (RegisteredSchema, error) {
	if !req.HasCredential {
		return RegisteredSchema{}, ErrAnonymous
	}
	if !req.IsAdmin {
		return RegisteredSchema{}, ErrUnauthorized
	}
	if req.TenantID == "" {
		// Reject silently-globalizing private kinds — the global namespace is
		// owned by the bundled-schema importer, not the HTTP API.
		return RegisteredSchema{}, ErrUnauthorized
	}
	if req.Owner == "" {
		return RegisteredSchema{}, ErrEmptyOwner
	}
	if req.Kind == "" {
		return RegisteredSchema{}, fmt.Errorf("kind is required")
	}
	tenantUUID, err := uuid.Parse(req.TenantID)
	if err != nil {
		return RegisteredSchema{}, fmt.Errorf("tenant_id must be a UUID: %w", err)
	}
	proposed, err := ParseSemver(req.Semver)
	if err != nil {
		return RegisteredSchema{}, err
	}
	if _, err := compileSchema(req.SchemaJSON); err != nil {
		return RegisteredSchema{}, fmt.Errorf("schema is not valid JSON Schema 2020-12: %w", err)
	}

	// All RLS-bound work runs inside a transaction with the tenant GUC
	// set. The SELECTs for the version list need it for tenant rows to
	// be visible; the INSERT needs it for the WITH CHECK policy to pass.
	var out RegisteredSchema
	err = pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		tenantCtx, terr := tenancy.WithTenant(ctx, req.TenantID)
		if terr != nil {
			return terr
		}
		if terr := tenancy.ApplyTenant(tenantCtx, tx); terr != nil {
			return terr
		}
		q := dbx.New(tx)

		// AC-5: gather all existing versions visible to the tenant for
		// this kind (global rows are always visible; tenant rows visible
		// only via the GUC just applied).
		existing, lerr := q.ListEvidenceKindSchemaVersionsForKind(ctx, dbx.ListEvidenceKindSchemaVersionsForKindParams{
			TenantID: pgtype.UUID{Bytes: tenantUUID, Valid: true},
			Kind:     req.Kind,
		})
		if lerr != nil {
			return fmt.Errorf("list existing versions: %w", lerr)
		}
		existingSemvers := make([]Semver, 0, len(existing))
		for _, r := range existing {
			sv, perr := ParseSemver(r.Semver)
			if perr != nil {
				continue
			}
			existingSemvers = append(existingSemvers, sv)
		}
		if serr := EnforceSemver(req.Kind, proposed, existingSemvers); serr != nil {
			return serr
		}

		// Additive check for minor bumps over the prior highest on the
		// same major.
		if hasSameMajor(existingSemvers, proposed.Major) && proposed.Minor > 0 && proposed.Patch == 0 {
			prior := highestOnMajor(existing, proposed.Major)
			if prior != nil && proposed.Major == int(prior.Major) && proposed.Minor == int(prior.Minor)+1 {
				if aerr := CheckAdditiveOver(prior.SchemaJson, req.SchemaJSON); aerr != nil {
					return aerr
				}
			}
		}

		anchors := req.DefaultSCFAnchors
		if anchors == nil {
			anchors = []string{}
		}
		row, ierr := q.InsertEvidenceKindSchema(ctx, dbx.InsertEvidenceKindSchemaParams{
			ID:                pgtype.UUID{Bytes: uuid.New(), Valid: true},
			TenantID:          pgtype.UUID{Bytes: tenantUUID, Valid: true},
			Kind:              req.Kind,
			Semver:            req.Semver,
			Major:             int32(proposed.Major),
			Minor:             int32(proposed.Minor),
			Patch:             int32(proposed.Patch),
			SchemaJson:        req.SchemaJSON,
			Owner:             req.Owner,
			DefaultScfAnchors: anchors,
			CreatedBy:         req.CreatedBy,
		})
		if ierr != nil {
			return fmt.Errorf("insert: %w", ierr)
		}
		out = RegisteredSchema{
			ID:                uuid.UUID(row.ID.Bytes).String(),
			TenantID:          req.TenantID,
			Kind:              row.Kind,
			Semver:            row.Semver,
			SchemaJSON:        row.SchemaJson,
			Owner:             row.Owner,
			DefaultSCFAnchors: row.DefaultScfAnchors,
			CreatedBy:         row.CreatedBy,
		}
		return nil
	})
	if err != nil {
		return RegisteredSchema{}, err
	}
	return out, nil
}

// List returns every (kind, semver) visible to tenantID — global rows plus
// the tenant's private rows. If tenantID is empty, only global rows are
// returned. Wrapped in a transaction with the tenant GUC set so RLS lets
// tenant rows through (global rows are always visible).
func (s *Service) List(ctx context.Context, tenantID string, limit, offset int32) ([]RegisteredSchema, error) {
	if tenantID == "" {
		rows, err := dbx.New(s.pool).ListEvidenceKindSchemasGlobal(ctx, dbx.ListEvidenceKindSchemasGlobalParams{
			Limit: limit, Offset: offset,
		})
		if err != nil {
			return nil, err
		}
		return toRegisteredSlice(rows), nil
	}
	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		return nil, fmt.Errorf("tenant_id must be a UUID: %w", err)
	}
	var out []RegisteredSchema
	err = pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		tctx, terr := tenancy.WithTenant(ctx, tenantID)
		if terr != nil {
			return terr
		}
		if terr := tenancy.ApplyTenant(tctx, tx); terr != nil {
			return terr
		}
		rows, lerr := dbx.New(tx).ListEvidenceKindSchemasForTenant(ctx, dbx.ListEvidenceKindSchemasForTenantParams{
			TenantID: pgtype.UUID{Bytes: tenantUUID, Valid: true},
			Limit:    limit, Offset: offset,
		})
		if lerr != nil {
			return lerr
		}
		out = toRegisteredSlice(rows)
		return nil
	})
	return out, err
}

// Get returns one (kind, semver) visible to tenantID. Tenant-private rows
// shadow global rows when both exist for the same (kind, semver). Wrapped
// in a tenant-GUC transaction so RLS lets the tenant row through.
func (s *Service) Get(ctx context.Context, tenantID, kind, version string) (RegisteredSchema, error) {
	if tenantID == "" {
		row, err := dbx.New(s.pool).GetEvidenceKindSchemaGlobal(ctx, dbx.GetEvidenceKindSchemaGlobalParams{
			Kind: kind, Semver: version,
		})
		if err != nil {
			return RegisteredSchema{}, ErrUnknownKind
		}
		return toRegistered(row), nil
	}
	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		return RegisteredSchema{}, fmt.Errorf("tenant_id must be a UUID: %w", err)
	}
	var out RegisteredSchema
	var unknown bool
	err = pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		tctx, terr := tenancy.WithTenant(ctx, tenantID)
		if terr != nil {
			return terr
		}
		if terr := tenancy.ApplyTenant(tctx, tx); terr != nil {
			return terr
		}
		row, gerr := dbx.New(tx).GetEvidenceKindSchemaForTenant(ctx, dbx.GetEvidenceKindSchemaForTenantParams{
			TenantID: pgtype.UUID{Bytes: tenantUUID, Valid: true},
			Kind:     kind, Semver: version,
		})
		if gerr != nil {
			unknown = true
			return nil
		}
		out = toRegistered(row)
		return nil
	})
	if err != nil {
		return RegisteredSchema{}, err
	}
	if unknown {
		return RegisteredSchema{}, ErrUnknownKind
	}
	return out, nil
}

// lookupCompiled returns the compiled validator for (tenantID, kind,
// semver). Loads from DB on cache miss. Tenant rows shadow global rows.
func (s *Service) lookupCompiled(ctx context.Context, tenantID, kind, version string) (*jsonschema.Schema, error) {
	// Tenant-private first.
	if tenantID != "" {
		s.mu.RLock()
		if m, ok := s.tenantSch[tenantID]; ok {
			if row, ok := m[cacheKey(kind, version)]; ok {
				s.mu.RUnlock()
				return row.compiled, nil
			}
		}
		s.mu.RUnlock()
	}
	// Then global.
	s.mu.RLock()
	if c, ok := s.compiled[cacheKey(kind, version)]; ok {
		s.mu.RUnlock()
		return c, nil
	}
	s.mu.RUnlock()

	// Slow path: hit the DB. Lets a private kind registered via Register
	// resolve without a full LoadFromDB refresh.
	if tenantID != "" {
		row, err := s.Get(ctx, tenantID, kind, version)
		if err == nil && row.TenantID == tenantID {
			compiled, err := compileSchema(row.SchemaJSON)
			if err != nil {
				return nil, fmt.Errorf("compile tenant schema: %w", err)
			}
			s.mu.Lock()
			if s.tenantSch[tenantID] == nil {
				s.tenantSch[tenantID] = map[string]*tenantRow{}
			}
			rules, rerr := redact.ExtractRulesFromSchema(row.SchemaJSON)
			if rerr != nil {
				s.mu.Unlock()
				return nil, fmt.Errorf("tenant redaction rules: %w", rerr)
			}
			s.tenantSch[tenantID][cacheKey(kind, version)] = &tenantRow{
				compiled: compiled,
				stored: storedSchema{
					kind:              row.Kind,
					semver:            row.Semver,
					schemaJSON:        row.SchemaJSON,
					owner:             row.Owner,
					defaultSCFAnchors: row.DefaultSCFAnchors,
				},
				redaction: rules,
			}
			s.mu.Unlock()
			return compiled, nil
		}
	}
	return nil, ErrUnknownKind
}

// InvalidateTenant drops the cached compiled validators for a tenant so a
// subsequent Validate call rereads from the DB. Called by Register after
// a successful insert.
func (s *Service) InvalidateTenant(tenantID string) {
	s.mu.Lock()
	delete(s.tenantSch, tenantID)
	s.mu.Unlock()
}

// ---- helpers ----

func compileSchema(body []byte) (*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	// Use a stable, file-like URL so $id references inside the schema
	// resolve consistently.
	url := "mem://schema"
	if err := c.AddResource(url, doc); err != nil {
		return nil, err
	}
	return c.Compile(url)
}

func cacheKey(kind, semver string) string { return kind + "|" + semver }

func toRegistered(row dbx.EvidenceKindSchema) RegisteredSchema {
	out := RegisteredSchema{
		ID:                uuid.UUID(row.ID.Bytes).String(),
		Kind:              row.Kind,
		Semver:            row.Semver,
		SchemaJSON:        row.SchemaJson,
		Owner:             row.Owner,
		DefaultSCFAnchors: row.DefaultScfAnchors,
		CreatedBy:         row.CreatedBy,
	}
	if row.TenantID.Valid {
		out.TenantID = uuid.UUID(row.TenantID.Bytes).String()
	}
	return out
}

func toRegisteredSlice(rows []dbx.EvidenceKindSchema) []RegisteredSchema {
	out := make([]RegisteredSchema, len(rows))
	for i, r := range rows {
		out[i] = toRegistered(r)
	}
	return out
}

func hasSameMajor(existing []Semver, major int) bool {
	for _, e := range existing {
		if e.Major == major {
			return true
		}
	}
	return false
}

func highestOnMajor(rows []dbx.EvidenceKindSchema, major int) *dbx.EvidenceKindSchema {
	var best *dbx.EvidenceKindSchema
	for i, r := range rows {
		if int(r.Major) != major {
			continue
		}
		if best == nil ||
			r.Minor > best.Minor ||
			(r.Minor == best.Minor && r.Patch > best.Patch) {
			best = &rows[i]
		}
	}
	return best
}

// Ensure Service satisfies both Registry and PayloadValidator at compile
// time. (PayloadValidator takes ctx + tenantID; the interface above takes
// only kind/version — we expose the simpler shape on a thin adapter so
// the existing evidence service does not have to change.)
var _ Registry = (*Service)(nil)
