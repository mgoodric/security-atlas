package riskimport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mgoodric/security-atlas/internal/risk"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

var (
	ErrBatchNotFound = errors.New("riskimport: import batch not found")
	ErrRowNotFound   = errors.New("riskimport: staged row not found")
	// ErrRowDecided is a decision on a row that is no longer pending.
	// Decisions are final; nothing is written.
	ErrRowDecided = errors.New("riskimport: staged row is already decided")
	// ErrApproverRequired is a decision with no deciding person.
	ErrApproverRequired = errors.New("riskimport: a decision requires the deciding person")
	ErrReasonRequired   = errors.New("riskimport: rejection requires a reason")
	ErrInvalidDecision  = errors.New("riskimport: invalid decision")
)

// Batch and row statuses.
const (
	BatchStatusStaged    = "staged"
	BatchStatusCompleted = "completed"

	RowStatusPending   = "pending"
	RowStatusApproved  = "approved"
	RowStatusRejected  = "rejected"
	RowStatusDuplicate = "duplicate"
)

// Review flags on a staged row: why an approver should look at it before
// deciding. The vocabulary is fixed (risk_import_rows_flags_chk); #2843,
// #2842 and #2846 set them.
const (
	FlagOwnerUnlinked       = "owner_unlinked"
	FlagTreatmentDowngraded = "treatment_downgraded"
	FlagParentLinkFailed    = "parent_link_failed"
)

// maxRejectReason caps a rejection reason, in characters.
const maxRejectReason = 2000

// BatchRow is a staged row as persisted in an import batch, with its
// decision once one is made.
type BatchRow struct {
	ID          string `json:"id"`
	RowNumber   int    `json:"row_number"`
	ExternalRef string `json:"external_ref,omitempty"`
	Title       string `json:"title"`
	Description string `json:"description"`
	// Category is the slug of the tenant category the row's value matched.
	Category string `json:"category"`
	// ReviewDue is a YYYY-MM-DD date, empty when unset.
	ReviewDue string `json:"review_due,omitempty"`
	// InherentScore and ResidualScore are in the batch methodology's
	// shape; absent is unscored, and an absent residual takes the create
	// path's default.
	InherentScore json.RawMessage   `json:"inherent_score,omitempty"`
	ResidualScore json.RawMessage   `json:"residual_score,omitempty"`
	Unmapped      map[string]string `json:"unmapped"`
	// Flags are the row's review flags (Flag*), empty when none.
	Flags     []string `json:"flags"`
	Status    string   `json:"status"`
	DecidedBy string   `json:"decided_by,omitempty"`
	// RiskID is the risk approval created, or for a duplicate the existing
	// risk carrying the row's organisation risk ID.
	RiskID          string `json:"risk_id,omitempty"`
	RejectionReason string `json:"rejection_reason,omitempty"`
}

// Batch is one upload. Every non-blank data row of the file is in exactly
// one of Staged or Rejected, so TotalRows == StagedCount + RejectedCount.
type Batch struct {
	ID             string `json:"id"`
	SourceFilename string `json:"source_filename"`
	SourceFormat   string `json:"source_format"`
	// Methodology is the one scoring methodology every risk of the batch
	// is created with (#2841).
	Methodology     string        `json:"methodology"`
	UploadedBy      string        `json:"uploaded_by"`
	Status          string        `json:"status"`
	TotalRows       int           `json:"total_rows"`
	StagedCount     int           `json:"staged_count"`
	RejectedCount   int           `json:"rejected_count"`
	UnmappedColumns []string      `json:"unmapped_columns"`
	CreatedAt       time.Time     `json:"created_at"`
	Staged          []BatchRow    `json:"staged"`
	Rejected        []RejectedRow `json:"rejected"`
}

// StageInput is one upload to stage.
type StageInput struct {
	Filename  string
	Raw       []byte
	ColumnMap map[string]string
	// Methodology is the batch's; empty is the platform default.
	Methodology string
	UploadedBy  string
}

// Stage parses the upload, matches each row's category to one of the
// tenant's, and persists the batch. A row whose category matches none
// becomes a rejected row; nothing is dropped.
func (s *Store) Stage(ctx context.Context, in StageInput) (Batch, error) {
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		return Batch{}, err
	}
	if len(in.Raw) > MaxUploadBytes {
		return Batch{}, ErrUploadTooLarge
	}
	methodology, err := ResolveMethodology(in.Methodology)
	if err != nil {
		return Batch{}, err
	}
	parsed, err := Parse(in.Filename, in.Raw, ParseOptions{ColumnMap: in.ColumnMap, Methodology: methodology})
	if err != nil {
		return Batch{}, err
	}
	cats, err := s.risks.ListCategories(ctx)
	if err != nil {
		return Batch{}, fmt.Errorf("riskimport: list categories: %w", err)
	}
	b := Batch{
		SourceFilename:  in.Filename,
		SourceFormat:    FormatOf(in.Filename),
		Methodology:     string(parsed.Methodology),
		UploadedBy:      in.UploadedBy,
		Status:          BatchStatusStaged,
		UnmappedColumns: parsed.UnmappedColumns,
		Staged:          make([]BatchRow, 0, len(parsed.Risks)),
		Rejected:        append(make([]RejectedRow, 0, len(parsed.Rejected)), parsed.Rejected...),
	}
	for _, r := range parsed.Risks {
		slug, ok := MatchCategory(cats, r.Category)
		if !ok {
			b.Rejected = append(b.Rejected, RejectedRow{
				RowNumber: r.RowNumber, ExternalRef: r.ExternalRef, Reason: RejectUnknownCategory,
				Detail: fmt.Sprintf("category %q matches none of this organisation's categories by slug or name; valid: %s",
					r.Category, strings.Join(activeSlugs(cats), ", ")),
			})
			continue
		}
		row := BatchRow{
			RowNumber:     r.RowNumber,
			ExternalRef:   r.ExternalRef,
			Title:         r.Title,
			Description:   r.Description,
			Category:      slug,
			InherentScore: r.InherentScore,
			ResidualScore: r.ResidualScore,
			Unmapped:      r.Unmapped,
			Status:        RowStatusPending,
		}
		if r.ReviewDue != nil {
			row.ReviewDue = r.ReviewDue.Format(time.DateOnly)
		}
		b.Staged = append(b.Staged, row)
	}
	sort.SliceStable(b.Rejected, func(i, j int) bool { return b.Rejected[i].RowNumber < b.Rejected[j].RowNumber })
	b.StagedCount = len(b.Staged)
	b.RejectedCount = len(b.Rejected)
	b.TotalRows = b.StagedCount + b.RejectedCount
	return s.createBatch(ctx, b)
}

// Get reads one batch of the caller's tenant with its rows.
func (s *Store) Get(ctx context.Context, id string) (Batch, error) {
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		return Batch{}, err
	}
	return s.getBatch(ctx, id)
}

// MatchCategory returns the slug of the active category ref names: by exact
// slug, or else by display name, case-insensitively on the trimmed value.
// An archived category takes no new risks, so it never matches, and a name
// shared by two active categories matches neither. Value mapping (spec
// #2812, Q13) replaces this in a later slice.
func MatchCategory(cats []risk.Category, ref string) (string, bool) {
	ref = strings.TrimSpace(ref)
	for _, c := range cats {
		if !c.Archived && c.Slug == ref {
			return c.Slug, true
		}
	}
	var match string
	for _, c := range cats {
		if c.Archived || !strings.EqualFold(strings.TrimSpace(c.Name), ref) {
			continue
		}
		if match != "" {
			return "", false
		}
		match = c.Slug
	}
	return match, match != ""
}

func activeSlugs(cats []risk.Category) []string {
	out := make([]string, 0, len(cats))
	for _, c := range cats {
		if !c.Archived {
			out = append(out, c.Slug)
		}
	}
	sort.Strings(out)
	return out
}
