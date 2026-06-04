package oscal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

// Manifest is the bundle's manifest.json: it lists every member, records
// the export provenance, and carries the bundle signature. An auditor
// reading the bundle starts here.
type Manifest struct {
	// SchemaVersion identifies the manifest shape. Bumped on any
	// breaking change to this struct.
	SchemaVersion string `json:"schema_version"`
	// AuditPeriodID is the frozen period the bundle was generated from.
	AuditPeriodID string `json:"audit_period_id"`
	// FrozenAt is the period's freeze horizon, RFC-3339. Every member
	// was generated from data at or before this instant (invariant 10).
	FrozenAt string `json:"frozen_at"`
	// OSCALVersion is the OSCAL spec version every member conforms to.
	OSCALVersion string `json:"oscal_version"`
	// GeneratedAt is the wall-clock instant the export ran, RFC-3339.
	GeneratedAt string `json:"generated_at"`
	// RequestedBy is the credential / user id that triggered the export.
	RequestedBy string `json:"requested_by"`
	// Members lists each OSCAL document in the bundle with its content
	// hash.
	Members []ManifestMember `json:"members"`
	// Signature is the detached signature over the bundle digest. It is
	// ALWAYS present in a written manifest — Exporter.Export aborts
	// before WriteBundle if signing failed (AC-5, P0 anti-criterion).
	Signature Signature `json:"signature"`
}

// ManifestMember is one OSCAL document's manifest entry.
type ManifestMember struct {
	Filename  string `json:"filename"`
	ModelType string `json:"model_type"`
	SHA256    string `json:"sha256"`
	SizeBytes int    `json:"size_bytes"`
}

// ManifestFilename is the fixed name of the manifest inside the bundle.
const ManifestFilename = "manifest.json"

// SchemaVersion is the current manifest schema version.
const SchemaVersion = "oscal-export-bundle/v1"

// newMember builds a BundleMember, computing the content hash.
func newMember(filename, modelType string, jsonBytes []byte) BundleMember {
	sum := sha256.Sum256(jsonBytes)
	return BundleMember{
		Filename:  filename,
		ModelType: modelType,
		JSON:      jsonBytes,
		SHA256:    hex.EncodeToString(sum[:]),
	}
}

// assembleBundle builds the in-memory Bundle from the aggregate and the
// serialized members. The Signature is left zero here — Exporter.Export
// fills it immediately after via Signer.SignBundle, and refuses to
// return a Bundle whose signing failed.
func assembleBundle(agg *aggregate, members []BundleMember, requestedBy string) *Bundle {
	return &Bundle{
		AuditPeriodID: uuidFromPg(agg.period.ID),
		FrozenAt:      agg.frozenAt.UTC().Format(time.RFC3339),
		OSCALVersion:  OSCALVersion,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		RequestedBy:   requestedBy,
		Members:       members,
	}
}

// Manifest renders the bundle's manifest.json content.
func (b *Bundle) Manifest() Manifest {
	members := make([]ManifestMember, 0, len(b.Members))
	for _, m := range b.Members {
		members = append(members, ManifestMember{
			Filename:  m.Filename,
			ModelType: m.ModelType,
			SHA256:    m.SHA256,
			SizeBytes: len(m.JSON),
		})
	}
	return Manifest{
		SchemaVersion: SchemaVersion,
		AuditPeriodID: b.AuditPeriodID.String(),
		FrozenAt:      b.FrozenAt,
		OSCALVersion:  b.OSCALVersion,
		GeneratedAt:   b.GeneratedAt,
		RequestedBy:   b.RequestedBy,
		Members:       members,
		Signature:     b.Signature,
	}
}

// WriteBundle persists the bundle to dir: one file per OSCAL member plus
// manifest.json. dir is created if absent. Returns the manifest path.
//
// WriteBundle refuses to write a bundle whose Signature is zero — a
// belt-and-braces enforcement of the P0 anti-criterion at the
// persistence layer (Exporter.Export already aborts before reaching
// here on a signing failure).
func (b *Bundle) WriteBundle(dir string) (string, error) {
	if b.Signature.Algorithm == "" || b.Signature.Signature == "" {
		return "", fmt.Errorf("%w: refusing to write an unsigned bundle", ErrSigningFailed)
	}
	if len(b.Members) == 0 {
		return "", fmt.Errorf("oscal: refusing to write an empty bundle")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("oscal: create bundle dir: %w", err)
	}
	for _, m := range b.Members {
		path := filepath.Join(dir, m.Filename)
		if err := os.WriteFile(path, m.JSON, 0o640); err != nil {
			return "", fmt.Errorf("oscal: write %s: %w", m.Filename, err)
		}
	}
	manifestBytes, err := json.MarshalIndent(b.Manifest(), "", "  ")
	if err != nil {
		return "", fmt.Errorf("oscal: marshal manifest: %w", err)
	}
	manifestPath := filepath.Join(dir, ManifestFilename)
	if err := os.WriteFile(manifestPath, manifestBytes, 0o640); err != nil {
		return "", fmt.Errorf("oscal: write manifest: %w", err)
	}
	return manifestPath, nil
}

// ReadBundle loads a written bundle back from dir: it parses manifest.json
// and re-reads every member file so the verifier can recompute the digest
// from the actual on-disk bytes. It is the inverse of WriteBundle and the
// loader the CLI `verify` command uses.
//
// ReadBundle does NOT itself verify — it only reconstructs the in-memory
// Bundle (including its recorded Signature, with whatever Mode the
// manifest carries, or none for a pre-413 manifest). The caller then runs
// VerifyBundle / VerifyBundleWithCosign.
func ReadBundle(dir string) (*Bundle, error) {
	manifestBytes, err := os.ReadFile(filepath.Join(dir, ManifestFilename)) //nolint:gosec // dir is an operator-supplied local path
	if err != nil {
		return nil, fmt.Errorf("oscal: read manifest: %w", err)
	}
	var man Manifest
	if err := json.Unmarshal(manifestBytes, &man); err != nil {
		return nil, fmt.Errorf("oscal: parse manifest: %w", err)
	}
	periodID, err := uuid.Parse(man.AuditPeriodID)
	if err != nil {
		return nil, fmt.Errorf("oscal: manifest audit_period_id is not a UUID: %w", err)
	}
	members := make([]BundleMember, 0, len(man.Members))
	for _, mm := range man.Members {
		if mm.Filename == ManifestFilename || mm.Filename == "" {
			return nil, fmt.Errorf("oscal: invalid member filename %q in manifest", mm.Filename)
		}
		// Guard against path traversal in a hostile manifest: the member
		// filename must be a bare basename, not an absolute or parent path.
		if filepath.Base(mm.Filename) != mm.Filename {
			return nil, fmt.Errorf("oscal: member filename %q must be a bare name (no path separators)", mm.Filename)
		}
		jsonBytes, err := os.ReadFile(filepath.Join(dir, mm.Filename)) //nolint:gosec // basename validated above
		if err != nil {
			return nil, fmt.Errorf("oscal: read member %s: %w", mm.Filename, err)
		}
		// Re-derive the member hash from the actual bytes (newMember), so a
		// member-file tamper changes the digest and verification fails.
		members = append(members, newMember(mm.Filename, mm.ModelType, jsonBytes))
	}
	return &Bundle{
		AuditPeriodID: periodID,
		FrozenAt:      man.FrozenAt,
		OSCALVersion:  man.OSCALVersion,
		GeneratedAt:   man.GeneratedAt,
		RequestedBy:   man.RequestedBy,
		Members:       members,
		Signature:     man.Signature,
	}, nil
}
