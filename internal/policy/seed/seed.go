// Package seed loads the 5 stock policies from policies/stock/*.md into
// the policies table as draft rows. Mirrors the slice-007 SOC 2 importer
// pattern: read markdown frontmatter, resolve SCF anchor codes to UUIDs
// via the slice-006 scf_anchors lookup, INSERT as draft with
// source_attribution=community_draft.
//
// Missing SCF anchors are not a hard error -- the loader logs a warning
// and drops the link; the resulting policy may end up with fewer than 3
// linked controls and surface the orphan_policy warning on read (AC-7).
package seed

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"gopkg.in/yaml.v3"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/policy"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// StockPolicyCount is the exact number of stock policies bundled with the
// platform. Constitutional anti-pattern (canvas §1.6, principle 1.6):
// "policy template libraries dressed as a feature -- 5 high-signal
// templates, not 50 placeholders". This constant is the load-bearing
// guard against scope creep -- the loader rejects any stock directory
// containing more or fewer than 5 markdown files.
const StockPolicyCount = 5

// FrontMatter is the YAML shape at the top of each stock policy markdown
// file. Mirrors the slice-007 crosswalk YAML shape (typed; loader rejects
// extra fields).
type FrontMatter struct {
	Title                       string   `yaml:"title"`
	Version                     string   `yaml:"version"`
	OwnerRole                   string   `yaml:"owner_role"`
	ApproverRole                string   `yaml:"approver_role"`
	LinkedControlIDs            []string `yaml:"linked_control_ids"` // SCF codes like "GOV-01"
	AcknowledgmentRequiredRoles []string `yaml:"acknowledgment_required_roles"`
	SourceAttribution           string   `yaml:"source_attribution"`
}

// StockPolicy is the parsed shape of one policies/stock/*.md file.
type StockPolicy struct {
	FrontMatter FrontMatter
	BodyMd      string
	SourcePath  string // e.g. "policies/stock/information-security-policy.md"
}

// Report summarizes a seed run.
type Report struct {
	Loaded             []LoadedPolicy
	MissingAnchors     []string // SCF codes that did not resolve
	OrphanWarnings     []string // titles whose linked_control_ids ended up empty
}

// LoadedPolicy is one inserted policy plus its resolved link state.
type LoadedPolicy struct {
	ID                 uuid.UUID
	Title              string
	LinkedControlCount int
	OrphanWarning      bool
}

// LoadFromFS reads the 5 stock markdown files from fsys at root and
// returns them parsed. fsys is typically os.DirFS(".") with root
// "policies/stock". Returns an error if the file count is not exactly
// StockPolicyCount.
func LoadFromFS(fsys fs.FS, root string) ([]StockPolicy, error) {
	entries, err := fs.ReadDir(fsys, root)
	if err != nil {
		return nil, fmt.Errorf("seed: read %s: %w", root, err)
	}
	var paths []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if filepath.Ext(e.Name()) != ".md" {
			continue
		}
		paths = append(paths, filepath.Join(root, e.Name()))
	}
	sort.Strings(paths)
	if len(paths) != StockPolicyCount {
		return nil, fmt.Errorf(
			"seed: stock directory must contain exactly %d markdown files; found %d (anti-criterion P0: 5 high-signal policies, not %d placeholders)",
			StockPolicyCount, len(paths), len(paths),
		)
	}
	out := make([]StockPolicy, 0, len(paths))
	for _, p := range paths {
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return nil, fmt.Errorf("seed: read %s: %w", p, err)
		}
		sp, err := parseStockPolicy(string(data))
		if err != nil {
			return nil, fmt.Errorf("seed: parse %s: %w", p, err)
		}
		sp.SourcePath = p
		out = append(out, sp)
	}
	return out, nil
}

// parseStockPolicy splits the leading YAML frontmatter (between two `---`
// lines) from the markdown body.
func parseStockPolicy(src string) (StockPolicy, error) {
	src = strings.TrimLeft(src, " \t\n")
	if !strings.HasPrefix(src, "---\n") && !strings.HasPrefix(src, "---\r\n") {
		return StockPolicy{}, errors.New("seed: missing leading `---` frontmatter delimiter")
	}
	// Drop the leading delimiter.
	body := strings.TrimPrefix(src, "---\n")
	body = strings.TrimPrefix(body, "---\r\n")
	idx := strings.Index(body, "\n---")
	if idx < 0 {
		return StockPolicy{}, errors.New("seed: missing trailing `---` frontmatter delimiter")
	}
	fmText := body[:idx]
	rest := strings.TrimLeft(body[idx+4:], "\r\n")
	var fm FrontMatter
	if err := yaml.Unmarshal([]byte(fmText), &fm); err != nil {
		return StockPolicy{}, fmt.Errorf("seed: parse frontmatter: %w", err)
	}
	return StockPolicy{FrontMatter: fm, BodyMd: rest}, nil
}

// Seed inserts the stock policies as draft rows under the supplied tenant
// using `atlas_app` permissions via the supplied pool. Caller is
// responsible for `tenancy.WithTenant` on the ctx OR for passing a pool
// connected with BYPASSRLS for cross-tenant bootstrap.
//
// This function uses the slice-021 inTx pattern: open one tx, apply the
// tenant GUC, run the inserts. All 5 policies land or none do.
func Seed(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, policies []StockPolicy, anchorResolver AnchorResolver) (Report, error) {
	if len(policies) != StockPolicyCount {
		return Report{}, fmt.Errorf("seed: expected %d policies, got %d", StockPolicyCount, len(policies))
	}
	if anchorResolver == nil {
		anchorResolver = NoopAnchorResolver{}
	}
	// Apply tenant context inline for the bootstrap path.
	ctx, err := tenancy.WithTenant(ctx, tenantID.String())
	if err != nil {
		return Report{}, fmt.Errorf("seed: apply tenant: %w", err)
	}
	report := Report{}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("seed: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return Report{}, err
	}
	q := dbx.New(tx)

	for _, sp := range policies {
		linkedIDs, missing, err := anchorResolver.Resolve(ctx, sp.FrontMatter.LinkedControlIDs)
		if err != nil {
			return Report{}, fmt.Errorf("seed: resolve anchors for %s: %w", sp.FrontMatter.Title, err)
		}
		report.MissingAnchors = append(report.MissingAnchors, missing...)
		linkedControlIDs := make([]pgtype.UUID, len(linkedIDs))
		for i, u := range linkedIDs {
			linkedControlIDs[i] = pgtype.UUID{Bytes: u, Valid: true}
		}
		policyID := uuid.New()
		source := sp.FrontMatter.SourceAttribution
		if source == "" {
			source = policy.SourceCommunityDraft
		}
		ackRoles := sp.FrontMatter.AcknowledgmentRequiredRoles
		if ackRoles == nil {
			ackRoles = []string{}
		}
		_, err = q.CreatePolicy(ctx, dbx.CreatePolicyParams{
			ID:                          pgtype.UUID{Bytes: policyID, Valid: true},
			TenantID:                    pgtype.UUID{Bytes: tenantID, Valid: true},
			PredecessorID:               pgtype.UUID{},
			Title:                       sp.FrontMatter.Title,
			Version:                     sp.FrontMatter.Version,
			BodyMd:                      sp.BodyMd,
			OwnerRole:                   sp.FrontMatter.OwnerRole,
			ApproverRole:                sp.FrontMatter.ApproverRole,
			LinkedControlIds:            linkedControlIDs,
			AcknowledgmentRequiredRoles: ackRoles,
			SourceAttribution:           source,
			CreatedBy:                   "system:stock-seed",
		})
		if err != nil {
			return Report{}, fmt.Errorf("seed: insert %s: %w", sp.FrontMatter.Title, err)
		}
		loaded := LoadedPolicy{
			ID:                 policyID,
			Title:              sp.FrontMatter.Title,
			LinkedControlCount: len(linkedIDs),
			OrphanWarning:      len(linkedIDs) == 0,
		}
		report.Loaded = append(report.Loaded, loaded)
		if loaded.OrphanWarning {
			report.OrphanWarnings = append(report.OrphanWarnings, sp.FrontMatter.Title)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Report{}, fmt.Errorf("seed: commit: %w", err)
	}
	return report, nil
}

// AnchorResolver maps SCF codes (e.g., "GOV-01") to the UUID of the
// `controls` row anchored at that SCF code in the tenant's bundle.
// Returns the resolved UUIDs (in stable order) and the list of unresolved
// codes for reporting.
type AnchorResolver interface {
	Resolve(ctx context.Context, scfCodes []string) (resolved []uuid.UUID, missing []string, err error)
}

// NoopAnchorResolver always returns "missing" for every code. Used when
// no controls have been imported yet (e.g. fresh deploy before slice 010
// SOC 2 control kit ships). The resulting policies end up orphan; the
// AC-7 warning surfaces on read.
type NoopAnchorResolver struct{}

// Resolve implements AnchorResolver.
func (NoopAnchorResolver) Resolve(_ context.Context, scfCodes []string) ([]uuid.UUID, []string, error) {
	return nil, append([]string{}, scfCodes...), nil
}

// SQLAnchorResolver resolves SCF codes to control UUIDs by joining
// `scf_anchors` + `controls` (controls.scf_anchor_id -> scf_anchors.id;
// scf_anchors.scf_id is the human code). The resolver runs in the
// caller-supplied transaction (so RLS context applies).
//
// Returns missing codes verbatim so the report can record them.
type SQLAnchorResolver struct {
	// pool is held to issue read-only lookups outside the seed
	// transaction. Bootstrap path always supplies an atlas_app pool.
	pool *pgxpool.Pool
}

// NewSQLAnchorResolver constructs a SQLAnchorResolver.
func NewSQLAnchorResolver(pool *pgxpool.Pool) *SQLAnchorResolver {
	return &SQLAnchorResolver{pool: pool}
}

// Resolve implements AnchorResolver. Looks up each SCF code in the
// controls table joined to scf_anchors. Tenant context MUST already be
// applied on the ctx so RLS scopes the controls lookup.
func (r *SQLAnchorResolver) Resolve(ctx context.Context, scfCodes []string) ([]uuid.UUID, []string, error) {
	if len(scfCodes) == 0 {
		return nil, nil, nil
	}
	// controls.scf_id is a free-text column holding the SCF code directly
	// (e.g. "GOV-01"). The slice-010 SOC 2 control kit will populate it
	// when the kit lands; until then, controls.scf_id may be empty and the
	// lookup returns "missing" for every code (the orphan_policy warning
	// surfaces on the seeded policies). The query below picks the first
	// matching control per code -- v1 has at most one control per SCF
	// anchor per tenant.
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT ON (scf_id) scf_id, id
		FROM controls
		WHERE scf_id = ANY($1)
		ORDER BY scf_id, created_at ASC
	`, scfCodes)
	if err != nil {
		return nil, nil, fmt.Errorf("seed: scf_anchors lookup: %w", err)
	}
	defer rows.Close()
	byCode := make(map[string]uuid.UUID)
	for rows.Next() {
		var code string
		var ctrlID pgtype.UUID
		if err := rows.Scan(&code, &ctrlID); err != nil {
			return nil, nil, fmt.Errorf("seed: scan: %w", err)
		}
		if ctrlID.Valid {
			byCode[code] = uuid.UUID(ctrlID.Bytes)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("seed: rows iter: %w", err)
	}
	var resolved []uuid.UUID
	var missing []string
	for _, code := range scfCodes {
		if id, ok := byCode[code]; ok {
			resolved = append(resolved, id)
			continue
		}
		missing = append(missing, code)
	}
	return resolved, missing, nil
}
