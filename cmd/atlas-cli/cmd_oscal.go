// Slice 030 — oscal-export subcommand.
//
// Generates the OSCAL audit-handoff bundle (SSP + Assessment Plan +
// Assessment Results + POA&M) for a FROZEN AuditPeriod, round-trip
// validates every document via the Python compliance-trestle bridge,
// signs the bundle, and writes it to a directory.
//
// Like `policy seed-stock`, this is a database operation (it reads the
// frozen period's aggregate directly) rather than an API call — it runs
// against a Postgres DSN with an explicit tenant id. The OSCAL
// serialization is delegated to the Python oscal-bridge over gRPC; the
// --bridge-addr flag points at that sidecar.
//
// Constitutional enforcement (surfaced as command errors):
//   - invariant 10: a non-frozen period is rejected (oscal.ErrPeriodNotFrozen).
//   - AC-5 / P0: a signing failure aborts the export — no unsigned bundle.
//   - AC-6/AC-7 / P0: a compliance-trestle round-trip failure aborts the export.

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/internal/oscal"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func newOscalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "oscal-export",
		Short: "export the OSCAL audit-handoff bundle for a frozen audit period",
		Long: `Generates the OSCAL audit-handoff bundle for a FROZEN AuditPeriod:

  - system-security-plan.json   (SSP)
  - assessment-plan.json        (AP)
  - assessment-results.json     (AR)
  - poam.json                   (POA&M)
  - manifest.json               (member list + provenance + signature)

The bundle is round-trip validated through IBM compliance-trestle (via the
Python oscal-bridge sidecar) and signed before it is written. The export
REFUSES to run against a period that has not been frozen — the auditor's
view must draw only from evidence at or before the freeze horizon
(constitutional invariant 10).

Requires a running oscal-bridge (see oscal-bridge/README.md):

  python -m atlas_oscal_bridge.server --address 127.0.0.1:50070`,
		RunE: runOscalExport,
	}

	cmd.Flags().String("dsn", "", "Postgres DSN (atlas_app role); env DATABASE_URL_APP")
	cmd.Flags().String("tenant-id", "", "tenant UUID the audit period belongs to (required)")
	cmd.Flags().String("period-id", "", "frozen audit period UUID to export (required)")
	cmd.Flags().String("bridge-addr", "127.0.0.1:50070", "oscal-bridge gRPC address")
	cmd.Flags().String("out", "", "output directory for the bundle (required)")
	cmd.Flags().String("org-name", "", "organization name for the SSP org profile")
	cmd.Flags().String("system-name", "", "system name for the SSP system-characteristics")
	cmd.Flags().String("system-description", "", "system description / authorization-boundary summary")
	cmd.Flags().String("requested-by", "atlas-cli", "id recorded in the bundle manifest as the requester")

	return cmd
}

func runOscalExport(cmd *cobra.Command, _ []string) error {
	dsn, _ := cmd.Flags().GetString("dsn")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL_APP")
	}
	if dsn == "" {
		return fmt.Errorf("--dsn or DATABASE_URL_APP is required (use the atlas_app role)")
	}
	tenantStr, _ := cmd.Flags().GetString("tenant-id")
	if tenantStr == "" {
		return fmt.Errorf("--tenant-id is required")
	}
	if _, err := uuid.Parse(tenantStr); err != nil {
		return fmt.Errorf("--tenant-id must be a UUID: %w", err)
	}
	periodStr, _ := cmd.Flags().GetString("period-id")
	if periodStr == "" {
		return fmt.Errorf("--period-id is required")
	}
	periodID, err := uuid.Parse(periodStr)
	if err != nil {
		return fmt.Errorf("--period-id must be a UUID: %w", err)
	}
	outDir, _ := cmd.Flags().GetString("out")
	if outDir == "" {
		return fmt.Errorf("--out is required")
	}
	bridgeAddr, _ := cmd.Flags().GetString("bridge-addr")
	orgName, _ := cmd.Flags().GetString("org-name")
	systemName, _ := cmd.Flags().GetString("system-name")
	systemDesc, _ := cmd.Flags().GetString("system-description")
	requestedBy, _ := cmd.Flags().GetString("requested-by")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("pgxpool: %w", err)
	}
	defer pool.Close()

	bridge, err := oscal.DialBridge(bridgeAddr)
	if err != nil {
		return fmt.Errorf("connect to oscal-bridge at %s: %w", bridgeAddr, err)
	}
	defer func() { _ = bridge.Close() }()

	// The export bundle is signed. Without a configured persistent
	// signing key the CLI uses a fresh ephemeral ed25519 keypair — the
	// public key travels in the bundle manifest so the signature is
	// still verifiable by the auditor. (See the slice-030 decisions log:
	// cosign keyless + Fulcio is the v3 revisit item.)
	signer, err := oscal.NewEphemeralSigner()
	if err != nil {
		return fmt.Errorf("create signer: %w", err)
	}

	tenantCtx, err := tenancy.WithTenant(ctx, tenantStr)
	if err != nil {
		return fmt.Errorf("tenancy context: %w", err)
	}

	exporter := oscal.NewExporter(pool, bridge, signer)
	bundle, err := exporter.Export(tenantCtx, oscal.ExportInput{
		AuditPeriodID:     periodID,
		OrganizationName:  orgName,
		SystemName:        systemName,
		SystemDescription: systemDesc,
		RequestedBy:       requestedBy,
	})
	if err != nil {
		// Surface the constitutional-enforcement errors with a clear
		// operator-facing message.
		switch {
		case errors.Is(err, oscal.ErrPeriodNotFrozen):
			return fmt.Errorf("audit period %s is not frozen: freeze it before exporting "+
				"(constitutional invariant 10)", periodID)
		case errors.Is(err, oscal.ErrPeriodNotFound):
			return fmt.Errorf("audit period %s not found for tenant %s", periodID, tenantStr)
		case errors.Is(err, oscal.ErrRoundTripFailed):
			return fmt.Errorf("compliance-trestle round-trip validation failed; "+
				"the bundle was NOT written: %w", err)
		case errors.Is(err, oscal.ErrSigningFailed):
			return fmt.Errorf("bundle signing failed; the bundle was NOT written: %w", err)
		case errors.Is(err, oscal.ErrBridgeUnavailable):
			return fmt.Errorf("oscal-bridge unavailable at %s: %w", bridgeAddr, err)
		default:
			return fmt.Errorf("export: %w", err)
		}
	}

	manifestPath, err := bundle.WriteBundle(outDir)
	if err != nil {
		return fmt.Errorf("write bundle: %w", err)
	}

	fmt.Printf("OSCAL export bundle written to %s\n", outDir)
	fmt.Printf("  audit period:   %s (frozen at %s)\n", bundle.AuditPeriodID, bundle.FrozenAt)
	fmt.Printf("  OSCAL version:  %s\n", bundle.OSCALVersion)
	fmt.Printf("  members:        %d\n", len(bundle.Members))
	fmt.Printf("  signature:      %s (%s)\n", bundle.Signature.Algorithm, bundle.Signature.Digest)
	fmt.Printf("  manifest:       %s\n", manifestPath)
	return nil
}
