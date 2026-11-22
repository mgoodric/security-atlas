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
	// CategoryValue is the row's category cell; Category the slug of the
	// tenant category the batch maps it to, empty while unmapped (#2844).
	CategoryValue string `json:"category_value"`
	Category      string `json:"category"`
	// ThemeValues are the row's theme values; Themes the theme names the
	// mapped ones map to.
	ThemeValues []string `json:"theme_values"`
	Themes      []string `json:"themes"`
	// ReviewDue is a YYYY-MM-DD date, empty when unset.
	ReviewDue string `json:"review_due,omitempty"`
	// InherentScore and ResidualScore are in the batch methodology's
	// shape; absent is unscored, and an absent residual takes the create
	// path's default.
	InherentScore json.RawMessage `json:"inherent_score,omitempty"`
	ResidualScore json.RawMessage `json:"residual_score,omitempty"`
	// Treatment is what the risk is created with. RequestedTreatment is
	// the register's when staging downgraded it to untreated (a mitigate
	// with no resolved control link, a transfer with no instrument
	// reference), empty otherwise.
	Treatment          string `json:"treatment"`
	RequestedTreatment string `json:"requested_treatment,omitempty"`
	// Justification and ProposedUntil (YYYY-MM-DD) are an accepted row's
	// proposal: the risk is created with a proposed acceptance, never an
	// approved one (#2371).
	Justification string `json:"justification,omitempty"`
	ProposedUntil string `json:"proposed_until,omitempty"`
	// HistoricalAccepter is who the register says accepted the risk. It is
	// history only and never the risk's accepter.
	HistoricalAccepter  string `json:"historical_accepter,omitempty"`
	InstrumentReference string `json:"instrument_reference,omitempty"`
	// LinkedControlIDs are the tenant controls the row's linked-controls
	// cell resolved to, by control id or bundle id; UnresolvedControls the
	// refs that matched none.
	LinkedControlIDs   []string `json:"linked_control_ids"`
	UnresolvedControls []string `json:"unresolved_controls"`
	// OwnerUserID is the active user of the tenant the owner cell matched
	// by email; OwnerUnlinked the cell when it matched none, which also
	// flags the row owner_unlinked (#2843). At most one is set.
	OwnerUserID   string            `json:"owner_user_id,omitempty"`
	OwnerUnlinked string            `json:"owner_unlinked,omitempty"`
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
	Methodology     string   `json:"methodology"`
	UploadedBy      string   `json:"uploaded_by"`
	Status          string   `json:"status"`
	TotalRows       int      `json:"total_rows"`
	StagedCount     int      `json:"staged_count"`
	RejectedCount   int      `json:"rejected_count"`
	UnmappedColumns []string `json:"unmapped_columns"`
	// CategoryValues and ThemeValues are the batch's distinct category and
	// theme values, each with the category or theme it maps to (#2844).
	CategoryValues []ValueMapping `json:"category_values"`
	ThemeValues    []ValueMapping `json:"theme_values"`
	CreatedAt      time.Time      `json:"created_at"`
	Staged         []BatchRow     `json:"staged"`
	Rejected       []RejectedRow  `json:"rejected"`
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

// Stage parses the upload and persists the batch with its distinct category
// and theme values, each mapped to the active category or visible theme it
// already names. A value that names none stays unmapped for the reviewer
// to map; staging never creates one and never rejects a row for it.
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
	themes, err := s.risks.ListVisibleThemes(ctx)
	if err != nil {
		return Batch{}, fmt.Errorf("riskimport: list themes: %w", err)
	}
	var refs, owners []string
	for _, r := range parsed.Risks {
		refs = append(refs, r.ControlRefs...)
		owners = append(owners, r.Owner)
	}
	controls, err := s.resolveControls(ctx, refs)
	if err != nil {
		return Batch{}, err
	}
	ownerIDs, err := s.resolveOwners(ctx, owners)
	if err != nil {
		return Batch{}, err
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
	values := distinctValues(parsed.Risks, cats, themes)
	targets := valueTargets{ValueKindCategory: {}, ValueKindTheme: {}}
	for _, v := range values {
		targets[v.Kind][valueKey(v.Value)] = v.Target
	}
	for _, r := range parsed.Risks {
		row := BatchRow{
			RowNumber:     r.RowNumber,
			ExternalRef:   r.ExternalRef,
			Title:         r.Title,
			Description:   r.Description,
			CategoryValue: r.Category,
			ThemeValues:   r.Themes,
			InherentScore: r.InherentScore,
			ResidualScore: r.ResidualScore,
			Justification: r.Justification,
			// The register's accepter is history: a create never names
			// an accepter (#2371).
			HistoricalAccepter:  r.HistoricalAccepter,
			InstrumentReference: r.InstrumentReference,
			Unmapped:            r.Unmapped,
			Status:              RowStatusPending,
		}
		if r.ReviewDue != nil {
			row.ReviewDue = r.ReviewDue.Format(time.DateOnly)
		}
		if r.ProposedUntil != nil {
			row.ProposedUntil = r.ProposedUntil.Format(time.DateOnly)
		}
		applyTreatmentRules(&row, r.Treatment, r.ControlRefs, controls)
		applyOwner(&row, r.Owner, ownerIDs)
		row.Category, row.Themes, _ = targets.resolveRow(row.CategoryValue, row.ThemeValues)
		b.Staged = append(b.Staged, row)
	}
	sort.SliceStable(b.Rejected, func(i, j int) bool { return b.Rejected[i].RowNumber < b.Rejected[j].RowNumber })
	b.StagedCount = len(b.Staged)
	b.RejectedCount = len(b.Rejected)
	b.TotalRows = b.StagedCount + b.RejectedCount
	b.CategoryValues, b.ThemeValues = splitByKind(values, b.Staged)
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
// shared by two active categories matches neither. It is how a batch's
// category value is mapped at upload, and how a mapping target is read.
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
