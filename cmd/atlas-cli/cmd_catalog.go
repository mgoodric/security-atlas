package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/internal/api/scfimport"
	"github.com/mgoodric/security-atlas/internal/api/soc2import"
)

func newCatalogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "catalog",
		Short: "control catalog operations (SCF + crosswalk imports, listings)",
	}
	cmd.AddCommand(newCatalogImportSCFCmd())
	cmd.AddCommand(newCatalogImportSOC2Cmd())
	return cmd
}

func newCatalogImportSCFCmd() *cobra.Command {
	var dsn string
	cmd := &cobra.Command{
		Use:   "import-scf <path>",
		Short: "import the SCF JSON catalog into Postgres",
		Args:  cobra.ExactArgs(1),
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if dsn == "" {
				dsn = os.Getenv("DATABASE_URL")
			}
			if dsn == "" {
				return fmt.Errorf("--dsn or DATABASE_URL is required (use atlas_migrate role for write access)")
			}
			return nil
		},
		RunE: func(_ *cobra.Command, args []string) error {
			path := args[0]
			cat, err := scfimport.Load(path)
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			pool, err := pgxpool.New(ctx, dsn)
			if err != nil {
				return fmt.Errorf("pgxpool: %w", err)
			}
			defer pool.Close()

			report, err := scfimport.Import(ctx, pool, cat)
			if err != nil {
				return err
			}
			fmt.Printf("release_version=%s framework_version_id=%s new_version=%v\n",
				report.ReleaseVersion, report.FrameworkVersionID, report.IsNewVersion)
			fmt.Printf("created=%d updated=%d unchanged=%d\n",
				report.Created, report.Updated, report.Unchanged)
			return nil
		},
	}
	cmd.Flags().StringVar(&dsn, "dsn", "", "Postgres DSN (env: DATABASE_URL)")
	return cmd
}

// newCatalogImportSOC2Cmd wires `atlas-cli catalog import-soc2 <path>`.
// The crosswalk YAML at <path> is loaded, validated, and applied against
// the DB at --dsn (DATABASE_URL by default). Idempotent on re-runs.
//
// Per slice 007's HITL gate, the agent-authored mapping file ships with
// `source_attribution: community_draft` on every row — the orchestrator
// spot-checks before merge; the importer is the same machinery once an
// SCF-published crosswalk lands with `source_attribution: scf_official`.
func newCatalogImportSOC2Cmd() *cobra.Command {
	var dsn string
	cmd := &cobra.Command{
		Use:   "import-soc2 <path>",
		Short: "import a SOC 2 TSC crosswalk YAML into Postgres (slice 007)",
		Args:  cobra.ExactArgs(1),
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if dsn == "" {
				dsn = os.Getenv("DATABASE_URL")
			}
			if dsn == "" {
				return fmt.Errorf("--dsn or DATABASE_URL is required (use atlas_migrate role for write access)")
			}
			return nil
		},
		RunE: func(_ *cobra.Command, args []string) error {
			path := args[0]
			cw, err := soc2import.Load(path)
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			pool, err := pgxpool.New(ctx, dsn)
			if err != nil {
				return fmt.Errorf("pgxpool: %w", err)
			}
			defer pool.Close()

			report, err := soc2import.Import(ctx, pool, cw)
			if err != nil {
				return err
			}
			fmt.Printf("framework=%s:%s framework_version_id=%s new_version=%v\n",
				report.FrameworkSlug, report.FrameworkVersion, report.FrameworkVersionID, report.IsNewVersion)
			fmt.Printf("requirements created=%d updated=%d unchanged=%d\n",
				report.RequirementsCreated, report.RequirementsUpdated, report.RequirementsUnchanged)
			fmt.Printf("edges        created=%d updated=%d unchanged=%d\n",
				report.EdgesCreated, report.EdgesUpdated, report.EdgesUnchanged)
			keys := make([]string, 0, len(report.MappingsByAttribution))
			for k := range report.MappingsByAttribution {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Printf("edges by source_attribution[%s]=%d\n", k, report.MappingsByAttribution[k])
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dsn, "dsn", "", "Postgres DSN (env: DATABASE_URL)")
	return cmd
}
