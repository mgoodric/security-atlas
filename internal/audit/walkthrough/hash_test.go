package walkthrough

// Unit tests for the slice-027 hash + export shapes. No DB required;
// these cover the pure-Go contract that an external verifier
// reimplementation (Python / TS in slice 030 OSCAL bridge) must match.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fixedTime returns a stable wall-clock used across the hash tests so the
// expected-bytes assertions can be hard-coded.
func fixedTime() time.Time {
	return time.Date(2026, 5, 13, 12, 34, 56, 789012345, time.UTC)
}

// TestComputeHash_DeterministicForSameInputs asserts AC-3 / AC-7-style
// determinism: the same content rehashes to the same bytes regardless of
// how many times we call computeHash.
func TestComputeHash_DeterministicForSameInputs(t *testing.T) {
	in := hashInputs{
		ControlID:  uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		Narrative:  "The team rotates the API key every 90 days.",
		Transcript: "auditor: walk me through the rotation cadence.",
		CreatedBy:  "engineer-001",
		CreatedAt:  fixedTime(),
		AttachmentHashes: []string{
			"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		},
	}
	h1, err := computeHash(in)
	if err != nil {
		t.Fatalf("computeHash #1: %v", err)
	}
	h2, err := computeHash(in)
	if err != nil {
		t.Fatalf("computeHash #2: %v", err)
	}
	if !hashEqual(h1, h2) {
		t.Fatalf("hash not deterministic: %x vs %x", h1, h2)
	}
	if len(h1) != 32 {
		t.Fatalf("expected 32-byte sha256, got %d", len(h1))
	}
}

// TestComputeHash_AttachmentReorderingIrrelevant asserts that the
// attachment-hash input is order-insensitive (the canonical contract
// sorts the slice). Two callers feeding the same set in different orders
// must compute the same hash.
func TestComputeHash_AttachmentReorderingIrrelevant(t *testing.T) {
	base := hashInputs{
		ControlID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		Narrative: "narrative",
		CreatedBy: "engineer-001",
		CreatedAt: fixedTime(),
	}
	a := base
	a.AttachmentHashes = []string{
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	}
	b := base
	b.AttachmentHashes = []string{
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	ha, err := computeHash(a)
	if err != nil {
		t.Fatalf("computeHash a: %v", err)
	}
	hb, err := computeHash(b)
	if err != nil {
		t.Fatalf("computeHash b: %v", err)
	}
	if !hashEqual(ha, hb) {
		t.Fatalf("reordering changed hash: %x vs %x", ha, hb)
	}
}

// TestComputeHash_AttachmentSetChangeInvalidates asserts AC-6: adding,
// removing, or mutating an attachment must change the hash so tamper
// detection at GET surfaces the drift.
func TestComputeHash_AttachmentSetChangeInvalidates(t *testing.T) {
	base := hashInputs{
		ControlID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		Narrative: "narrative",
		CreatedBy: "engineer-001",
		CreatedAt: fixedTime(),
		AttachmentHashes: []string{
			"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
	}
	hBase, err := computeHash(base)
	if err != nil {
		t.Fatalf("computeHash base: %v", err)
	}

	addOne := base
	addOne.AttachmentHashes = append([]string{}, base.AttachmentHashes...)
	addOne.AttachmentHashes = append(addOne.AttachmentHashes,
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	)
	hAdd, err := computeHash(addOne)
	if err != nil {
		t.Fatalf("computeHash addOne: %v", err)
	}
	if hashEqual(hBase, hAdd) {
		t.Fatalf("adding attachment did not change hash (AC-6 broken)")
	}

	removeAll := base
	removeAll.AttachmentHashes = nil
	hRemove, err := computeHash(removeAll)
	if err != nil {
		t.Fatalf("computeHash removeAll: %v", err)
	}
	if hashEqual(hBase, hRemove) {
		t.Fatalf("removing attachment did not change hash (AC-6 broken)")
	}

	mutate := base
	mutate.AttachmentHashes = []string{
		"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
	}
	hMutate, err := computeHash(mutate)
	if err != nil {
		t.Fatalf("computeHash mutate: %v", err)
	}
	if hashEqual(hBase, hMutate) {
		t.Fatalf("mutating attachment did not change hash (AC-6 broken)")
	}
}

// TestComputeHash_NarrativeOrCreatedByOrControlIDChangeInvalidates asserts
// the content-bound fields (everything in hashInputs) all participate in
// the hash. If any of them slips out of the hash, this test catches it.
func TestComputeHash_FieldSensitivity(t *testing.T) {
	base := hashInputs{
		ControlID:  uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		Narrative:  "original narrative",
		Transcript: "original transcript",
		CreatedBy:  "engineer-001",
		CreatedAt:  fixedTime(),
	}
	hBase, _ := computeHash(base)

	cases := []struct {
		name string
		mut  func(hashInputs) hashInputs
	}{
		{"narrative", func(in hashInputs) hashInputs { in.Narrative = "tampered"; return in }},
		{"transcript", func(in hashInputs) hashInputs { in.Transcript = "tampered"; return in }},
		{"created_by", func(in hashInputs) hashInputs { in.CreatedBy = "other"; return in }},
		{"control_id", func(in hashInputs) hashInputs {
			in.ControlID = uuid.MustParse("22222222-2222-2222-2222-222222222222")
			return in
		}},
		{"created_at", func(in hashInputs) hashInputs {
			in.CreatedAt = fixedTime().Add(time.Nanosecond)
			return in
		}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			h, _ := computeHash(tc.mut(base))
			if hashEqual(h, hBase) {
				t.Fatalf("mutating %s did not change hash", tc.name)
			}
		})
	}
}

// TestComputeHash_CanonicalShape asserts the wire-key order is the one
// the verifier-side reimplementation depends on (control_id, narrative,
// transcript, created_by, created_at, attachment_hashes). Any reorder of
// the struct fields breaks downstream verifiers; this test pins the
// contract.
func TestComputeHash_CanonicalShape(t *testing.T) {
	// We reach into the wire shape indirectly: the encoded JSON for a
	// known hashInputs value must contain the keys in the documented
	// order. Build the same shape via json.Marshal of an anonymous
	// struct mirroring the wire type and compare.
	in := hashInputs{
		ControlID:        uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		Narrative:        "n",
		Transcript:       "t",
		CreatedBy:        "u",
		CreatedAt:        fixedTime(),
		AttachmentHashes: []string{"x"},
	}
	expected := struct {
		ControlID        string   `json:"control_id"`
		Narrative        string   `json:"narrative"`
		Transcript       string   `json:"transcript"`
		CreatedBy        string   `json:"created_by"`
		CreatedAt        string   `json:"created_at"`
		AttachmentHashes []string `json:"attachment_hashes"`
	}{
		ControlID:        in.ControlID.String(),
		Narrative:        in.Narrative,
		Transcript:       in.Transcript,
		CreatedBy:        in.CreatedBy,
		CreatedAt:        in.CreatedAt.Format(time.RFC3339Nano),
		AttachmentHashes: in.AttachmentHashes,
	}
	expectedBytes, err := json.Marshal(expected)
	if err != nil {
		t.Fatalf("marshal expected: %v", err)
	}
	// computeHash's body marshals the same wire and feeds the bytes to
	// sha256. We re-run that step here.
	wantSum := sha256Sum(expectedBytes)
	got, err := computeHash(in)
	if err != nil {
		t.Fatalf("computeHash: %v", err)
	}
	if !hashEqual(got, wantSum) {
		t.Fatalf("hash drifted from documented wire order\n  got:  %s\n  want: %s\n  bytes: %s",
			hex.EncodeToString(got), hex.EncodeToString(wantSum), string(expectedBytes))
	}
}

// TestToExportJSON_StableShape asserts the JSON export keys + types are
// the slice-030 OSCAL bridge contract. Renaming a key would break the
// downstream renderer.
func TestToExportJSON_StableShape(t *testing.T) {
	periodID := uuid.MustParse("99999999-9999-9999-9999-999999999999")
	w := Walkthrough{
		ID:            uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		TenantID:      uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		AuditPeriodID: &periodID,
		ControlID:     uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		Narrative:     "narrative",
		Transcript:    "transcript",
		Status:        StatusDraft,
		CanonicalHash: []byte{0x01, 0x02, 0x03, 0x04},
		CreatedBy:     "engineer-001",
		CreatedAt:     fixedTime(),
		UpdatedAt:     fixedTime(),
		Attachments: []Attachment{{
			ID:             uuid.MustParse("44444444-4444-4444-4444-444444444444"),
			TenantID:       uuid.MustParse("22222222-2222-2222-2222-222222222222"),
			WalkthroughID:  uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			StorageKey:     "tenant-22222222-2222-2222-2222-222222222222/44444444-4444-4444-4444-444444444444",
			ContentType:    "image/png",
			SizeBytes:      1024,
			SHA256Hex:      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			AnnotationsRaw: []byte(`{"regions":[]}`),
			UploadedBy:     "engineer-001",
			UploadedAt:     fixedTime(),
		}},
	}
	ex := ToExportJSON(w)
	out, err := json.Marshal(ex)
	if err != nil {
		t.Fatalf("marshal export: %v", err)
	}
	mustContain := []string{
		`"id":"11111111-1111-1111-1111-111111111111"`,
		`"tenant_id":"22222222-2222-2222-2222-222222222222"`,
		`"audit_period_id":"99999999-9999-9999-9999-999999999999"`,
		`"control_id":"33333333-3333-3333-3333-333333333333"`,
		`"narrative":"narrative"`,
		`"transcript":"transcript"`,
		`"status":"draft"`,
		`"canonical_hash":"01020304"`,
		`"created_by":"engineer-001"`,
		`"tamper_detected":false`,
		`"sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`,
		`"annotations":{"regions":[]}`,
		`"storage_key":"tenant-22222222-2222-2222-2222-222222222222/44444444-4444-4444-4444-444444444444"`,
	}
	s := string(out)
	for _, want := range mustContain {
		if !strings.Contains(s, want) {
			t.Errorf("export JSON missing %q\nfull: %s", want, s)
		}
	}
}

// TestToExportJSON_OmitsAuditPeriodWhenLive asserts that a walkthrough
// without an audit_period_id pin renders without the audit_period_id
// key in JSON (omitempty), so slice 030's OSCAL bridge can distinguish
// "live" from "period-pinned" without sentinel string parsing.
func TestToExportJSON_OmitsAuditPeriodWhenLive(t *testing.T) {
	w := Walkthrough{
		ID:            uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		TenantID:      uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		ControlID:     uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		Narrative:     "narrative",
		Status:        StatusDraft,
		CanonicalHash: []byte{0x01, 0x02},
		CreatedBy:     "engineer-001",
		CreatedAt:     fixedTime(),
		UpdatedAt:     fixedTime(),
	}
	ex := ToExportJSON(w)
	out, _ := json.Marshal(ex)
	if strings.Contains(string(out), `"audit_period_id"`) {
		t.Fatalf("live walkthrough export should omit audit_period_id; got %s", string(out))
	}
}

// sha256Sum mirrors what computeHash does internally so the canonical-
// shape test stays self-contained.
func sha256Sum(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:]
}
