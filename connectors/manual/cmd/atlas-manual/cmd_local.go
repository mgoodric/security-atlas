package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/manual/internal/idem"
	"github.com/mgoodric/security-atlas/connectors/manual/internal/manualcsv"
)

// Default DoS caps for the local CSV parser. Overridable via flags so
// operators with one-off bigger CSVs can opt-in; defaults are deliberately
// conservative to fail loud on attacker-supplied inputs.
const (
	DefaultMaxRows       = 100_000
	DefaultMaxFieldBytes = 1 << 20 // 1 MiB
)

// maxRowPayloadBytes caps the inline payload bytes per row. Rows that
// exceed this cap are skipped with a warning (slice 036 redirect is a
// follow-up).
const maxRowPayloadBytes = 1 << 20 // 1 MiB

type localFlags struct {
	file          string
	controlID     string
	scope         []string
	maxRows       int
	maxFieldBytes int
}

func newLocalCmd() *cobra.Command {
	var f localFlags
	cmd := &cobra.Command{
		Use:           "local",
		Short:         "parse a local CSV file; emit one manual.upload.v1 record per row",
		SilenceErrors: true,
		SilenceUsage:  true,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if f.file == "" {
				return errors.New("--file is required")
			}
			if len(f.scope) == 0 {
				return errors.New("at least one --scope key=value pair is required (e.g. --scope environment=prod)")
			}
			return resolveCommon()
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return doLocal(context.Background(), f, os.Stderr)
		},
	}
	cmd.Flags().StringVar(&f.file, "file", "", "path to the CSV file to ingest [required]")
	cmd.Flags().StringVar(&f.controlID, "control-id", "scf:GOV-04", "control id to attach to each record")
	cmd.Flags().StringArrayVar(&f.scope, "scope", nil, "scope tag in key=value form (repeatable, at least one required)")
	cmd.Flags().IntVar(&f.maxRows, "max-rows", DefaultMaxRows, "maximum CSV data rows to accept (DoS guard)")
	cmd.Flags().IntVar(&f.maxFieldBytes, "max-field-bytes", DefaultMaxFieldBytes, "maximum bytes per CSV field (DoS guard)")
	return cmd
}

func doLocal(ctx context.Context, f localFlags, warn *os.File) error {
	file, err := os.Open(f.file)
	if err != nil {
		return fmt.Errorf("open csv: %w", err)
	}
	defer func() { _ = file.Close() }()

	rows, err := manualcsv.Parse(file, manualcsv.Limits{MaxRows: f.maxRows, MaxFieldBytes: f.maxFieldBytes})
	if err != nil {
		return fmt.Errorf("parse csv: %w", err)
	}

	scope, err := parseScope(f.scope)
	if err != nil {
		return err
	}

	client, err := sdk.NewClient(common.endpoint, common.token, sdkOpts()...)
	if err != nil {
		return fmt.Errorf("sdk client: %w", err)
	}
	defer func() { _ = client.Close() }()

	now := time.Now().UTC().Truncate(time.Hour)
	filename := filepath.Base(f.file)
	pushed, skipped := 0, 0
	for _, row := range rows {
		rec, err := buildLocalRecord(row, f.file, filename, f.controlID, scope, now)
		if errors.Is(err, errPayloadTooLarge) {
			_, _ = fmt.Fprintf(warn, "skipped row %d: payload exceeds %d bytes (slice 036 redirect not wired)\n", row.Index, maxRowPayloadBytes)
			skipped++
			continue
		}
		if err != nil {
			return fmt.Errorf("build record row=%d: %w", row.Index, err)
		}
		pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err = client.Push(pctx, rec)
		cancel()
		if err != nil {
			return fmt.Errorf("push row=%d: %w", row.Index, err)
		}
		pushed++
	}
	fmt.Printf("pushed %d records, skipped %d (file=%s)\n", pushed, skipped, filename)
	return nil
}

var errPayloadTooLarge = errors.New("manual: payload exceeds cap")

func buildLocalRecord(row manualcsv.Row, filePath, filename, controlID string, scope []*evidencev1.ScopeDimension, observedAt time.Time) (*evidencev1.EvidenceRecord, error) {
	// Encode the row as a JSON object keyed by header → field; base64 the
	// resulting bytes for the schema's payload property. The schema-required
	// fields (uploaded_by, filename, content_type) sit alongside the
	// payload_b64 in a single structpb.Struct.
	rowJSON, err := json.Marshal(rowAsObject(row))
	if err != nil {
		return nil, fmt.Errorf("marshal row: %w", err)
	}
	if len(rowJSON) > maxRowPayloadBytes {
		return nil, errPayloadTooLarge
	}
	payload, err := structpb.NewStruct(map[string]any{
		"uploaded_by":  actorID("local"),
		"filename":     filename,
		"content_type": "text/csv",
		"size_bytes":   float64(len(rowJSON)),
		"description":  fmt.Sprintf("row %d of %s", row.Index, filename),
		"row_index":    float64(row.Index),
		"payload_b64":  base64.StdEncoding.EncodeToString(rowJSON),
	})
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idem.LocalRowKey(filePath, row.Index, observedAt),
		EvidenceKind:   "manual.upload.v1",
		SchemaVersion:  "1.0.0",
		ControlId:      controlID,
		Scope:          scope,
		ObservedAt:     timestamppb.New(observedAt),
		Result:         evidencev1.Result_RESULT_INCONCLUSIVE,
		Payload:        payload,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "connector",
			ActorId:   actorID("local"),
		},
	}, nil
}

// rowAsObject pairs each header column with its row field. Falls back to
// numeric indices when there's no header (header-less CSVs).
func rowAsObject(row manualcsv.Row) map[string]string {
	out := map[string]string{}
	for i, f := range row.Fields {
		key := ""
		if i < len(row.Header) {
			key = row.Header[i]
		}
		if key == "" {
			key = fmt.Sprintf("col_%d", i)
		}
		out[key] = f
	}
	return out
}

// parseScope splits each --scope key=value pair into a ScopeDimension.
func parseScope(in []string) ([]*evidencev1.ScopeDimension, error) {
	out := make([]*evidencev1.ScopeDimension, 0, len(in))
	for _, s := range in {
		for i := 0; i < len(s); i++ {
			if s[i] == '=' {
				key, val := s[:i], s[i+1:]
				if key == "" || val == "" {
					return nil, fmt.Errorf("invalid --scope %q: expected key=value", s)
				}
				out = append(out, &evidencev1.ScopeDimension{Key: key, Values: []string{val}})
				goto next
			}
		}
		return nil, fmt.Errorf("invalid --scope %q: missing =", s)
	next:
	}
	return out, nil
}
