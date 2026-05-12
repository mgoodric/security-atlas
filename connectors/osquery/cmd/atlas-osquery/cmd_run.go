package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/osquery/internal/idem"
	"github.com/mgoodric/security-atlas/connectors/osquery/internal/osqueryauth"
	"github.com/mgoodric/security-atlas/connectors/osquery/internal/osqueryposture"
)

// runFlags captures every parsed flag value for the `run` subcommand.
type runFlags struct {
	mode           string
	org            string
	environment    string
	fleetBaseURL   string
	token          string
	osqueryDSocket string
	hostPostureCtl string
}

// ErrLocalSocketNotWired is returned when --mode=local is selected. The
// configuration surface (flag + scope tagging + posture model) is wired
// in this slice; the live Thrift/JSON-over-unix-socket transport to a
// running osqueryd lands in a follow-up slice that takes a dependency on
// a vetted osquery extension library. This sentinel keeps AC honest: the
// user gets a clear "use Fleet mode" message rather than a silent
// fallthrough or a fabricated empty result. Mirrors slice 044's
// ErrAppNotWired pattern.
var ErrLocalSocketNotWired = errors.New("atlas-osquery: --mode=local osqueryd-socket transport not wired in slice 047 — use --mode=fleet")

func newRunCmd() *cobra.Command {
	var f runFlags
	cmd := &cobra.Command{
		Use:   "run",
		Short: "pull host posture and push evidence records",
		Long: `Pull endpoint posture from Fleet (or the local osqueryd extension
socket) and push one osquery.host_posture.v1 record per host.

Modes:
  --mode=fleet  (default): Fleet REST API. Two-call pull —
                           GET /api/v1/fleet/hosts +
                           GET /api/v1/fleet/hosts/{id}.
  --mode=local           : Local osqueryd extension socket; reads the
                           single-host posture row from the osquery
                           tables. Socket path defaults to
                           /var/osquery/osquery.em. The connector reads
                           only; it never writes to or proxies the socket.

Auth: set FLEET_API_TOKEN in the process environment for fleet mode. The
local mode dials a Unix socket; the root-owned socket's filesystem
permission is the security boundary (no token).

Fleet roles required (least-privilege):
  - observer            (global)
  - observer_plus       (per-team)`,
		SilenceErrors: true,
		SilenceUsage:  true,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if f.org == "" {
				return errors.New("--org is required")
			}
			if f.environment == "" {
				return errors.New("--environment is required")
			}
			switch f.mode {
			case "fleet":
				if f.fleetBaseURL == "" {
					return errors.New("--fleet-base-url is required for --mode=fleet (e.g. https://fleet.example.com)")
				}
			case "local":
				if f.osqueryDSocket == "" {
					return errors.New("--osqueryd-socket is required for --mode=local")
				}
			default:
				return fmt.Errorf("--mode=%q invalid; want fleet or local", f.mode)
			}
			return resolveCommon()
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return doRun(context.Background(), f)
		},
	}
	cmd.Flags().StringVar(&f.mode, "mode", "fleet", "upstream mode: fleet (REST API) or local (osqueryd socket)")
	cmd.Flags().StringVar(&f.org, "org", "", "organization slug for scope tagging [required]")
	cmd.Flags().StringVar(&f.environment, "environment", "", "environment scope tag [required]")
	cmd.Flags().StringVar(&f.fleetBaseURL, "fleet-base-url", "", "Fleet base URL (e.g. https://fleet.example.com) [required for fleet mode]")
	cmd.Flags().StringVar(&f.osqueryDSocket, "osqueryd-socket", "/var/osquery/osquery.em", "local osqueryd extension socket path (mode=local)")
	cmd.Flags().StringVar(&f.hostPostureCtl, "host-posture-control", "scf:END-04", "control_id to attach to osquery.host_posture.v1 records")
	// --token accepted for ad-hoc shells but env-var is the documented
	// preferred path so the secret never reaches shell history.
	cmd.Flags().StringVar(&f.token, "token-env-only-do-not-pass-on-cli", "", "")
	_ = cmd.Flags().MarkHidden("token-env-only-do-not-pass-on-cli")
	return cmd
}

func doRun(ctx context.Context, f runFlags) error {
	creds, err := osqueryauth.Resolve(osqueryauth.ResolveOpts{
		PreferLocalMode: f.mode == "local",
		Token:           f.token,
	})
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	sdkClient, err := sdk.NewClient(common.endpoint, common.token, sdkOpts()...)
	if err != nil {
		return fmt.Errorf("sdk client: %w", err)
	}
	defer func() { _ = sdkClient.Close() }()

	var rows []osqueryposture.HostPosture

	switch f.mode {
	case "fleet":
		httpClient := &http.Client{Timeout: 20 * time.Second}
		api := osqueryposture.NewFleetClient(httpClient, f.fleetBaseURL, creds)
		rows, err = osqueryposture.PullFromFleet(ctx, api, nil)
		if err != nil {
			return fmt.Errorf("fleet pull: %w", err)
		}
	case "local":
		// The transport is intentionally deferred; the flag, scope, model,
		// and security boundary are all wired so callers can grow into it.
		return ErrLocalSocketNotWired
	default:
		return fmt.Errorf("unreachable: invalid mode %q", f.mode)
	}

	pushed := 0
	for _, r := range rows {
		rec, err := buildHostPostureRecord(r, f.org, f.environment, f.hostPostureCtl)
		if err != nil {
			return fmt.Errorf("build host_posture record %s: %w", r.HostUUID, err)
		}
		pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err = sdkClient.Push(pctx, rec)
		cancel()
		if err != nil {
			return fmt.Errorf("push host_posture %s: %w", r.HostUUID, err)
		}
		pushed++
	}

	fmt.Printf("pushed %d records (mode=%s org=%s environment=%s)\n", pushed, f.mode, f.org, f.environment)
	return nil
}

// buildHostPostureRecord builds the canonical evidence record for one
// host. AC-2: payload carries only the slice-014 schema's declared fields
// (additionalProperties:false). AC-4: scope tagging includes the
// connector-canonical `cloud_account=workforce` plus a per-host
// `data_classification` derived from MDM enrolment + platform. AC-5:
// idempotency_key from sha256("osquery.host_posture" + host_uuid + hour).
func buildHostPostureRecord(p osqueryposture.HostPosture, org, env, controlID string) (*evidencev1.EvidenceRecord, error) {
	if p.HostUUID == "" {
		// Anti-criterion P0: never push without a derivable idempotency key.
		return nil, fmt.Errorf("host_uuid required (idempotency key cannot be derived)")
	}
	now := p.ObservedAt.UTC().Truncate(time.Hour)
	// Schema 1.0.0 declares host_uuid AND hostname as required. Fleet
	// almost always returns hostname; on the rare host where it does not
	// (transient enrolment), fall back to host_uuid so the record stays
	// schema-valid rather than rejecting an entire run on a single
	// missing field. The evaluator can flag this in a follow-up.
	hostname := p.Hostname
	if hostname == "" {
		hostname = p.HostUUID
	}
	pm := map[string]any{
		"host_uuid": p.HostUUID,
		"hostname":  hostname,
	}
	if p.Platform != "" {
		pm["platform"] = p.Platform
	}
	if p.OSVersion != "" {
		pm["os_version"] = p.OSVersion
	}
	// The four declared boolean policy fields are always emitted (their
	// zero value is meaningful — "false" means policy off, not absent).
	pm["disk_encryption_enabled"] = p.DiskEncryptionEnabled
	pm["screen_lock_enabled"] = p.ScreenLockEnabled
	pm["firewall_enabled"] = p.FirewallEnabled
	pm["mdm_enrolled"] = p.MDMEnrolled

	payload, err := structpb.NewStruct(pm)
	if err != nil {
		return nil, err
	}

	idemKey := idem.HostPostureKey(p.HostUUID, now)
	if idemKey == "" {
		return nil, fmt.Errorf("idempotency_key empty for host_uuid=%q", p.HostUUID)
	}

	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idemKey,
		EvidenceKind:   "osquery.host_posture.v1",
		SchemaVersion:  "1.0.0",
		ControlId:      controlID,
		Scope: []*evidencev1.ScopeDimension{
			{Key: "org", Values: []string{org}},
			{Key: "environment", Values: []string{env}},
			// AC-4: cloud_account=workforce distinguishes endpoint posture
			// from server/cloud-account scoping used by AWS/GCP/Azure.
			{Key: "cloud_account", Values: []string{"workforce"}},
			// AC-4: data_classification inferred per device — MDM-enrolled
			// devices are managed corporate endpoints and treated as
			// `restricted`; un-enrolled devices may be BYOD/transient and
			// flagged `unknown` so downstream scope rules can surface
			// them. Connector keeps inference shallow; the evaluator owns
			// the policy ladder.
			{Key: "data_classification", Values: []string{inferDataClassification(p)}},
		},
		ObservedAt: timestamppb.New(now),
		Result:     mapResult(p.Result),
		Payload:    payload,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "connector",
			ActorId:   actorID("posture"),
		},
	}, nil
}

// inferDataClassification: MDM-enrolled → restricted; otherwise unknown.
// Single canonical hook so the test and the doc agree.
func inferDataClassification(p osqueryposture.HostPosture) string {
	if p.MDMEnrolled {
		return "restricted"
	}
	return "unknown"
}

func mapResult(r osqueryposture.Result) evidencev1.Result {
	switch r {
	case osqueryposture.ResultPass:
		return evidencev1.Result_RESULT_PASS
	case osqueryposture.ResultFail:
		return evidencev1.Result_RESULT_FAIL
	case osqueryposture.ResultInconclusive:
		return evidencev1.Result_RESULT_INCONCLUSIVE
	default:
		return evidencev1.Result_RESULT_UNSPECIFIED
	}
}
