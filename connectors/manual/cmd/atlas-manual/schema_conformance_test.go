package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/api/schemaregistry"

	"github.com/mgoodric/security-atlas/connectors/manual/internal/manualcsv"
	"github.com/mgoodric/security-atlas/connectors/manual/internal/manuals3"
)

const conformanceTenant = "11111111-1111-1111-1111-111111111111"

// connectorRecords builds one real record per path, through the same
// builders the subcommands call.
func connectorRecords(t *testing.T) map[string]*evidencev1.EvidenceRecord {
	t.Helper()
	scope := []*evidencev1.ScopeDimension{{Key: "environment", Values: []string{"prod"}}}
	observed := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)

	local, err := buildLocalRecord(manualcsv.Row{
		Index:  0,
		Header: []string{"asset_id", "disposal_method"},
		Fields: []string{"SN-123", "wiped"},
	}, "/inbox/disposals.csv", "disposals.csv", "scf:AST-22", scope, observed)
	if err != nil {
		t.Fatalf("buildLocalRecord: %v", err)
	}
	s3rec, err := buildS3Record(manuals3.Object{
		Bucket: "evidence", Key: "bcp/2026-plan.pdf", ETag: `"abc123"`, Size: 4096,
		LastModified: time.Date(2026, 11, 1, 9, 30, 0, 0, time.UTC),
	}, "scf:BCD-01", scope, observed)
	if err != nil {
		t.Fatalf("buildS3Record: %v", err)
	}
	sftpRec, err := buildSFTPRecord("sftp.example.com", "/inbox/q3-disposal.pdf", 8192,
		time.Date(2026, 11, 2, 8, 0, 0, 123456789, time.UTC), "scf:AST-22", scope, observed)
	if err != nil {
		t.Fatalf("buildSFTPRecord: %v", err)
	}
	return map[string]*evidencev1.EvidenceRecord{"local": local, "s3": s3rec, "sftp": sftpRec}
}

// validateAs runs rec's payload through the check ingest applies to every
// push (internal/evidence/ingest: protojson-marshal the payload, then
// ValidatePayload against the registered schema for kind + version).
func validateAs(t *testing.T, rec *evidencev1.EvidenceRecord, version string) error {
	t.Helper()
	reg, err := schemaregistry.NewEmbedded(schemaregistry.PlatformSchemasFS())
	if err != nil {
		t.Fatalf("NewEmbedded: %v", err)
	}
	payloadJSON, err := protojson.Marshal(rec.GetPayload())
	if err != nil {
		t.Fatalf("protojson.Marshal: %v", err)
	}
	return reg.ValidatePayload(context.Background(), conformanceTenant, rec.GetEvidenceKind(), version, payloadJSON)
}

// #2940: every path's real record passes ingest's schema validation at the
// version it declares.
func TestConnectorRecords_ConformToRegisteredSchema(t *testing.T) {
	t.Parallel()
	for name, rec := range connectorRecords(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := validateAs(t, rec, rec.GetSchemaVersion()); err != nil {
				t.Fatalf("ingest schema validation rejected the %s payload at %s: %v", name, rec.GetSchemaVersion(), err)
			}
		})
	}
}

// Pins the defect #2940 fixed: under 1.0.0, which the connector declared
// before, ingest rejected every path's payload for its undeclared fields.
func TestConnectorRecords_RejectedBy100(t *testing.T) {
	t.Parallel()
	for name, rec := range connectorRecords(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := validateAs(t, rec, "1.0.0"); err == nil {
				t.Fatalf("1.0.0 accepted the %s payload; the per-path fields should be undeclared there", name)
			}
		})
	}
}

func TestManualUpload110_AdditiveOver100(t *testing.T) {
	t.Parallel()
	read := func(v string) []byte {
		t.Helper()
		b, err := fs.ReadFile(schemaregistry.PlatformSchemasFS(), "manual.upload/"+v+".json")
		if err != nil {
			t.Fatalf("read %s: %v", v, err)
		}
		return b
	}
	if err := schemaregistry.CheckAdditiveOver(read("1.0.0"), read("1.1.0")); err != nil {
		t.Fatalf("1.1.0 is not additive over 1.0.0: %v", err)
	}
}

// #2940: a re-run over an unchanged CSV row in a later hour builds the
// same idempotency key, so the ledger holds the first push and its
// observed_at; an edited row gets a new key.
func TestBuildLocalRecord_KeyIgnoresRunTime(t *testing.T) {
	t.Parallel()
	row := manualcsv.Row{Index: 3, Header: []string{"a"}, Fields: []string{"1"}}
	build := func(r manualcsv.Row, at time.Time) string {
		t.Helper()
		rec, err := buildLocalRecord(r, "/inbox/x.csv", "x.csv", "scf:GOV-05", nil, at)
		if err != nil {
			t.Fatalf("buildLocalRecord: %v", err)
		}
		return rec.GetIdempotencyKey()
	}
	first := build(row, time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC))
	if got := build(row, time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)); got != first {
		t.Fatalf("re-run of an unchanged row changed the key: %q -> %q", first, got)
	}
	edited := manualcsv.Row{Index: 3, Header: []string{"a"}, Fields: []string{"2"}}
	if build(edited, time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)) == first {
		t.Fatal("an edited row kept its key")
	}
}

type fakePusher struct{ err error }

func (f fakePusher) Push(context.Context, *evidencev1.EvidenceRecord) (*evidencev1.EvidenceReceipt, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &evidencev1.EvidenceReceipt{}, nil
}

// pushRecord reads AlreadyExists (ingest's answer to a reused key with a
// different hash) as "already ingested", including through the SDK's %w
// wrapping, and passes every other error through.
func TestPushRecord(t *testing.T) {
	t.Parallel()
	exists := status.Error(codes.AlreadyExists, "ingest: idempotency_key reused with different content")
	invalid := status.Error(codes.InvalidArgument, "payload invalid")
	cases := []struct {
		name     string
		err      error
		wantHeld bool
		wantErr  error
	}{
		{"accepted", nil, false, nil},
		{"already_exists", exists, true, nil},
		{"already_exists_wrapped", fmt.Errorf("sdk: push: %w", exists), true, nil},
		{"other_error", invalid, false, invalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			held, err := pushRecord(context.Background(), fakePusher{err: tc.err}, &evidencev1.EvidenceRecord{})
			if held != tc.wantHeld {
				t.Errorf("held = %v; want %v", held, tc.wantHeld)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("err = %v; want %v", err, tc.wantErr)
			}
		})
	}
}
