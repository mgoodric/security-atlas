// Package accessreview implements operational access-recertification campaigns.
//
// A campaign snapshots entitlement review items from SCIM or a manual CSV,
// assigns reviewers, records keep/revoke attestations with reasons, exports
// revoke decisions, emits completion evidence mapped to SOC 2 CC6, and creates
// due reminders. It never revokes access directly.
package accessreview

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/tenancy"
)

const (
	SourceSCIM      = "scim"
	SourceManualCSV = "manual_csv"

	StatusDraft     = "draft"
	StatusActive    = "active"
	StatusCompleted = "completed"
	StatusCancelled = "cancelled"

	ItemStatusPending  = "pending"
	ItemStatusAttested = "attested"

	DecisionKeep   = "keep"
	DecisionRevoke = "revoke"

	ReminderNotificationType = "access_review_due"
	// EvidenceKind must stay aligned with the registered platform schema
	// (internal/api/schemaregistry/schemas/access_review.completion/1.0.0.json)
	// and with the CC6.3 bundle's evidence query, both keyed on this kind —
	// an unregistered kind would never satisfy the control it certifies.
	EvidenceKind          = "access_review.completion.v1"
	EvidenceSchemaVersion = "1.0.0"
	CC6ControlRef         = "soc2_cc6_3_periodic_access_review"
	// evidenceReviewerRole is the reviewer_role reported in completion
	// evidence. Campaign reviewers are assigned per campaign, not drawn from
	// a role table, so the role is the campaign-reviewer designation itself.
	evidenceReviewerRole = "campaign_reviewer"
)

var (
	ErrNameRequired      = errors.New("access_review: name is required")
	ErrDueRequired       = errors.New("access_review: due_at is required")
	ErrCreatedByRequired = errors.New("access_review: created_by is required")
	ErrReviewerRequired  = errors.New("access_review: at least one reviewer is required")
	ErrItemsRequired     = errors.New("access_review: at least one entitlement item is required")
	ErrNotFound          = errors.New("access_review: not found")
	ErrReasonRequired    = errors.New("access_review: reason is required")
	ErrInvalidDecision   = errors.New("access_review: decision must be keep or revoke")
	ErrIncomplete        = errors.New("access_review: campaign has pending review items")
)

type Store struct {
	pool  *pgxpool.Pool
	clock func() time.Time
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, clock: func() time.Time { return time.Now().UTC() }}
}

func (s *Store) WithClock(fn func() time.Time) *Store {
	s.clock = fn
	return s
}

type Scope struct {
	Systems      []string
	Entitlements []string
	UserIDs      []string
}

type CreateInput struct {
	Name      string
	Source    string
	Scope     Scope
	DueAt     time.Time
	Reviewers []string
	CreatedBy string
	ManualCSV io.Reader
}

type Campaign struct {
	ID               uuid.UUID
	TenantID         uuid.UUID
	Name             string
	Source           string
	Scope            Scope
	Status           string
	DueAt            time.Time
	CreatedBy        string
	CompletedAt      *time.Time
	EvidenceRecordID *uuid.UUID
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type Item struct {
	ID              uuid.UUID
	CampaignID      uuid.UUID
	System          string
	Entitlement     string
	PrincipalUserID string
	PrincipalEmail  string
	ReviewerID      string
	Status          string
	Decision        *string
	Reason          string
	AttestedBy      *string
	AttestedAt      *time.Time
	Source          string
	SourceRef       string
}

type Entitlement struct {
	System          string
	Entitlement     string
	PrincipalUserID string
	PrincipalEmail  string
	Source          string
	SourceRef       string
}

type AttestInput struct {
	ItemID     uuid.UUID
	ReviewerID string
	Decision   string
	Reason     string
}

type Rollup struct {
	CampaignID       uuid.UUID  `json:"campaign_id"`
	TotalItems       int        `json:"total_items"`
	PendingItems     int        `json:"pending_items"`
	KeepDecisions    int        `json:"keep_decisions"`
	RevokeDecisions  int        `json:"revoke_decisions"`
	ReviewerCount    int        `json:"reviewer_count"`
	EvidenceRecordID *uuid.UUID `json:"evidence_record_id,omitempty"`
}

// CompletionEvidencePayload shapes a completed campaign's rollup into the
// registered access_review.completion/1.0.0 schema (additionalProperties is
// false there, so only its declared fields may appear). users_terminated
// counts revoke DECISIONS by distinct principal — enforcement remains the
// operator's action via the revoke-list export, never this module's.
func CompletionEvidencePayload(r Rollup, campaignName, completedBy string, usersReviewed, usersTerminated int) map[string]any {
	return map[string]any{
		"review_id":          r.CampaignID.String(),
		"completed_by":       completedBy,
		"reviewer_role":      evidenceReviewerRole,
		"users_reviewed":     usersReviewed,
		"users_terminated":   usersTerminated,
		"users_role_changed": 0,
		"notes": fmt.Sprintf(
			"campaign %q: %d items reviewed by %d reviewer(s); %d keep, %d revoke decisions. Revoke decisions are recorded for operator enforcement, not auto-applied.",
			campaignName, r.TotalItems, r.ReviewerCount, r.KeepDecisions, r.RevokeDecisions),
	}
}

type RevokeDecision struct {
	ItemID          uuid.UUID `json:"item_id"`
	System          string    `json:"system"`
	Entitlement     string    `json:"entitlement"`
	PrincipalUserID string    `json:"principal_user_id"`
	PrincipalEmail  string    `json:"principal_email"`
	ReviewerID      string    `json:"reviewer_id"`
	Reason          string    `json:"reason"`
	AttestedAt      time.Time `json:"attested_at"`
}

func (s *Store) CreateCampaign(ctx context.Context, in CreateInput) (Campaign, []Item, error) {
	if strings.TrimSpace(in.Name) == "" {
		return Campaign{}, nil, ErrNameRequired
	}
	if in.DueAt.IsZero() {
		return Campaign{}, nil, ErrDueRequired
	}
	reviewers := cleanUnique(in.Reviewers)
	if len(reviewers) == 0 {
		return Campaign{}, nil, ErrReviewerRequired
	}
	source := in.Source
	if source == "" {
		source = SourceSCIM
	}
	if source != SourceSCIM && source != SourceManualCSV {
		return Campaign{}, nil, fmt.Errorf("access_review: unsupported source %q", source)
	}
	if strings.TrimSpace(in.CreatedBy) == "" {
		return Campaign{}, nil, ErrCreatedByRequired
	}

	var campaign Campaign
	var items []Item
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		entitlements, err := s.sourceEntitlements(ctx, tx, tenantID, source, in)
		if err != nil {
			return err
		}
		if len(entitlements) == 0 {
			return ErrItemsRequired
		}

		campaignID := uuid.New()
		row := tx.QueryRow(ctx, `
			INSERT INTO access_review_campaigns (
				id, tenant_id, name, source, scope_systems, scope_entitlements,
				scope_user_ids, status, due_at, created_by
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 'active', $8, $9)
			RETURNING id, tenant_id, name, source, scope_systems, scope_entitlements,
				scope_user_ids, status, due_at, created_by, completed_at,
				evidence_record_id, created_at, updated_at
		`, campaignID, tenantID, strings.TrimSpace(in.Name), source, cleanUnique(in.Scope.Systems),
			cleanUnique(in.Scope.Entitlements), cleanUnique(in.Scope.UserIDs), in.DueAt.UTC(), in.CreatedBy)
		if err := scanCampaign(row, &campaign); err != nil {
			return fmt.Errorf("access_review: create campaign: %w", err)
		}

		for _, reviewer := range reviewers {
			if _, err := tx.Exec(ctx, `
				INSERT INTO access_review_reviewer_assignments (id, tenant_id, campaign_id, reviewer_id)
				VALUES ($1, $2, $3, $4)
			`, uuid.New(), tenantID, campaignID, reviewer); err != nil {
				return fmt.Errorf("access_review: assign reviewer: %w", err)
			}
		}
		items = make([]Item, 0, len(entitlements))
		for i, e := range entitlements {
			reviewer := reviewers[i%len(reviewers)]
			var item Item
			row := tx.QueryRow(ctx, `
				INSERT INTO access_review_items (
					id, tenant_id, campaign_id, system, entitlement, principal_user_id,
					principal_email, reviewer_id, source, source_ref
				)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
				RETURNING id, campaign_id, system, entitlement, principal_user_id,
					principal_email, reviewer_id, status, decision, reason,
					attested_by, attested_at, source, source_ref
			`, uuid.New(), tenantID, campaignID, e.System, e.Entitlement, e.PrincipalUserID,
				e.PrincipalEmail, reviewer, e.Source, e.SourceRef)
			if err := scanItem(row, &item); err != nil {
				return fmt.Errorf("access_review: create item: %w", err)
			}
			items = append(items, item)
		}
		return nil
	})
	return campaign, items, err
}

func (s *Store) Attest(ctx context.Context, in AttestInput) (Item, error) {
	if in.Decision != DecisionKeep && in.Decision != DecisionRevoke {
		return Item{}, ErrInvalidDecision
	}
	if strings.TrimSpace(in.Reason) == "" {
		return Item{}, ErrReasonRequired
	}
	var item Item
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		row := tx.QueryRow(ctx, `
			UPDATE access_review_items
			SET status = 'attested',
				decision = $4,
				reason = $5,
				attested_by = $3,
				attested_at = $6,
				updated_at = $6
			WHERE tenant_id = $1 AND id = $2 AND reviewer_id = $3
			RETURNING id, campaign_id, system, entitlement, principal_user_id,
				principal_email, reviewer_id, status, decision, reason,
				attested_by, attested_at, source, source_ref
		`, tenantID, in.ItemID, in.ReviewerID, in.Decision, strings.TrimSpace(in.Reason), s.clock())
		if err := scanItem(row, &item); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("access_review: attest: %w", err)
		}
		return nil
	})
	return item, err
}

func (s *Store) Rollup(ctx context.Context, campaignID uuid.UUID) (Rollup, error) {
	var out Rollup
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		return scanRollup(tx.QueryRow(ctx, rollupSQL, tenantID, campaignID), &out)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Rollup{}, ErrNotFound
	}
	return out, err
}

func (s *Store) RevokeList(ctx context.Context, campaignID uuid.UUID) ([]RevokeDecision, error) {
	var out []RevokeDecision
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		rows, err := tx.Query(ctx, `
			SELECT id, system, entitlement, principal_user_id, principal_email,
				reviewer_id, reason, attested_at
			FROM access_review_items
			WHERE tenant_id = $1 AND campaign_id = $2 AND decision = 'revoke'
			ORDER BY system, entitlement, principal_email, principal_user_id
		`, tenantID, campaignID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r RevokeDecision
			if err := rows.Scan(&r.ItemID, &r.System, &r.Entitlement, &r.PrincipalUserID,
				&r.PrincipalEmail, &r.ReviewerID, &r.Reason, &r.AttestedAt); err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, err
}

// WriteRevokeCSV writes revoke decisions as CSV — the actionable handoff for
// the operator (or a future enforcement connector). It never revokes access.
func WriteRevokeCSV(w io.Writer, decisions []RevokeDecision) error {
	cw := csv.NewWriter(w)
	header := []string{"system", "entitlement", "principal_user_id", "principal_email", "reviewer_id", "reason", "attested_at"}
	if err := cw.Write(header); err != nil {
		return fmt.Errorf("access_review: write csv header: %w", err)
	}
	for _, d := range decisions {
		row := []string{d.System, d.Entitlement, d.PrincipalUserID, d.PrincipalEmail, d.ReviewerID, d.Reason, d.AttestedAt.UTC().Format(time.RFC3339)}
		if err := cw.Write(row); err != nil {
			return fmt.Errorf("access_review: write csv row: %w", err)
		}
	}
	cw.Flush()
	return cw.Error()
}

func (s *Store) Complete(ctx context.Context, campaignID uuid.UUID) (Rollup, error) {
	var out Rollup
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		if err := scanRollup(tx.QueryRow(ctx, rollupSQL, tenantID, campaignID), &out); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if out.PendingItems > 0 {
			return ErrIncomplete
		}
		if out.EvidenceRecordID != nil {
			return nil
		}
		var campaignName, createdBy string
		var usersReviewed, usersTerminated int
		if err := tx.QueryRow(ctx, `
			SELECT c.name, c.created_by,
				COALESCE(COUNT(DISTINCT i.principal_user_id), 0)::int,
				COALESCE(COUNT(DISTINCT i.principal_user_id) FILTER (WHERE i.decision = 'revoke'), 0)::int
			FROM access_review_campaigns c
			LEFT JOIN access_review_items i
				ON i.tenant_id = c.tenant_id AND i.campaign_id = c.id
			WHERE c.tenant_id = $1 AND c.id = $2
			GROUP BY c.name, c.created_by
		`, tenantID, campaignID).Scan(&campaignName, &createdBy, &usersReviewed, &usersTerminated); err != nil {
			return fmt.Errorf("access_review: completion stats: %w", err)
		}
		payload, err := json.Marshal(CompletionEvidencePayload(out, campaignName, createdBy, usersReviewed, usersTerminated))
		if err != nil {
			return err
		}
		// Best-effort control resolution: if the tenant imported the SOC 2
		// kit, attach the CC6.3 control's UUID so the eval engine (which
		// matches control_id, or control_ref as a UUID string) picks the
		// record up. Without the kit the record still lands with the bundle
		// slug in control_ref for traceability.
		var controlID *uuid.UUID
		var resolved uuid.UUID
		switch err := tx.QueryRow(ctx, `
			SELECT id FROM controls
			WHERE tenant_id = $1 AND bundle_id = $2 AND superseded_by IS NULL
		`, tenantID, CC6ControlRef).Scan(&resolved); {
		case err == nil:
			controlID = &resolved
		case errors.Is(err, pgx.ErrNoRows):
			// kit not imported; leave control_id NULL
		default:
			return fmt.Errorf("access_review: resolve control: %w", err)
		}
		evidenceID := uuid.New()
		hash := sha256.Sum256(payload)
		now := s.clock()
		_, err = tx.Exec(ctx, `
			INSERT INTO evidence_records (
				id, tenant_id, control_id, control_ref, observed_at, provenance,
				result, payload, hash, freshness_class, valid_until,
				idempotency_key, evidence_kind, schema_version, credential_id,
				ingestion_path, source_attribution, scope_canonical, observed_at_nanos
			)
			VALUES (
				$1, $2, $11, $3, $4,
				'{"source":"access_review_campaign"}'::jsonb,
				'pass', $5, $6, 'quarterly', $7,
				$8, $9, $12, 'access_review_campaign',
				'manual_upload', '{"producer":"access_review"}'::jsonb, '{}'::jsonb, $10
			)
		`, evidenceID, tenantID, CC6ControlRef, now, payload, hex.EncodeToString(hash[:]),
			now.Add(400*24*time.Hour), "access-review:"+campaignID.String()+":completion",
			EvidenceKind, now.UnixNano(), controlID, EvidenceSchemaVersion)
		if err != nil {
			return fmt.Errorf("access_review: insert evidence: %w", err)
		}
		_, err = tx.Exec(ctx, `
			UPDATE access_review_campaigns
			SET status = 'completed', completed_at = $3, evidence_record_id = $4, updated_at = $3
			WHERE tenant_id = $1 AND id = $2
		`, tenantID, campaignID, now, evidenceID)
		if err != nil {
			return fmt.Errorf("access_review: mark complete: %w", err)
		}
		out.EvidenceRecordID = &evidenceID
		return nil
	})
	return out, err
}

func (s *Store) FireDueReminders(ctx context.Context, now time.Time) (int, error) {
	var created int
	ymd := now.UTC().Format("2006-01-02")
	err := s.inTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		tag, err := tx.Exec(ctx, `
			INSERT INTO notifications (id, tenant_id, recipient_user_id, type, payload, created_at)
			SELECT gen_random_uuid(), c.tenant_id, a.reviewer_id, $3,
				jsonb_build_object(
					'campaign_id', c.id::text,
					'campaign_name', c.name,
					'due_at', c.due_at,
					'reminder_date', $4::text
				),
				$5
			FROM access_review_campaigns c
			JOIN access_review_reviewer_assignments a
			  ON a.tenant_id = c.tenant_id AND a.campaign_id = c.id
			WHERE c.tenant_id = $1
			  AND c.status = 'active'
			  AND c.due_at <= $2
			  AND EXISTS (
			    SELECT 1 FROM access_review_items i
			    WHERE i.tenant_id = c.tenant_id AND i.campaign_id = c.id AND i.status = 'pending'
			  )
			  AND NOT EXISTS (
			    SELECT 1 FROM notifications n
			    WHERE n.tenant_id = c.tenant_id
			      AND n.recipient_user_id = a.reviewer_id
			      AND n.type = $3
			      AND n.payload->>'campaign_id' = c.id::text
			      AND n.payload->>'reminder_date' = $4
			  )
		`, tenantID, now.UTC(), ReminderNotificationType, ymd, now.UTC())
		if err != nil {
			return err
		}
		created = int(tag.RowsAffected())
		return nil
	})
	return created, err
}

func (s *Store) sourceEntitlements(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, source string, in CreateInput) ([]Entitlement, error) {
	if source == SourceManualCSV {
		return parseManualCSV(in.ManualCSV, in.Scope)
	}
	return s.sourceSCIM(ctx, tx, tenantID, in.Scope)
}

func (s *Store) sourceSCIM(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, scope Scope) ([]Entitlement, error) {
	if len(scope.Systems) > 0 && !contains(scope.Systems, "security-atlas") {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT
			u.id::text AS user_id,
			u.email,
			COALESCE(g.display_name, m.group_ref, 'atlas_user') AS entitlement,
			COALESCE(g.scim_external_id, g.display_name, m.group_ref, 'atlas_user') AS source_ref
		FROM users u
		LEFT JOIN scim_group_members m
		  ON m.tenant_id = u.tenant_id AND m.user_id = u.id::text
		LEFT JOIN scim_groups g
		  ON g.tenant_id = m.tenant_id AND g.id = m.group_id AND g.active = TRUE
		WHERE u.tenant_id = $1
		  AND u.active = TRUE
		ORDER BY u.email, entitlement
	`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entitlement
	for rows.Next() {
		e := Entitlement{System: "security-atlas", Source: SourceSCIM}
		if err := rows.Scan(&e.PrincipalUserID, &e.PrincipalEmail, &e.Entitlement, &e.SourceRef); err != nil {
			return nil, err
		}
		if scopeAllows(scope, e) {
			out = append(out, e)
		}
	}
	return out, rows.Err()
}

func parseManualCSV(r io.Reader, scope Scope) ([]Entitlement, error) {
	if r == nil {
		return nil, ErrItemsRequired
	}
	cr := csv.NewReader(r)
	cr.TrimLeadingSpace = true
	header, err := cr.Read()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, ErrItemsRequired
		}
		return nil, fmt.Errorf("access_review: read csv header: %w", err)
	}
	cols := map[string]int{}
	for i, h := range header {
		cols[strings.ToLower(strings.TrimSpace(h))] = i
	}
	required := []string{"system", "entitlement", "user_id"}
	for _, col := range required {
		if _, ok := cols[col]; !ok {
			return nil, fmt.Errorf("access_review: csv missing %s column", col)
		}
	}
	var out []Entitlement
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("access_review: read csv: %w", err)
		}
		e := Entitlement{
			System:          csvVal(rec, cols, "system"),
			Entitlement:     csvVal(rec, cols, "entitlement"),
			PrincipalUserID: csvVal(rec, cols, "user_id"),
			PrincipalEmail:  csvVal(rec, cols, "email"),
			Source:          SourceManualCSV,
			SourceRef:       csvVal(rec, cols, "source_ref"),
		}
		if e.SourceRef == "" {
			e.SourceRef = e.System + ":" + e.Entitlement
		}
		if e.System == "" || e.Entitlement == "" || e.PrincipalUserID == "" {
			continue
		}
		if scopeAllows(scope, e) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (s *Store) inTx(ctx context.Context, fn func(context.Context, pgx.Tx, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	if err := fn(ctx, tx, tenantID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

const rollupSQL = `
SELECT c.id,
       COALESCE(i.total_items, 0)::int AS total_items,
       COALESCE(i.pending_items, 0)::int AS pending_items,
       COALESCE(i.keep_decisions, 0)::int AS keep_decisions,
       COALESCE(i.revoke_decisions, 0)::int AS revoke_decisions,
       COALESCE(a.reviewer_count, 0)::int AS reviewer_count,
       c.evidence_record_id
FROM access_review_campaigns c
LEFT JOIN LATERAL (
	SELECT COUNT(*) AS total_items,
	       COUNT(*) FILTER (WHERE status = 'pending') AS pending_items,
	       COUNT(*) FILTER (WHERE decision = 'keep') AS keep_decisions,
	       COUNT(*) FILTER (WHERE decision = 'revoke') AS revoke_decisions
	FROM access_review_items i
	WHERE i.tenant_id = c.tenant_id AND i.campaign_id = c.id
) i ON TRUE
LEFT JOIN LATERAL (
	SELECT COUNT(DISTINCT reviewer_id) AS reviewer_count
	FROM access_review_reviewer_assignments a
	WHERE a.tenant_id = c.tenant_id AND a.campaign_id = c.id
) a ON TRUE
WHERE c.tenant_id = $1 AND c.id = $2
`

func scanRollup(row pgx.Row, out *Rollup) error {
	return row.Scan(&out.CampaignID, &out.TotalItems, &out.PendingItems, &out.KeepDecisions,
		&out.RevokeDecisions, &out.ReviewerCount, &out.EvidenceRecordID)
}

func scanCampaign(row pgx.Row, out *Campaign) error {
	return row.Scan(&out.ID, &out.TenantID, &out.Name, &out.Source, &out.Scope.Systems,
		&out.Scope.Entitlements, &out.Scope.UserIDs, &out.Status, &out.DueAt,
		&out.CreatedBy, &out.CompletedAt, &out.EvidenceRecordID, &out.CreatedAt, &out.UpdatedAt)
}

func scanItem(row pgx.Row, out *Item) error {
	return row.Scan(&out.ID, &out.CampaignID, &out.System, &out.Entitlement,
		&out.PrincipalUserID, &out.PrincipalEmail, &out.ReviewerID, &out.Status,
		&out.Decision, &out.Reason, &out.AttestedBy, &out.AttestedAt, &out.Source, &out.SourceRef)
}

func csvVal(rec []string, cols map[string]int, key string) string {
	i, ok := cols[key]
	if !ok || i >= len(rec) {
		return ""
	}
	return strings.TrimSpace(rec[i])
}

func cleanUnique(in []string) []string {
	// Non-nil so pgx encodes an empty scope as an empty TEXT[] rather than
	// NULL (the scope columns are NOT NULL).
	out := []string{}
	seen := map[string]struct{}{}
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func contains(values []string, target string) bool {
	return slices.Contains(cleanUnique(values), target)
}

func scopeAllows(scope Scope, e Entitlement) bool {
	if len(scope.Systems) > 0 && !contains(scope.Systems, e.System) {
		return false
	}
	if len(scope.Entitlements) > 0 && !contains(scope.Entitlements, e.Entitlement) {
		return false
	}
	if len(scope.UserIDs) > 0 && !contains(scope.UserIDs, e.PrincipalUserID) {
		return false
	}
	return true
}
