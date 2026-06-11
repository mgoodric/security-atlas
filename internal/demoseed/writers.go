package demoseed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// writeTenantRow inserts the demo tenant row into `tenants`. Pattern
// mirrors internal/api/admintenants/handler.go's Create path.
func writeTenantRow(ctx context.Context, tx pgx.Tx, t tenantRow, actorUserID uuid.UUID) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO tenants (id, name, slug, is_bootstrap_tenant, created_by_user_id)
		 VALUES ($1, $2, $3, false, $4)`,
		t.ID, t.Name, t.Slug, nullableUUID(actorUserID),
	)
	if err != nil {
		return fmt.Errorf("demoseed: insert tenant: %w", err)
	}
	return nil
}

// writeScopeDimensionAndCell inserts the default `environment`
// dimension + "All" cell. Mirrors the admintenants Create path.
func writeScopeDimensionAndCell(ctx context.Context, tx pgx.Tx, fs *fixtureSet) error {
	if _, err := tx.Exec(ctx,
		`INSERT INTO scope_dimensions
		 (id, tenant_id, name, value_type, allowed_values, is_required, is_builtin)
		 VALUES ($1, $2, 'environment', 'string', $3::jsonb, FALSE, TRUE)`,
		fs.scope.DimensionID, fs.tenant.ID,
		`["prod", "staging", "dev"]`,
	); err != nil {
		return fmt.Errorf("demoseed: insert scope_dimension: %w", err)
	}
	dimensions := `{"environment":"prod"}`
	hash := sha256.Sum256([]byte(dimensions))
	if _, err := tx.Exec(ctx,
		`INSERT INTO scope_cells
		 (id, tenant_id, label, dimensions, dimensions_hash)
		 VALUES ($1, $2, $3, $4::jsonb, $5)`,
		fs.scope.CellID, fs.tenant.ID, "All",
		`{"environment": "prod"}`, hex.EncodeToString(hash[:]),
	); err != nil {
		return fmt.Errorf("demoseed: insert scope_cell: %w", err)
	}
	return nil
}

// writeUserAndCreds inserts the demo administrator user + their
// local_credentials row + the admin role grant. demo_only=TRUE flags
// the row for the slice-205 forensic mark + the slice-142 promotion
// guard (P0-A2 + threat-model "E EoP").
func writeUserAndCreds(ctx context.Context, tx pgx.Tx, u userRow, passwordHash string) error {
	if _, err := tx.Exec(ctx,
		`INSERT INTO users (id, tenant_id, email, display_name, status, demo_only)
		 VALUES ($1, $2, $3, $4, 'active', TRUE)`,
		u.ID, currentTenantOf(ctx), u.Email, u.Name,
	); err != nil {
		return fmt.Errorf("demoseed: insert user: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO local_credentials (user_id, tenant_id, password_hash, algo, params)
		 VALUES ($1, $2, $3, 'argon2id', '{}'::jsonb)`,
		u.ID, currentTenantOf(ctx), passwordHash,
	); err != nil {
		return fmt.Errorf("demoseed: insert local_credentials: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO user_roles (tenant_id, user_id, role, granted_by)
		 VALUES ($1, $2, 'admin', 'system:demoseed')`,
		currentTenantOf(ctx), u.ID.String(),
	); err != nil {
		return fmt.Errorf("demoseed: insert user_roles: %w", err)
	}
	return nil
}

// writeAuditLogRow writes ONE super_admin_audit_log row + ONE
// me_audit_log row, both stamped with the action ('demo_seed_apply'
// or 'demo_seed_teardown'). The payload_json carries the slice-205
// forensic mark (`demo_seed_v`).
//
// If actorTenantID is uuid.Nil (CLI-direct invocation without a
// session-tenant context), the me_audit_log row anchors to the demo
// tenant instead so the row is still tagged for forensic filtering.
func writeAuditLogRow(ctx context.Context, tx pgx.Tx, action string,
	actorUserID uuid.UUID, actorTenantID uuid.UUID, demoTenantID uuid.UUID,
	payload map[string]any,
) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("demoseed: marshal audit payload: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO super_admin_audit_log
		 (action, target_user_id, actor_user_id, actor_tenant_id, payload_json)
		 VALUES ($1, $2, $3, $4, $5::jsonb)`,
		action,
		nonZeroOrSelf(actorUserID),
		nonZeroOrSelf(actorUserID),
		nonZeroOrTenant(actorTenantID, demoTenantID),
		payloadBytes,
	); err != nil {
		return fmt.Errorf("demoseed: insert super_admin_audit_log: %w", err)
	}
	tenantForMe := actorTenantID
	if tenantForMe == uuid.Nil {
		tenantForMe = demoTenantID
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO me_audit_log (tenant_id, user_id, action, before, after)
		 VALUES ($1, $2, $3, '{}'::jsonb, $4::jsonb)`,
		tenantForMe, nonZeroOrSelf(actorUserID), action, payloadBytes,
	); err != nil {
		return fmt.Errorf("demoseed: insert me_audit_log: %w", err)
	}
	return nil
}

// writeControls bulk-inserts the controls fixture set. Returns the
// row count written.
func writeControls(ctx context.Context, tx pgx.Tx, fs *fixtureSet) (int, error) {
	for _, c := range fs.controls {
		var scfPtr *string
		if c.SCFID != "" {
			s := c.SCFID
			scfPtr = &s
		}
		// bundle_id is a slice-009-introduced NOT NULL TEXT column —
		// the canonical "control bundle" identifier. Each demo control
		// is its own bundle (no supersession chain), so we synthesize
		// a unique slug per row. The `controls_one_active_version_per_bundle`
		// partial UNIQUE index permits exactly one active version per
		// (tenant_id, bundle_id) — our 1-bundle-per-row mapping
		// satisfies it trivially.
		bundleID := fmt.Sprintf("demo-bundle-%s", c.ID.String()[:8])
		if _, err := tx.Exec(ctx,
			`INSERT INTO controls
			 (id, tenant_id, scf_id, title, description, control_family,
			  implementation_type, owner_role, lifecycle_state, applicability_expr,
			  bundle_id)
			 VALUES ($1, $2, $3, $4, $5, $6, $7::control_implementation_type, $8, $9::control_lifecycle_state, 'true',
			         $10)`,
			c.ID, fs.tenant.ID, scfPtr, c.Title, c.Description, c.Family,
			c.ImplementationType, c.OwnerRole, c.Lifecycle,
			bundleID,
		); err != nil {
			return 0, fmt.Errorf("demoseed: insert control %s: %w", c.Title, err)
		}
	}
	return len(fs.controls), nil
}

// writeRisks bulk-inserts the risks fixture set + their risk_control_links.
func writeRisks(ctx context.Context, tx pgx.Tx, fs *fixtureSet) (int, error) {
	for _, r := range fs.risks {
		// org_unit_id + themes are slice-052 columns the slice-678 seed
		// populates so /risks/hierarchy renders (org tree needs the
		// org_unit binding; the theme × org_unit heatmap needs BOTH a
		// non-NULL org_unit_id AND a non-empty themes array). org_unit_id
		// is nil-safe: nullableUUID passes NULL when the org tree is empty.
		if _, err := tx.Exec(ctx,
			`INSERT INTO risks
			 (id, tenant_id, title, description, category, treatment, treatment_owner,
			  inherent_score, residual_score, review_due_at, org_unit_id, themes)
			 VALUES ($1, $2, $3, $4, $5::risk_category, $6::risk_treatment, $7, $8::jsonb, $9::jsonb, $10, $11, $12)`,
			r.ID, fs.tenant.ID, r.Title, r.Description,
			r.Category, r.Treatment, r.TreatmentOwner,
			r.InherentScoreJ, r.ResidualScoreJ, r.ReviewDueAt,
			nullableUUID(r.OrgUnitID), r.Themes,
		); err != nil {
			return 0, fmt.Errorf("demoseed: insert risk %s: %w", r.Title, err)
		}
		// Link the risk to its associated control.
		if _, err := tx.Exec(ctx,
			`INSERT INTO risk_control_links (risk_id, control_id, tenant_id)
			 VALUES ($1, $2, $3)`,
			r.ID, r.LinkedControlID, fs.tenant.ID,
		); err != nil {
			return 0, fmt.Errorf("demoseed: insert risk_control_link: %w", err)
		}
	}
	return len(fs.risks), nil
}

// writePolicies bulk-inserts the policies fixture set. Schema evolved
// since slice 002 (slice 019/020 added owner_role/approver_role/source_attribution/
// created_by columns + the published-status-requires-effective_date CHECK).
// The demo ships all policies as 'published'.
func writePolicies(ctx context.Context, tx pgx.Tx, fs *fixtureSet) (int, error) {
	for _, p := range fs.policies {
		// acknowledgment_required_roles is the ack-roster denominator key
		// (slice 678 / ATLAS-037). An empty array reproduces the "no
		// required-role users" empty state; we set a non-empty set so the
		// roster has a real denominator. text[] arg passed as a Go slice —
		// pgx encodes []string to text[] directly.
		if _, err := tx.Exec(ctx,
			`INSERT INTO policies
			 (id, tenant_id, title, version, effective_date, body_md,
			  owner_role, approver_role, status, source_attribution, created_by,
			  published_at, published_by, acknowledgment_required_roles)
			 VALUES ($1, $2, $3, '1.0.0', $4, $5,
			         $6, $7, $8, 'tenant_authored', $9,
			         $10, $9, $11)`,
			p.ID, fs.tenant.ID, p.Title, p.EffectiveDate, p.BodyMD,
			p.Owner, p.Approver, p.Status, fs.user.Email,
			p.EffectiveDate.UTC(), // published_at — wall-clock; reuse the effective_date day
			p.RequiredRoles,
		); err != nil {
			return 0, fmt.Errorf("demoseed: insert policy %s: %w", p.Title, err)
		}
	}
	return len(fs.policies), nil
}

// writeVendors bulk-inserts the vendors fixture set.
func writeVendors(ctx context.Context, tx pgx.Tx, fs *fixtureSet) (int, error) {
	for _, v := range fs.vendors {
		var dpaSignedAtPtr *time.Time
		if v.DPASignedAt != nil {
			dpaSignedAtPtr = v.DPASignedAt
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO vendors
			 (id, tenant_id, name, domain, criticality, contract_start, contract_end,
			  dpa_signed, dpa_signed_at, review_cadence, last_review_date, owner_user, notes)
			 VALUES ($1, $2, $3, $4, $5::vendor_criticality, $6, $7, $8, $9, $10::vendor_review_cadence, $11, $12, $13)`,
			v.ID, fs.tenant.ID, v.Name, v.Domain, v.Criticality,
			v.ContractStart, v.ContractEnd, v.DPASigned, dpaSignedAtPtr,
			v.Cadence, v.LastReviewDate, v.OwnerUser, v.Notes,
		); err != nil {
			return 0, fmt.Errorf("demoseed: insert vendor %s: %w", v.Name, err)
		}
	}
	return len(fs.vendors), nil
}

// writeAuditPeriodsAndSamples inserts audit_periods + their populations
// + samples + sample_evidence rows. The first period is frozen per
// AC-10. Returns counts: (auditPeriods, populations, samples,
// sampleEvidence).
//
// Framework version IDs are resolved by reading `frameworks` for the
// SCF row (the bundled catalog). If no SCF framework_version row
// exists in the test DB, the seeder creates a tenant-scoped "demo"
// framework + version row so the audit-periods FK is satisfied.
func writeAuditPeriodsAndSamples(ctx context.Context, tx pgx.Tx, fs *fixtureSet) (int, int, int, int, error) {
	// Look up an existing SCF framework_version_id (global catalog).
	// If not present, create a tenant-scoped demo framework so the
	// audit periods can still reference a valid framework version.
	var fwVersionID uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT id FROM framework_versions
		WHERE tenant_id IS NULL
		ORDER BY created_at ASC
		LIMIT 1
	`).Scan(&fwVersionID)
	if errors.Is(err, pgx.ErrNoRows) {
		// Create a tenant-scoped demo framework + version.
		fwID := uuid.New()
		if _, err := tx.Exec(ctx,
			`INSERT INTO frameworks (id, tenant_id, name, slug, issuer)
			 VALUES ($1, $2, 'Demo Framework', 'demo-framework', 'demo-seed')`,
			fwID, fs.tenant.ID,
		); err != nil {
			return 0, 0, 0, 0, fmt.Errorf("demoseed: insert demo framework: %w", err)
		}
		fwVersionID = uuid.New()
		if _, err := tx.Exec(ctx,
			`INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
			 VALUES ($1, $2, $3, '1.0', 'current')`,
			fwVersionID, fs.tenant.ID, fwID,
		); err != nil {
			return 0, 0, 0, 0, fmt.Errorf("demoseed: insert demo framework_version: %w", err)
		}
	} else if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("demoseed: lookup framework_version: %w", err)
	}
	// Cache for writeFrameworkScopes.
	fs.frameworkVersionIDs = append(fs.frameworkVersionIDs, fwVersionID)

	apCount := 0
	popCount := 0
	sampleCount := 0
	sampleEvidenceCount := 0

	for i := range fs.auditPeriods {
		ap := &fs.auditPeriods[i]
		ap.FrameworkVersionID = fwVersionID
		_, err := tx.Exec(ctx,
			`INSERT INTO audit_periods
			 (id, tenant_id, name, framework_version_id, period_start, period_end,
			  status, frozen_at, frozen_hash, frozen_by, created_by)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			ap.ID, fs.tenant.ID, ap.Name, ap.FrameworkVersionID,
			ap.PeriodStart, ap.PeriodEnd,
			periodStatus(ap.Frozen),
			ap.FrozenAt,
			frozenHashOrNil(ap),
			frozenByOrNil(ap),
			ap.CreatedBy,
		)
		if err != nil {
			return 0, 0, 0, 0, fmt.Errorf("demoseed: insert audit_period %s: %w", ap.Name, err)
		}
		apCount++

		// Each audit period gets one population + one sample drawn
		// from a small subset of the evidence rows (which are
		// inserted later — order matters here; we capture the IDs
		// from the fixture but the actual evidence INSERT happens in
		// writeEvidence).
		popID := uuid.New()
		// Population time-window covers the period.
		_, err = tx.Exec(ctx,
			`INSERT INTO populations
			 (id, tenant_id, control_id, scope_predicate, time_window_start,
			  time_window_end, frozen_at, audit_period_id, created_by, row_count)
			 VALUES ($1, $2, $3, $4::jsonb, $5, $6, $7, $8, $9, $10)`,
			popID, fs.tenant.ID, fs.controls[i%len(fs.controls)].ID,
			`{"op":"true"}`,
			ap.PeriodStart, ap.PeriodEnd,
			ap.FrozenAt, ap.ID, fs.user.Email,
			samplesPerPeriod,
		)
		if err != nil {
			return 0, 0, 0, 0, fmt.Errorf("demoseed: insert population: %w", err)
		}
		popCount++

		sampleID := uuid.New()
		_, err = tx.Exec(ctx,
			`INSERT INTO samples
			 (id, tenant_id, population_id, n, seed, created_by)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			sampleID, fs.tenant.ID, popID, samplesPerPeriod,
			fmt.Sprintf("demo-sample-seed-%d", i),
			fs.user.Email,
		)
		if err != nil {
			return 0, 0, 0, 0, fmt.Errorf("demoseed: insert sample: %w", err)
		}
		sampleCount++

		// Remember the sample id so we can attach sample_evidence rows
		// after evidence rows land. Stash via the fixtureSet rather
		// than thread it through the return path.
		ap.SampleEvidenceIDs = append(ap.SampleEvidenceIDs, sampleID)
	}
	return apCount, popCount, sampleCount, sampleEvidenceCount, nil
}

// writeEvidence inserts every evidence_record fixture + emits a small
// number of sample_evidence rows linking the first samplesPerPeriod
// evidence rows to each audit period's sample.
//
// Returns the evidence row count + the distinct list of evidence
// kinds used (for D3 / Result.EvidenceKindsUsed).
func writeEvidence(ctx context.Context, tx pgx.Tx, fs *fixtureSet) (int, []string, error) {
	kindsSet := map[string]struct{}{}
	for i, e := range fs.evidence {
		payloadBytes, err := json.Marshal(e.Payload)
		if err != nil {
			return 0, nil, fmt.Errorf("demoseed: marshal payload: %w", err)
		}
		provBytes, err := json.Marshal(e.Provenance)
		if err != nil {
			return 0, nil, fmt.Errorf("demoseed: marshal provenance: %w", err)
		}
		// scope_id references the `scopes` table, NOT `scope_cells`.
		// The demo seed only creates a scope_cell; pass nil for scope_id
		// since the demo doesn't need scope-resolved evidence.
		// control_ref is a NOT NULL text column (slice 014's
		// connector-driven evidence path). Use the control's UUID
		// string as the reference token.
		_, err = tx.Exec(ctx,
			`INSERT INTO evidence_records
			 (id, tenant_id, control_id, control_ref, scope_id,
			  observed_at, ingested_at,
			  provenance, result, payload, hash, freshness_class,
			  evidence_kind, ingestion_path)
			 VALUES ($1, $2, $3, $4, NULL,
			         $5, now(),
			         $6::jsonb, $7::evidence_result, $8::jsonb, $9, $10::evidence_freshness_class,
			         $11, 'push')`,
			e.ID, fs.tenant.ID, e.ControlID, e.ControlID.String(),
			e.ObservedAt,
			provBytes, e.Result, payloadBytes, e.HashHex, e.FreshnessCl,
			e.EvidenceKind,
		)
		if err != nil {
			return 0, nil, fmt.Errorf("demoseed: insert evidence record idx=%d: %w", i, err)
		}
		kindsSet[e.EvidenceKind] = struct{}{}
	}

	// Attach a few evidence rows to each sample (sample_evidence).
	// The PK is (sample_id, evidence_record_id) so ordinal is a
	// position-within-sample integer starting at 0.
	evidenceIdx := 0
	for i := range fs.auditPeriods {
		ap := &fs.auditPeriods[i]
		for _, sampleID := range ap.SampleEvidenceIDs {
			for j := 0; j < samplesPerPeriod; j++ {
				if evidenceIdx >= len(fs.evidence) {
					break
				}
				_, err := tx.Exec(ctx,
					`INSERT INTO sample_evidence
					 (tenant_id, sample_id, evidence_record_id, ordinal)
					 VALUES ($1, $2, $3, $4)
					 ON CONFLICT DO NOTHING`,
					fs.tenant.ID, sampleID, fs.evidence[evidenceIdx].ID, j,
				)
				if err != nil {
					return 0, nil, fmt.Errorf("demoseed: insert sample_evidence: %w", err)
				}
				evidenceIdx++
			}
		}
	}

	kinds := make([]string, 0, len(kindsSet))
	for k := range kindsSet {
		kinds = append(kinds, k)
	}
	return len(fs.evidence), kinds, nil
}

// writeWalkthroughs inserts the walkthrough rows. All demo
// walkthroughs are finalized.
func writeWalkthroughs(ctx context.Context, tx pgx.Tx, fs *fixtureSet) (int, error) {
	for _, w := range fs.walkthroughs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO walkthroughs
			 (id, tenant_id, control_id, narrative, transcript,
			  canonical_hash, status, created_by)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			w.ID, fs.tenant.ID, w.ControlID,
			w.Narrative, w.Transcript, w.HashBytes,
			w.Status, w.CreatedBy,
		); err != nil {
			return 0, fmt.Errorf("demoseed: insert walkthrough: %w", err)
		}
	}
	return len(fs.walkthroughs), nil
}

// writeExceptions inserts the exception rows + a matching
// exception_audit_log row per state transition.
func writeExceptions(ctx context.Context, tx pgx.Tx, fs *fixtureSet) (int, error) {
	for _, e := range fs.exceptions {
		_, err := tx.Exec(ctx,
			`INSERT INTO exceptions
			 (id, tenant_id, control_id, scope_cell_predicate, justification,
			  compensating_controls, requested_by, requested_at,
			  approved_by, approved_at, activated_by, activated_at,
			  effective_from, expires_at, status)
			 VALUES ($1, $2, $3, $4::jsonb, $5,
			         $6, $7, $8,
			         $9, $10, $11, $12,
			         $13, $14, $15)`,
			e.ID, fs.tenant.ID, e.ControlID,
			`{"op":"true"}`, e.Justification,
			e.CompensatingCtrls, e.RequestedBy, e.RequestedAt,
			e.ApprovedBy, e.ApprovedAt, e.ActivatedBy, e.ActivatedAt,
			e.EffectiveFrom, e.ExpiresAt, e.Status,
		)
		if err != nil {
			return 0, fmt.Errorf("demoseed: insert exception: %w", err)
		}
	}
	return len(fs.exceptions), nil
}

// writeBoardReports inserts board_briefs + board_packs.
// All packs ship as 'published' for the demo.
func writeBoardReports(ctx context.Context, tx pgx.Tx, fs *fixtureSet) (int, int, error) {
	bbCount := 0
	for _, b := range fs.boardBriefs {
		contentBytes, err := json.Marshal(b.Content)
		if err != nil {
			return 0, 0, fmt.Errorf("demoseed: marshal board_brief content: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO board_briefs
			 (id, tenant_id, period_end, content, narrative_md)
			 VALUES ($1, $2, $3, $4::jsonb, $5)`,
			b.ID, fs.tenant.ID, b.PeriodEnd, contentBytes, b.NarrativeMD,
		); err != nil {
			return 0, 0, fmt.Errorf("demoseed: insert board_brief: %w", err)
		}
		bbCount++
	}
	bpCount := 0
	for _, p := range fs.boardPacks {
		contentBytes, err := json.Marshal(p.Content)
		if err != nil {
			return 0, 0, fmt.Errorf("demoseed: marshal board_pack content: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO board_packs
			 (id, tenant_id, period_end, status, content, narrative_md,
			  published_by, published_at)
			 VALUES ($1, $2, $3, 'published', $4::jsonb, $5, $6, $7)`,
			p.ID, fs.tenant.ID, p.PeriodEnd, contentBytes, p.NarrativeMD,
			p.PublishedBy, p.PublishedAt,
		); err != nil {
			return 0, 0, fmt.Errorf("demoseed: insert board_pack: %w", err)
		}
		bpCount++
	}
	return bbCount, bpCount, nil
}

// writeFrameworkScopes inserts ~3 framework_scopes rows binding the
// demo tenant to each framework_version_id discovered. AC-7.
//
// D5: the seeder ships exactly 3 framework scopes (SOC 2, ISO 27001,
// NIST CSF). It does NOT enumerate every framework_version row in the
// catalog — the catalog may contain dozens (SCF crosswalks etc.) and
// demoing every one is noise. The framework_version_id for each
// scope is taken from a small named set; if the catalog doesn't have
// the named framework, the demo binds to the discovered fallback.
func writeFrameworkScopes(ctx context.Context, tx pgx.Tx, fs *fixtureSet) (int, error) {
	if len(fs.frameworkVersionIDs) == 0 {
		return 0, nil
	}
	// For the demo, we'll create N FrameworkScope rows all pointing
	// at the first discovered framework_version_id (the catalog has
	// only the demo framework in the test DB; in a real install with
	// SCF + cross-walks the IDs would differ). The names + statuses
	// vary so the surface looks differentiated.
	scopeNames := []string{
		"SOC 2 — Type II Audit Scope",
		"ISO 27001 — Annex A Scope",
		"NIST CSF 2.0 — Implementation Scope",
	}
	// Schema evolved since slice 002 (slice 018/019 reshaped
	// framework_scopes). The current shape: state is TEXT (draft |
	// proposed | activated | retired), predicate is JSONB, and a
	// predicate_hash is required NOT NULL. We use the same predicate
	// shape ({"op":"true"}) and hash it deterministically.
	predicate := `{"op":"true"}`
	predicateHash := hexString(sha256Of(predicate))

	// Only ONE framework_scope can be `activated` per framework_version
	// (partial UNIQUE index). For the demo we ship 3 scopes pointing
	// at distinct logical frameworks — since the test DB has only
	// the one demo framework_version, we ship the first as activated
	// and the rest as draft to keep the unique-index satisfied.
	scopeStates := []string{"activated", "draft", "draft"}
	count := 0
	for i := 0; i < frameworkScopeNum && i < len(scopeNames); i++ {
		id := uuid.New()
		fwVID := fs.frameworkVersionIDs[i%len(fs.frameworkVersionIDs)]
		if _, err := tx.Exec(ctx,
			`INSERT INTO framework_scopes
			 (id, tenant_id, framework_version_id, name, state, predicate, predicate_hash,
			  effective_from)
			 VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7,
			         $8)`,
			id, fs.tenant.ID, fwVID, scopeNames[i],
			scopeStates[i%len(scopeStates)],
			predicate, predicateHash,
			fs.now.AddDate(0, -6, 0),
		); err != nil {
			return 0, fmt.Errorf("demoseed: insert framework_scope: %w", err)
		}
		count++
	}
	return count, nil
}

// sha256Of is a small helper for the predicate-hash computation.
func sha256Of(s string) []byte {
	h := sha256.Sum256([]byte(s))
	return h[:]
}

// writeDemoAuditTrail writes ~50 tenant-scoped me_audit_log rows with
// realistic actions ('profile.update', 'preferences.update', etc.).
// Every row carries `payload_json -> demo_seed_v = "205"` for AC-9.
//
// Returns the row count written.
func writeDemoAuditTrail(ctx context.Context, tx pgx.Tx, fs *fixtureSet) (int, error) {
	// Pick from the action vocabulary the me_audit_log CHECK
	// constraint admits. Keeping it to the "user activity" subset so
	// the demo's audit trail reads like real operator activity.
	actions := []string{
		"profile.update",
		"preferences.update",
		"session.revoke",
	}
	count := 0
	for i := 0; i < fs.auditTrailCount; i++ {
		action := actions[i%len(actions)]
		before := map[string]any{"demo_seed_v": DemoSeedVersion}
		after := map[string]any{
			"demo_seed_v": DemoSeedVersion,
			"action_idx":  i,
		}
		beforeB, _ := json.Marshal(before)
		afterB, _ := json.Marshal(after)
		// Spread occurred_at across the last 90 days.
		occurredAt := fs.now.AddDate(0, 0, -(i % 90))
		if _, err := tx.Exec(ctx,
			`INSERT INTO me_audit_log
			 (tenant_id, occurred_at, user_id, action, before, after)
			 VALUES ($1, $2, $3, $4, $5::jsonb, $6::jsonb)`,
			fs.tenant.ID, occurredAt, fs.user.ID, action, beforeB, afterB,
		); err != nil {
			return 0, fmt.Errorf("demoseed: insert audit trail row: %w", err)
		}
		count++
	}
	return count, nil
}

// writeOrgUnits inserts the org-unit hierarchy. Parents are inserted
// before children (the fixture builder emits company → org → team order,
// which is already topologically sorted for the self-ref FK). Slice 678 /
// ATLAS-028. MUST run before writeRisks (risks.org_unit_id FKs here).
func writeOrgUnits(ctx context.Context, tx pgx.Tx, fs *fixtureSet) (int, error) {
	for _, u := range fs.orgUnits {
		authoritiesBytes, err := json.Marshal(u.AcceptanceAuthorities)
		if err != nil {
			return 0, fmt.Errorf("demoseed: marshal acceptance_authorities: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO org_units
			 (id, tenant_id, name, parent_id, level, acceptance_authorities)
			 VALUES ($1, $2, $3, $4, $5::risk_level, $6::jsonb)`,
			u.ID, fs.tenant.ID, u.Name, nullableUUIDPtr(u.ParentID), u.Level,
			authoritiesBytes,
		); err != nil {
			return 0, fmt.Errorf("demoseed: insert org_unit %s: %w", u.Name, err)
		}
	}
	return len(fs.orgUnits), nil
}

// writeRoleUsers inserts the role-holder users (+ their local_credentials,
// user_roles, and api_keys rows) so the policy-ack roster has a real
// denominator (slice 678 / ATLAS-037, AC-3). The roster query counts
// distinct api_keys.issued_by whose owner_roles intersect a policy's
// acknowledgment_required_roles, so each user carries an api_keys row with
// OwnerRoles stamped. Users are demo_only=TRUE (the slice-205 forensic
// mark + slice-142 super_admin-promotion guard). A subset also write
// policy_acknowledgments so the roster numerator is non-zero.
//
// passwordHash is reused from the admin user — these demo users never log
// in interactively (the operator logs in as admin@demo.example); the
// credential row exists so the user is a complete, RLS-consistent record.
func writeRoleUsers(ctx context.Context, tx pgx.Tx, fs *fixtureSet, passwordHash string) (int, error) {
	for _, u := range fs.roleUsers {
		if _, err := tx.Exec(ctx,
			`INSERT INTO users (id, tenant_id, email, display_name, status, demo_only)
			 VALUES ($1, $2, $3, $4, 'active', TRUE)`,
			u.ID, fs.tenant.ID, u.Email, u.Name,
		); err != nil {
			return 0, fmt.Errorf("demoseed: insert role user %s: %w", u.Email, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO local_credentials (user_id, tenant_id, password_hash, algo, params)
			 VALUES ($1, $2, $3, 'argon2id', '{}'::jsonb)`,
			u.ID, fs.tenant.ID, passwordHash,
		); err != nil {
			return 0, fmt.Errorf("demoseed: insert role user creds %s: %w", u.Email, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO user_roles (tenant_id, user_id, role, granted_by)
			 VALUES ($1, $2, 'viewer', 'system:demoseed')`,
			fs.tenant.ID, u.ID.String(),
		); err != nil {
			return 0, fmt.Errorf("demoseed: insert role user_roles %s: %w", u.Email, err)
		}
		// api_keys row carrying owner_roles — the roster's role source.
		// token_hash is a deterministic-but-unique 32-byte digest derived
		// from the user id (the key is never used to authenticate; it
		// exists so the user appears in the roster denominator).
		tokenHash := sha256.Sum256([]byte("demo-api-key:" + u.ID.String()))
		last4 := u.ID.String()[len(u.ID.String())-4:]
		if _, err := tx.Exec(ctx,
			`INSERT INTO api_keys
			 (id, tenant_id, token_hash, issued_by, owner_roles, last4)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			uuid.New(), fs.tenant.ID, tokenHash[:], u.ID, u.OwnerRoles, last4,
		); err != nil {
			return 0, fmt.Errorf("demoseed: insert role user api_key %s: %w", u.Email, err)
		}
	}

	// Acks: a subset of role users acknowledge every published policy so
	// the roster numerator is non-zero (a partial roster, not 0% or 100%).
	if err := writePolicyAcks(ctx, tx, fs); err != nil {
		return 0, err
	}
	return len(fs.roleUsers), nil
}

// writePolicyAcks writes policy_acknowledgments for the role users flagged
// Acks=true, one per published policy. ack_token is the deterministic
// (user, policy_version, day-bucket) key the real handler derives; we
// reproduce a stable shape so a re-seed is idempotent at the unique index.
func writePolicyAcks(ctx context.Context, tx pgx.Tx, fs *fixtureSet) error {
	for _, u := range fs.roleUsers {
		if !u.Acks {
			continue
		}
		for _, p := range fs.policies {
			ackToken := fmt.Sprintf("demo-ack:%s:%s", u.ID.String(), p.ID.String())
			ackedAt := fs.now.AddDate(0, 0, -7) // acknowledged a week ago (fresh)
			if _, err := tx.Exec(ctx,
				`INSERT INTO policy_acknowledgments
				 (tenant_id, policy_id, policy_version_id, user_id, acknowledged_at, ack_token)
				 VALUES ($1, $2, $3, $4, $5, $6)`,
				fs.tenant.ID, p.ID, p.ID, u.ID, ackedAt, ackToken,
			); err != nil {
				return fmt.Errorf("demoseed: insert policy_ack %s/%s: %w", u.Email, p.Title, err)
			}
		}
	}
	return nil
}

// writeDecisions inserts the Decision Log entries + their decision_risks
// links so the /risks/hierarchy decision timeline is non-empty (slice 678
// / ATLAS-028, AC-1). MUST run after writeRisks (decision_risks FKs into
// risks). The decisions.tenant_write RLS WITH CHECK gates on
// app.current_role being set/non-empty — the seeder runs BYPASSRLS so the
// policy is not enforced, but the row carries the correct tenant_id.
func writeDecisions(ctx context.Context, tx pgx.Tx, fs *fixtureSet) (int, error) {
	for _, d := range fs.decisions {
		var revisitBy any
		if d.RevisitBy != nil {
			revisitBy = *d.RevisitBy
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO decisions
			 (id, tenant_id, decision_id, title, narrative, constraints,
			  tradeoffs, decision_maker, decided_at, revisit_by, status)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11::decision_status)`,
			d.ID, fs.tenant.ID, d.DecisionID, d.Title, d.Narrative, d.Constraints,
			d.Tradeoffs, d.DecisionMaker, d.DecidedAt, revisitBy, d.Status,
		); err != nil {
			return 0, fmt.Errorf("demoseed: insert decision %s: %w", d.DecisionID, err)
		}
		if d.LinkedRiskID != nil {
			if _, err := tx.Exec(ctx,
				`INSERT INTO decision_risks (decision_id, target_id, tenant_id)
				 VALUES ($1, $2, $3)`,
				d.ID, *d.LinkedRiskID, fs.tenant.ID,
			); err != nil {
				return 0, fmt.Errorf("demoseed: insert decision_risk link %s: %w", d.DecisionID, err)
			}
		}
	}
	return len(fs.decisions), nil
}

// writeQuestionnaire inserts the single questionnaire response instance +
// its questions + answers so /questionnaires is demonstrable (slice 678 /
// ATLAS-037, AC-2). Returns the question count written.
func writeQuestionnaire(ctx context.Context, tx pgx.Tx, fs *fixtureSet) (int, error) {
	q := fs.questionnaire
	if _, err := tx.Exec(ctx,
		`INSERT INTO questionnaires (id, tenant_id, name, source_label, status, notes)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		q.ID, fs.tenant.ID, q.Name, q.SourceLabel, q.Status, q.Notes,
	); err != nil {
		return 0, fmt.Errorf("demoseed: insert questionnaire: %w", err)
	}
	for _, qq := range q.Questions {
		if _, err := tx.Exec(ctx,
			`INSERT INTO questionnaire_questions
			 (id, tenant_id, questionnaire_id, code, text, domain, answer_type, scf_anchor_id, sort_order)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			qq.ID, fs.tenant.ID, q.ID, qq.Code, qq.Text, qq.Domain,
			qq.AnswerType, qq.SCFAnchorID, qq.SortOrder,
		); err != nil {
			return 0, fmt.Errorf("demoseed: insert questionnaire_question %s: %w", qq.Code, err)
		}
		// Only answered questions get an answer row (one question is left
		// unanswered + needs-mapping on purpose — the realistic state).
		if qq.AnswerValue == "" && qq.Narrative == "" {
			continue
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO questionnaire_answers
			 (id, tenant_id, question_id, answer_value, narrative, authored_by)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			uuid.New(), fs.tenant.ID, qq.ID, qq.AnswerValue, qq.Narrative, qq.AuthoredBy,
		); err != nil {
			return 0, fmt.Errorf("demoseed: insert questionnaire_answer %s: %w", qq.Code, err)
		}
	}
	return len(q.Questions), nil
}

// nullableUUIDPtr returns nil if p is nil or points at uuid.Nil, otherwise
// the dereferenced uuid. Used for nullable self-ref FK columns (org_units
// parent_id) where the fixture carries a *uuid.UUID.
func nullableUUIDPtr(p *uuid.UUID) any {
	if p == nil || *p == uuid.Nil {
		return nil
	}
	return *p
}

// ----- helper helpers -----

// currentTenantOf reads the tenant id stored in ctx by Apply. The
// seeder bundles fs.tenant.ID into the ctx through a context-value
// pattern so the writers can pull it without threading it through
// every signature.
type ctxKey int

const ctxKeyTenantID ctxKey = 1

func withTenant(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, ctxKeyTenantID, id)
}

func currentTenantOf(ctx context.Context) uuid.UUID {
	v, _ := ctx.Value(ctxKeyTenantID).(uuid.UUID)
	return v
}

// nullableUUID returns nil if id is uuid.Nil, otherwise a pointer.
// Used at INSERT time for nullable UUID columns.
func nullableUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

// nonZeroOrSelf returns id if non-zero, otherwise a fresh UUID. Used
// for audit-log columns that have a `<> zero` CHECK constraint when
// the CLI was invoked without an actor context.
func nonZeroOrSelf(id uuid.UUID) uuid.UUID {
	if id == uuid.Nil {
		return uuid.New()
	}
	return id
}

// nonZeroOrTenant returns the actor tenant if non-zero, otherwise the
// demo tenant. The super_admin_audit_log.actor_tenant_id column is
// NOT NULL, so we must supply something even when the CLI was
// invoked without a session-tenant context.
func nonZeroOrTenant(actorTenant uuid.UUID, demoTenant uuid.UUID) uuid.UUID {
	if actorTenant == uuid.Nil {
		return demoTenant
	}
	return actorTenant
}

// periodStatus maps the fixture bool to the audit_periods.status text.
func periodStatus(frozen bool) string {
	if frozen {
		return "frozen"
	}
	return "open"
}

// frozenHashOrNil returns the period's frozen-content hash as a 32-byte
// byte slice (the schema requires octet_length=32) or nil if the
// period is open. The hash is computed over the period's metadata —
// not the live evidence universe (the demo doesn't materialize a true
// freeze hash because the slice-205 dataset is itself synthetic).
func frozenHashOrNil(ap *auditPeriodFixture) []byte {
	if !ap.Frozen {
		return nil
	}
	h := sha256.Sum256([]byte(ap.ID.String() + ap.Name))
	return h[:]
}

// frozenByOrNil returns the period's frozen_by string or nil for open
// periods. NOT NULL when frozen; NULL when open (CHECK enforces).
func frozenByOrNil(ap *auditPeriodFixture) any {
	if !ap.Frozen {
		return nil
	}
	return ap.FrozenBy
}
