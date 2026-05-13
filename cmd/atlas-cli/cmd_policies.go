// Slice 022 — policy seed-stock subcommand.
//
// Loads the 5 stock policies from a local directory (default
// policies/stock) and INSERTs them as draft rows under a supplied
// tenant via direct Postgres connection. Mirrors the slice-007
// `catalog import-soc2` shape (database operation, not API operation;
// runs once per fresh deploy).
//
// Anti-criterion P0: the loader rejects a stock directory that does NOT
// contain exactly 5 markdown files (the "policy template libraries
// dressed as a feature" constitutional anti-pattern).

package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/internal/policy/seed"
)

func newPolicyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "policy",
		Short: "policy library operations (seed-stock, list)",
	}
	cmd.AddCommand(newPolicySeedStockCmd())
	return cmd
}

func newPolicySeedStockCmd() *cobra.Command {
	var (
		dsn       string
		tenantID  string
		stockDir  string
	)
	cmd := &cobra.Command{
		Use:   "seed-stock",
		Short: "seed the 5 stock policies as draft rows for a tenant",
		Long: `Loads the 5 bundled stock policies (Information Security, Access Control,
Vendor Management, Incident Response, Change Management) from
policies/stock/*.md and INSERTs them as 'draft' rows under the supplied
tenant. Mirrors slice 007's SOC 2 importer pattern.

Stock policies ship as community_draft attribution. They are intended as
starting templates -- the tenant admin reviews, edits, submits for
review, and approves before publish. The HITL review for the bundled
text itself lives at docs/audit-log/stock-policies-review.md.

The stock directory MUST contain exactly 5 markdown files (anti-criterion
P0: 5 high-signal policies, not 50 placeholders).`,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if dsn == "" {
				dsn = os.Getenv("DATABASE_URL")
			}
			if dsn == "" {
				return fmt.Errorf("--dsn or DATABASE_URL is required (use atlas_app role)")
			}
			if tenantID == "" {
				return fmt.Errorf("--tenant-id is required")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			tenant, err := uuid.Parse(tenantID)
			if err != nil {
				return fmt.Errorf("--tenant-id must be a UUID: %w", err)
			}
			policies, err := seed.LoadFromFS(os.DirFS("."), stockDir)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			pool, err := pgxpool.New(ctx, dsn)
			if err != nil {
				return fmt.Errorf("pgxpool: %w", err)
			}
			defer pool.Close()

			resolver := seed.NewSQLAnchorResolver(pool)
			report, err := seed.Seed(ctx, pool, tenant, policies, resolver)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "seeded %d stock policies under tenant %s\n", len(report.Loaded), tenant.String())
			for _, p := range report.Loaded {
				marker := ""
				if p.OrphanWarning {
					marker = " [orphan_policy warning]"
				}
				fmt.Fprintf(out, "  - %s (linked controls: %d)%s\n", p.Title, p.LinkedControlCount, marker)
			}
			if len(report.MissingAnchors) > 0 {
				fmt.Fprintf(out, "missing SCF anchors (control not yet imported): %v\n", report.MissingAnchors)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dsn, "dsn", "", "Postgres DSN (env: DATABASE_URL)")
	cmd.Flags().StringVar(&tenantID, "tenant-id", "", "target tenant UUID")
	cmd.Flags().StringVar(&stockDir, "stock-dir", "policies/stock", "directory holding the 5 stock policy markdown files")
	return cmd
}
