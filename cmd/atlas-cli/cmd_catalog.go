package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	cmd.AddCommand(newCatalogImportCrosswalkCmd())
	return cmd
}

func newCatalogImportSCFCmd() *cobra.Command {
	var dsn string
	var releaseVersion string
	var releaseDate string
	cmd := &cobra.Command{
		Use:   "import-scf <path>",
		Short: "import the SCF catalog (JSON, or the published SCF .xlsx workbook) into Postgres",
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
			var cat *scfimport.Catalog
			var err error
			if strings.EqualFold(filepath.Ext(path), ".xlsx") {
				cat, err = scfimport.LoadWorkbook(path, scfimport.WorkbookOptions{
					ReleaseVersion: releaseVersion,
					ReleaseDate:    releaseDate,
				})
			} else {
				if releaseVersion != "" || releaseDate != "" {
					return fmt.Errorf("--release-version and --release-date apply to .xlsx workbooks only; a JSON catalog carries its own")
				}
				cat, err = scfimport.Load(path)
			}
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
	cmd.Flags().StringVar(&releaseVersion, "release-version", "", "release version override for .xlsx workbooks (default: from the \"SCF <release>\" sheet name)")
	cmd.Flags().StringVar(&releaseDate, "release-date", "", "release date (YYYY-MM-DD) for .xlsx workbooks, which do not carry one")
	return cmd
}

// newCatalogImportCrosswalkCmd wires `atlas-cli catalog import-crosswalk
// <path>`. Framework-agnostic crosswalk YAML and SCF-format XLSX workbooks are
// loaded, validated, and applied against the DB at --dsn (DATABASE_URL by
// default). Idempotent on re-runs. The same command imports any framework's
// requirement-to-SCF-anchor crosswalk (SOC 2, ISO 27001:2022, PCI DSS, …).
//
// The legacy `import-soc2` name remains as an alias so existing operator
// runbooks keep working after the slice-438 generalization.
//
// The agent-authored DRAFT mapping files and SCF workbook imports land with
// `source_attribution: community_draft` — maintainers review/promote through
// the crosswalk tier workflow.
func newCatalogImportCrosswalkCmd() *cobra.Command {
	var dsn string
	var frameworkColumns []string
	var frameworkSlug string
	var frameworkName string
	var frameworkIssuer string
	var frameworkVersion string
	var releaseDate string
	cmd := &cobra.Command{
		Use:     "import-crosswalk <path>",
		Aliases: []string{"import-soc2"},
		Short:   "import a framework→SCF crosswalk YAML or SCF workbook into Postgres",
		Args:    cobra.ExactArgs(1),
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
			var cw *soc2import.Crosswalk
			var err error
			if strings.EqualFold(filepath.Ext(path), ".xlsx") {
				cw, err = soc2import.LoadSCFWorkbook(path, soc2import.SCFWorkbookOptions{
					FrameworkColumns: frameworkColumns,
					FrameworkSlug:    frameworkSlug,
					FrameworkName:    frameworkName,
					FrameworkIssuer:  frameworkIssuer,
					FrameworkVersion: frameworkVersion,
					ReleaseDate:      releaseDate,
				})
			} else {
				cw, err = soc2import.Load(path)
			}
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
			fmt.Printf("edges        created=%d updated=%d unchanged=%d skipped=%d pruned=%d\n",
				report.EdgesCreated, report.EdgesUpdated, report.EdgesUnchanged, len(report.EdgesSkipped), len(report.EdgesPruned))
			for _, pr := range report.EdgesPruned {
				fmt.Printf("edge pruned %s->%s: no longer in the crosswalk (source_attribution=%s mapping_tier=%s)\n",
					pr.RequirementCode, pr.SCFAnchor, pr.Attribution, pr.Tier)
			}
			for _, un := range report.EdgesUnlisted {
				fmt.Printf("edge kept %s->%s: not in the crosswalk, but source_attribution=%s is another source's\n",
					un.RequirementCode, un.SCFAnchor, un.Attribution)
			}
			for _, sk := range report.EdgesSkipped {
				fmt.Printf("edge skipped %s->%s: existing source_attribution=%s outranks incoming %s\n",
					sk.RequirementCode, sk.SCFAnchor, sk.ExistingAttribution, sk.IncomingAttribution)
			}
			for _, up := range report.EdgesUpgradedOntoRejected {
				fmt.Printf("edge upgraded onto rejected tier %s->%s: source_attribution %s -> %s; mapping_tier stays rejected\n",
					up.RequirementCode, up.SCFAnchor, up.PreviousAttribution, up.IncomingAttribution)
			}
			for _, rs := range report.EdgesTierReseeded {
				fmt.Printf("edge tier re-seeded %s->%s: content changed; mapping_tier %s -> %s\n",
					rs.RequirementCode, rs.SCFAnchor, rs.PreviousTier, rs.NewTier)
			}
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
	cmd.Flags().StringArrayVar(&frameworkColumns, "framework-column", nil, "SCF workbook column header to import, as HEADER or HEADER=PREFIX to prefix its requirement codes; repeat to combine columns into one framework (required when an .xlsx has multiple framework columns)")
	cmd.Flags().StringVar(&frameworkSlug, "framework-slug", "", "framework slug override for SCF workbook imports (required when the first framework column has no Focal Documents metadata)")
	cmd.Flags().StringVar(&frameworkName, "framework-name", "", "framework name override for SCF workbook imports")
	cmd.Flags().StringVar(&frameworkIssuer, "framework-issuer", "", "framework issuer override for SCF workbook imports")
	cmd.Flags().StringVar(&frameworkVersion, "framework-version", "", "framework version override for SCF workbook imports")
	cmd.Flags().StringVar(&releaseDate, "release-date", "", "framework release date override (YYYY-MM-DD) for SCF workbook imports")
	return cmd
}
