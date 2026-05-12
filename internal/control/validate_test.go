package control

import (
	"context"
	"errors"
	"testing"
)

// stubRegistry implements SchemaRegistry for unit tests.
type stubRegistry struct {
	known map[string]map[string]bool
}

func (s *stubRegistry) IsRegistered(kind, ver string) bool {
	if s == nil {
		return false
	}
	v, ok := s.known[kind]
	if !ok {
		return false
	}
	return v[ver]
}

func TestValidateApplicabilityExpr_Empty(t *testing.T) {
	t.Parallel()
	b := &Bundle{Manifest: Manifest{}}
	if err := b.ValidateApplicabilityExpr(); err != nil {
		t.Fatalf("empty expr should pass: %v", err)
	}
}

func TestValidateApplicabilityExpr_RejectsUnknownOp(t *testing.T) {
	t.Parallel()
	b := &Bundle{
		Manifest: Manifest{
			ApplicabilityExpr: map[string]any{
				"op": "xor", // not in slice-017's allowed set
				"args": []any{
					map[string]any{"op": "eq", "dim": "environment", "value": "prod"},
				},
			},
		},
	}
	if err := b.ValidateApplicabilityExpr(); err == nil {
		t.Fatalf("expected rejection of unknown op")
	}
}

func TestValidateApplicabilityExpr_AcceptsWellFormed(t *testing.T) {
	t.Parallel()
	b := &Bundle{
		Manifest: Manifest{
			ApplicabilityExpr: map[string]any{
				"op": "and",
				"args": []any{
					map[string]any{"op": "eq", "dim": "environment", "value": "prod"},
					map[string]any{"op": "in", "dim": "data_classification", "values": []any{"restricted", "confidential"}},
				},
			},
		},
	}
	if err := b.ValidateApplicabilityExpr(); err != nil {
		t.Fatalf("well-formed expr rejected: %v", err)
	}
}

func TestValidateEvidenceKinds_NilRegistryNoOp(t *testing.T) {
	t.Parallel()
	b := &Bundle{
		Manifest: Manifest{
			EvidenceQueries: []EvidenceQuery{{ID: "query_one", Language: "rego", Expression: "x", EvidenceKind: "anything"}},
		},
	}
	if err := b.ValidateEvidenceKinds(context.Background(), nil); err != nil {
		t.Fatalf("nil registry must be a no-op; got %v", err)
	}
}

func TestValidateEvidenceKinds_AcceptsRegisteredKind(t *testing.T) {
	t.Parallel()
	reg := &stubRegistry{
		known: map[string]map[string]bool{
			"aws.s3.bucket_encryption_state": {"1.0.0": true},
		},
	}
	b := &Bundle{
		Manifest: Manifest{
			EvidenceQueries: []EvidenceQuery{
				{ID: "query_one", Language: "rego", Expression: "x", EvidenceKind: "aws.s3.bucket_encryption_state"},
			},
		},
	}
	if err := b.ValidateEvidenceKinds(context.Background(), reg); err != nil {
		t.Fatalf("expected ok; got %v", err)
	}
}

func TestValidateEvidenceKinds_RejectsUnknownKind(t *testing.T) {
	t.Parallel()
	reg := &stubRegistry{known: map[string]map[string]bool{}}
	b := &Bundle{
		Manifest: Manifest{
			EvidenceQueries: []EvidenceQuery{
				{ID: "query_one", Language: "rego", Expression: "x", EvidenceKind: "make.up.kind"},
			},
		},
	}
	err := b.ValidateEvidenceKinds(context.Background(), reg)
	if err == nil {
		t.Fatalf("expected rejection")
	}
	var ue ErrUnknownEvidenceKind
	if !errors.As(err, &ue) {
		t.Fatalf("expected ErrUnknownEvidenceKind; got %T %v", err, err)
	}
	if ue.Kind != "make.up.kind" {
		t.Fatalf("expected Kind to be carried; got %q", ue.Kind)
	}
}

func TestValidateEvidenceKinds_SkipsEmptyKind(t *testing.T) {
	t.Parallel()
	// A query without an evidence_kind is acceptable.
	reg := &stubRegistry{known: map[string]map[string]bool{}}
	b := &Bundle{
		Manifest: Manifest{
			EvidenceQueries: []EvidenceQuery{
				{ID: "query_one", Language: "rego", Expression: "x", EvidenceKind: ""},
			},
		},
	}
	if err := b.ValidateEvidenceKinds(context.Background(), reg); err != nil {
		t.Fatalf("expected ok for empty kind; got %v", err)
	}
}
