package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/internal/api/scfimport"
)

func newCatalogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "catalog",
		Short: "control catalog operations (SCF import, listings)",
	}
	cmd.AddCommand(newCatalogImportSCFCmd())
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
