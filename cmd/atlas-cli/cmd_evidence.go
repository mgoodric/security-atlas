package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
)

type evidencePushFlags struct {
	kind           string
	control        string
	scopeJSON      string
	observedAt     string
	resultStr      string
	payloadRef     string
	idempotencyKey string
	schemaVersion  string
	actorType      string
	actorID        string
	sessionID      string
	payloadURI     string
}

func newEvidenceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "evidence",
		Short: "evidence-ledger operations",
	}
	cmd.AddCommand(newEvidencePushCmd())
	cmd.AddCommand(newEvidenceVerifyCmd())
	return cmd
}

func newEvidencePushCmd() *cobra.Command {
	var f evidencePushFlags

	cmd := &cobra.Command{
		Use:   "push",
		Short: "push one evidence record to the ledger",
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if err := validatePushFlags(&f); err != nil {
				return err
			}
			return resolveCommon()
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			record, err := buildRecord(&f)
			if err != nil {
				return err
			}
			client, err := newSDKClient()
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			receipt, err := client.Push(ctx, record)
			if err != nil {
				return err
			}
			b, err := protojson.MarshalOptions{Multiline: true, Indent: "  "}.Marshal(receipt)
			if err != nil {
				return fmt.Errorf("marshal receipt: %w", err)
			}
			fmt.Println(string(b))
			return nil
		},
	}

	cmd.Flags().StringVar(&f.kind, "kind", "", "evidence_kind (e.g., sast.scan_result.v1) [required]")
	cmd.Flags().StringVar(&f.schemaVersion, "schema-version", "1.0.0", "evidence_kind schema version")
	cmd.Flags().StringVar(&f.control, "control", "", "control identifier [required]")
	cmd.Flags().StringVar(&f.scopeJSON, "scope", "", `JSON scope, e.g. '{"environment":"prod","cloud_account":"aws:111122223333"}' [required]`)
	cmd.Flags().StringVar(&f.observedAt, "observed-at", "", "RFC3339 timestamp when the source observed reality [required]")
	cmd.Flags().StringVar(&f.resultStr, "result", "", "pass | fail | na | inconclusive [required]")
	cmd.Flags().StringVar(&f.payloadRef, "payload", "", "JSON payload (literal or @path/to/file.json) [required]")
	cmd.Flags().StringVar(&f.idempotencyKey, "idempotency-key", "", "idempotency key [required]")
	cmd.Flags().StringVar(&f.actorType, "actor-type", "service_account", "source actor type")
	cmd.Flags().StringVar(&f.actorID, "actor-id", "", "source actor identifier [required]")
	cmd.Flags().StringVar(&f.sessionID, "session-id", "", "optional session reference")
	cmd.Flags().StringVar(&f.payloadURI, "payload-uri", "", "optional object-storage URI for large artifacts")
	return cmd
}

func validatePushFlags(f *evidencePushFlags) error {
	missing := []string{}
	if f.kind == "" {
		missing = append(missing, "--kind")
	}
	if f.control == "" {
		missing = append(missing, "--control")
	}
	if f.scopeJSON == "" {
		missing = append(missing, "--scope")
	}
	if f.observedAt == "" {
		missing = append(missing, "--observed-at")
	}
	if f.resultStr == "" {
		missing = append(missing, "--result")
	}
	if f.payloadRef == "" {
		missing = append(missing, "--payload")
	}
	if f.idempotencyKey == "" {
		missing = append(missing, "--idempotency-key")
	}
	if f.actorID == "" {
		missing = append(missing, "--actor-id")
	}
	if len(missing) > 0 {
		return fmt.Errorf("required flag(s) missing: %s", strings.Join(missing, ", "))
	}
	if _, ok := parseResult(f.resultStr); !ok {
		return fmt.Errorf("--result must be one of: pass | fail | na | inconclusive")
	}
	return nil
}

func buildRecord(f *evidencePushFlags) (*evidencev1.EvidenceRecord, error) {
	observed, err := time.Parse(time.RFC3339, f.observedAt)
	if err != nil {
		return nil, fmt.Errorf("--observed-at %q: %w", f.observedAt, err)
	}

	scopeMap, err := parseScope(f.scopeJSON)
	if err != nil {
		return nil, err
	}
	scope := make([]*evidencev1.ScopeDimension, 0, len(scopeMap))
	for k, v := range scopeMap {
		scope = append(scope, &evidencev1.ScopeDimension{Key: k, Values: []string{v}})
	}

	payloadStruct, err := parsePayload(f.payloadRef)
	if err != nil {
		return nil, err
	}

	result, _ := parseResult(f.resultStr)

	rec := &evidencev1.EvidenceRecord{
		IdempotencyKey:    f.idempotencyKey,
		EvidenceKind:      f.kind,
		SchemaVersion:     f.schemaVersion,
		ControlId:         f.control,
		Scope:             scope,
		ObservedAt:        timestamppb.New(observed),
		Result:            result,
		Payload:           payloadStruct,
		SourceAttribution: &evidencev1.SourceAttribution{ActorType: f.actorType, ActorId: f.actorID, SessionId: f.sessionID},
	}
	if f.payloadURI != "" {
		rec.PayloadUri = &f.payloadURI
	}
	return rec, nil
}

func parseScope(s string) (map[string]string, error) {
	if s == "" {
		return nil, errors.New("empty scope")
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, fmt.Errorf("--scope: %w", err)
	}
	if len(m) == 0 {
		return nil, errors.New("--scope: at least one key=value entry required")
	}
	return m, nil
}

func parsePayload(ref string) (*structpb.Struct, error) {
	body := ref
	if strings.HasPrefix(ref, "@") {
		data, err := os.ReadFile(ref[1:])
		if err != nil {
			return nil, fmt.Errorf("--payload @%s: %w", ref[1:], err)
		}
		body = string(data)
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return nil, fmt.Errorf("--payload: %w", err)
	}
	s, err := structpb.NewStruct(raw)
	if err != nil {
		return nil, fmt.Errorf("--payload to struct: %w", err)
	}
	return s, nil
}

func parseResult(s string) (evidencev1.Result, bool) {
	switch strings.ToLower(s) {
	case "pass":
		return evidencev1.Result_RESULT_PASS, true
	case "fail":
		return evidencev1.Result_RESULT_FAIL, true
	case "na":
		return evidencev1.Result_RESULT_NA, true
	case "inconclusive":
		return evidencev1.Result_RESULT_INCONCLUSIVE, true
	}
	return evidencev1.Result_RESULT_UNSPECIFIED, false
}
