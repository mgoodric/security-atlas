package controlimport

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mgoodric/security-atlas/internal/tenancy"
)

var (
	ErrFrameworkRequired = errors.New("controlimport: framework is required")
	ErrUnknownFramework  = errors.New("controlimport: framework has no current version")
	ErrBatchNotFound     = errors.New("controlimport: import batch not found")
)

// BatchStatusStaged is a batch whose rows are staged and awaiting review.
const BatchStatusStaged = "staged"

// RowStatusPending is a staged row awaiting review.
const RowStatusPending = "pending"

// FrameworkRef is the framework version that tested-under codes resolve in.
type FrameworkRef struct {
	Slug      string `json:"slug"`
	VersionID string `json:"version_id"`
	Version   string `json:"version"`
}

// Requirement is one resolved tested-under criterion.
type Requirement struct {
	Code          string `json:"code"`
	RequirementID string `json:"requirement_id"`
}

// BatchControl is a staged row as persisted in an import batch.
type BatchControl struct {
	ID                 string        `json:"id"`
	RowNumber          int           `json:"row_number"`
	ExternalID         string        `json:"external_id"`
	Title              string        `json:"title"`
	Description        string        `json:"description"`
	TestedUnder        []Requirement `json:"tested_under"`
	ImplementationType string        `json:"implementation_type,omitempty"`
	Applicability      string        `json:"applicability,omitempty"`
	Status             string        `json:"status"`
}

// Batch is one upload: its counts, staged rows and rejected rows. Every
// non-blank data row of the file is in exactly one of Staged or Rejected, so
// TotalRows == StagedCount + RejectedCount.
type Batch struct {
	ID              string         `json:"id"`
	Framework       FrameworkRef   `json:"framework"`
	SourceFilename  string         `json:"source_filename"`
	SourceFormat    string         `json:"source_format"`
	UploadedBy      string         `json:"uploaded_by"`
	Status          string         `json:"status"`
	TotalRows       int            `json:"total_rows"`
	StagedCount     int            `json:"staged_count"`
	RejectedCount   int            `json:"rejected_count"`
	UnmappedColumns []string       `json:"unmapped_columns"`
	CreatedAt       time.Time      `json:"created_at"`
	Staged          []BatchControl `json:"staged"`
	Rejected        []RejectedRow  `json:"rejected"`
}

// BatchStore persists import batches. Implementations apply the caller's
// tenant context to every batch read and write (RLS); framework and
// requirement reads are global catalog reads.
type BatchStore interface {
	// ResolveFramework returns the current version of the framework slug.
	ResolveFramework(ctx context.Context, slug string) (FrameworkRef, bool, error)
	// ResolveRequirements maps each code it finds in the framework version to
	// its requirement, keyed by the lower-cased code.
	ResolveRequirements(ctx context.Context, versionID string, codes []string) (map[string]Requirement, error)
	// CreateBatch persists the batch with its staged and rejected rows and
	// returns it with server-assigned IDs and timestamps.
	CreateBatch(ctx context.Context, b Batch) (Batch, error)
	// GetBatch reads one batch of the caller's tenant, or ErrBatchNotFound.
	GetBatch(ctx context.Context, id string) (Batch, error)
}

// StageInput is one upload to stage.
type StageInput struct {
	Filename      string
	Raw           []byte
	FrameworkSlug string
	ColumnMap     map[string]string
	UploadedBy    string
}

// Stager turns an uploaded control sheet into a persisted import batch.
type Stager struct {
	store BatchStore
}

func NewStager(store BatchStore) *Stager { return &Stager{store: store} }

// Stage parses the upload, resolves each row's tested-under codes against the
// framework's current version, and persists the batch. A row with an unknown
// code becomes a rejected row; nothing is dropped.
func (s *Stager) Stage(ctx context.Context, in StageInput) (Batch, error) {
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		return Batch{}, err
	}
	slug := strings.TrimSpace(in.FrameworkSlug)
	if slug == "" {
		return Batch{}, ErrFrameworkRequired
	}
	if len(in.Raw) > MaxUploadBytes {
		return Batch{}, ErrUploadTooLarge
	}
	parsed, err := Parse(in.Filename, in.Raw, ParseOptions{ColumnMap: in.ColumnMap})
	if err != nil {
		return Batch{}, err
	}
	fw, ok, err := s.store.ResolveFramework(ctx, slug)
	if err != nil {
		return Batch{}, fmt.Errorf("controlimport: resolve framework: %w", err)
	}
	if !ok {
		return Batch{}, fmt.Errorf("%w: %q", ErrUnknownFramework, slug)
	}
	resolved, err := s.store.ResolveRequirements(ctx, fw.VersionID, allCodes(parsed.Controls))
	if err != nil {
		return Batch{}, fmt.Errorf("controlimport: resolve tested-under codes: %w", err)
	}

	b := Batch{
		Framework:       fw,
		SourceFilename:  in.Filename,
		SourceFormat:    FormatOf(in.Filename),
		UploadedBy:      in.UploadedBy,
		Status:          BatchStatusStaged,
		UnmappedColumns: parsed.UnmappedColumns,
		Staged:          make([]BatchControl, 0, len(parsed.Controls)),
		Rejected:        append(make([]RejectedRow, 0, len(parsed.Rejected)), parsed.Rejected...),
	}
	for _, c := range parsed.Controls {
		reqs, unknown := resolveCodes(c.TestedUnder, resolved)
		if len(unknown) > 0 {
			b.Rejected = append(b.Rejected, RejectedRow{
				RowNumber: c.RowNumber, ExternalID: c.ExternalID,
				Reason: RejectUnknownTestedUnder,
				Detail: fmt.Sprintf("%s %s not in %s %s", strings.Join(unknown, ", "), codeNoun(len(unknown)), fw.Slug, fw.Version),
			})
			continue
		}
		b.Staged = append(b.Staged, BatchControl{
			RowNumber:          c.RowNumber,
			ExternalID:         c.ExternalID,
			Title:              c.Title,
			Description:        c.Description,
			TestedUnder:        reqs,
			ImplementationType: c.ImplementationType,
			Applicability:      c.Applicability,
			Status:             RowStatusPending,
		})
	}
	sort.SliceStable(b.Rejected, func(i, j int) bool { return b.Rejected[i].RowNumber < b.Rejected[j].RowNumber })
	b.StagedCount = len(b.Staged)
	b.RejectedCount = len(b.Rejected)
	b.TotalRows = b.StagedCount + b.RejectedCount
	return s.store.CreateBatch(ctx, b)
}

// Get reads one batch of the caller's tenant.
func (s *Stager) Get(ctx context.Context, id string) (Batch, error) {
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		return Batch{}, err
	}
	return s.store.GetBatch(ctx, id)
}

func allCodes(controls []StagedControl) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range controls {
		for _, code := range c.TestedUnder {
			if !seen[code] {
				seen[code] = true
				out = append(out, code)
			}
		}
	}
	return out
}

func resolveCodes(codes []string, resolved map[string]Requirement) ([]Requirement, []string) {
	reqs := make([]Requirement, 0, len(codes))
	var unknown []string
	for _, code := range codes {
		r, ok := resolved[strings.ToLower(code)]
		if !ok {
			unknown = append(unknown, code)
			continue
		}
		reqs = append(reqs, r)
	}
	return reqs, unknown
}

func codeNoun(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}
