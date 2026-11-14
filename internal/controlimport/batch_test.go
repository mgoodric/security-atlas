package controlimport

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeBatchStore is an in-memory BatchStore for the pure-Go tier; the
// Postgres Store is covered by the API integration tests.
type fakeBatchStore struct {
	frameworks   map[string]FrameworkRef
	requirements map[string]Requirement // lower-cased code -> requirement
	created      *Batch
}

func (f *fakeBatchStore) ResolveFramework(_ context.Context, slug string) (FrameworkRef, bool, error) {
	fw, ok := f.frameworks[slug]
	return fw, ok, nil
}

func (f *fakeBatchStore) ResolveRequirements(_ context.Context, _ string, codes []string) (map[string]Requirement, error) {
	out := map[string]Requirement{}
	for _, c := range codes {
		if r, ok := f.requirements[strings.ToLower(c)]; ok {
			out[strings.ToLower(c)] = r
		}
	}
	return out, nil
}

func (f *fakeBatchStore) CreateBatch(_ context.Context, b Batch) (Batch, error) {
	b.ID = "batch-1"
	f.created = &b
	return b, nil
}

func (f *fakeBatchStore) GetBatch(_ context.Context, id string) (Batch, error) {
	if f.created == nil || f.created.ID != id {
		return Batch{}, ErrBatchNotFound
	}
	return *f.created, nil
}

func newFakeBatchStore() *fakeBatchStore {
	return &fakeBatchStore{
		frameworks: map[string]FrameworkRef{"soc2": {Slug: "soc2", VersionID: "v-1", Version: "2017"}},
		requirements: map[string]Requirement{
			"cc6.1": {Code: "CC6.1", RequirementID: "r-61"},
			"cc6.2": {Code: "CC6.2", RequirementID: "r-62"},
		},
	}
}

const batchTenant = "11111111-1111-1111-1111-111111111111"

func TestStageResolvesTestedUnderAndRejectsUnknownCodes(t *testing.T) {
	t.Parallel()
	store := newFakeBatchStore()
	raw := []byte("Control ID,Title,Description,Criteria\n" +
		"AC-1,MFA,Users use MFA,cc6.1; CC6.2\n" +
		"AC-2,Encryption,Data encrypted,\"CC6.1, CC9.9, CC9.8\"\n" +
		",Missing,No id,\n" +
		"AC-3,Plain,No criteria,\n")
	b, err := NewStager(store).Stage(tenantCtx(t, batchTenant), StageInput{
		Filename: "controls.csv", Raw: raw, FrameworkSlug: "soc2", UploadedBy: "user-1",
	})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if b.TotalRows != 4 || b.StagedCount != 2 || b.RejectedCount != 2 {
		t.Fatalf("counts = %d/%d/%d, want 4 total, 2 staged, 2 rejected", b.TotalRows, b.StagedCount, b.RejectedCount)
	}
	if b.Framework.VersionID != "v-1" || b.SourceFormat != "csv" || b.UploadedBy != "user-1" || b.Status != BatchStatusStaged {
		t.Errorf("batch header = %+v", b)
	}
	got := b.Staged[0].TestedUnder
	if len(got) != 2 || got[0].Code != "CC6.1" || got[0].RequirementID != "r-61" || got[1].RequirementID != "r-62" {
		t.Errorf("tested under = %+v, want canonical codes with requirement ids", got)
	}
	if b.Staged[1].ExternalID != "AC-3" || len(b.Staged[1].TestedUnder) != 0 {
		t.Errorf("no-criteria row = %+v", b.Staged[1])
	}
	// Rejected rows come back ordered by row number, parser and code
	// resolution rejections merged.
	if b.Rejected[0].RowNumber != 3 || b.Rejected[0].Reason != RejectUnknownTestedUnder ||
		b.Rejected[0].Detail != "CC9.9, CC9.8 are not in soc2 2017" {
		t.Errorf("rejected[0] = %+v", b.Rejected[0])
	}
	if b.Rejected[1].RowNumber != 4 || b.Rejected[1].Reason != RejectMissingField {
		t.Errorf("rejected[1] = %+v", b.Rejected[1])
	}
}

func TestStageRefusals(t *testing.T) {
	t.Parallel()
	raw := []byte("Control ID,Title,Description\nAC-1,MFA,Users use MFA\n")
	cases := []struct {
		name string
		ctx  context.Context
		in   StageInput
		want error
	}{
		{"no framework", tenantCtx(t, batchTenant), StageInput{Filename: "c.csv", Raw: raw}, ErrFrameworkRequired},
		{"unknown framework", tenantCtx(t, batchTenant), StageInput{Filename: "c.csv", Raw: raw, FrameworkSlug: "iso"}, ErrUnknownFramework},
		{"oversize", tenantCtx(t, batchTenant), StageInput{Filename: "c.csv", Raw: make([]byte, MaxUploadBytes+1), FrameworkSlug: "soc2"}, ErrUploadTooLarge},
		{"format", tenantCtx(t, batchTenant), StageInput{Filename: "c.txt", Raw: raw, FrameworkSlug: "soc2"}, ErrUnsupportedFormat},
		{"no tenant", context.Background(), StageInput{Filename: "c.csv", Raw: raw, FrameworkSlug: "soc2"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeBatchStore()
			_, err := NewStager(store).Stage(tc.ctx, tc.in)
			if err == nil {
				t.Fatal("Stage succeeded, want refusal")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
			if store.created != nil {
				t.Errorf("refused upload persisted a batch")
			}
		})
	}
}

func TestGetRequiresTenantAndReturnsBatch(t *testing.T) {
	t.Parallel()
	store := newFakeBatchStore()
	s := NewStager(store)
	if _, err := s.Get(context.Background(), "batch-1"); err == nil {
		t.Error("Get without tenant succeeded")
	}
	if _, err := s.Get(tenantCtx(t, batchTenant), "batch-1"); !errors.Is(err, ErrBatchNotFound) {
		t.Errorf("missing batch err = %v", err)
	}
	if _, err := s.Stage(tenantCtx(t, batchTenant), StageInput{Filename: "c.csv", Raw: []byte("ID,Title,Description\nA,B,C\n"), FrameworkSlug: "soc2"}); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if b, err := s.Get(tenantCtx(t, batchTenant), "batch-1"); err != nil || b.StagedCount != 1 {
		t.Errorf("Get = %+v, %v", b, err)
	}
}
