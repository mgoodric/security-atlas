package control

import (
	"strings"
	"testing"
)

// TestFixture_MinimalParsesCleanly covers the basic AC-1 path: a hand-authored
// bundle directory parses without error and structural validation passes.
func TestFixture_MinimalParsesCleanly(t *testing.T) {
	t.Parallel()
	b, err := ParseDirectory("testdata/minimal-bundle")
	if err != nil {
		t.Fatalf("ParseDirectory: %v", err)
	}
	if b.Manifest.BundleID != "minimal_test_control" {
		t.Fatalf("bundle_id wrong: %s", b.Manifest.BundleID)
	}
}

// TestFixture_AWSMFAParsesWithQueries — full-featured fixture with
// applicability_expr and evidence_queries. ValidateApplicabilityExpr passes
// independent of any registry.
func TestFixture_AWSMFAParsesWithQueries(t *testing.T) {
	t.Parallel()
	b, err := ParseDirectory("testdata/aws-mfa-bundle")
	if err != nil {
		t.Fatalf("ParseDirectory: %v", err)
	}
	if err := b.ValidateApplicabilityExpr(); err != nil {
		t.Fatalf("applicability_expr invalid: %v", err)
	}
	if len(b.Manifest.EvidenceQueries) != 1 {
		t.Fatalf("expected 1 query; got %d", len(b.Manifest.EvidenceQueries))
	}
	if b.Manifest.EvidenceQueries[0].Language != "rego" {
		t.Fatalf("expected rego query language; got %s", b.Manifest.EvidenceQueries[0].Language)
	}
}

// TestFixture_ManualBundle covers AC-1 + canvas invariant 9: manual
// implementation_type is first-class, no degraded shape.
func TestFixture_ManualBundle(t *testing.T) {
	t.Parallel()
	b, err := ParseDirectory("testdata/manual-bundle")
	if err != nil {
		t.Fatalf("ParseDirectory: %v", err)
	}
	if b.Manifest.ImplementationType != "manual_periodic" {
		t.Fatalf("expected manual_periodic; got %s", b.Manifest.ImplementationType)
	}
	if len(b.Manifest.ManualEvidenceSchema) == 0 {
		t.Fatalf("expected manual_evidence_schema set")
	}
}

// TestFixture_NoAnchorIsRejected — AC-4 + canvas invariant 7. A bundle
// missing scf_anchor_id rejects at parse with the field pointed at.
func TestFixture_NoAnchorIsRejected(t *testing.T) {
	t.Parallel()
	_, err := ParseDirectory("testdata/no-anchor-bundle")
	if err == nil {
		t.Fatalf("expected rejection")
	}
	if !strings.Contains(err.Error(), "scf_anchor_id") {
		t.Fatalf("error must point at the offending field; got %v", err)
	}
}

// TestFixture_BadApplicabilityIsRejected — AC-5.
func TestFixture_BadApplicabilityIsRejected(t *testing.T) {
	t.Parallel()
	b, err := ParseDirectory("testdata/bad-applicability")
	if err != nil {
		t.Fatalf("parse should succeed (op rejection happens in ValidateApplicabilityExpr): %v", err)
	}
	if err := b.ValidateApplicabilityExpr(); err == nil {
		t.Fatalf("expected rejection of unknown op")
	}
}
