package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/internal/api/soc2import"
)

// newCatalogConvergeCrosswalksCmd wires `atlas-cli catalog
// converge-crosswalks <dir>`: re-import every bundled crosswalk in dir whose
// framework this install already imported, and skip the rest (#2014). The
// bootstrap runs it on every boot so an upgrade converges HIPAA, ISO, PCI
// DSS and NIST CSF the way it already converged SOC 2.
//
// Each file goes through the same import as `import-crosswalk`, so an edge
// the bundled file no longer lists is retired when that file's source wrote
// it (or it has no recorded source). Every retired edge is printed. A file
// that fails to import is reported and the rest still run; the command then
// exits non-zero, and the bootstrap logs that as a warning without failing.
func newCatalogConvergeCrosswalksCmd() *cobra.Command {
	var dsn string
	var exclude []string
	cmd := &cobra.Command{
		Use:   "converge-crosswalks <dir>",
		Short: "re-import every crosswalk YAML in <dir> whose framework is already imported; skip the rest",
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
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			pool, err := pgxpool.New(ctx, dsn)
			if err != nil {
				return fmt.Errorf("pgxpool: %w", err)
			}
			defer pool.Close()

			results, err := soc2import.ConvergeImported(ctx, pool, args[0], exclude...)
			if err != nil {
				return err
			}
			return printConvergeResults(os.Stdout, os.Stderr, results)
		},
	}
	cmd.Flags().StringVar(&dsn, "dsn", "", "Postgres DSN (env: DATABASE_URL)")
	cmd.Flags().StringArrayVar(&exclude, "exclude", nil, "crosswalk file name in <dir> to leave out; repeatable")
	return cmd
}

// printConvergeResults writes one summary line per framework, every retired
// edge, and the import's warnings. It returns an error naming each file that
// failed, after printing the rest.
func printConvergeResults(stdout, stderr io.Writer, results []soc2import.ConvergeResult) error {
	var errs []error
	for _, r := range results {
		switch {
		case r.Err != nil:
			_, _ = fmt.Fprintf(stderr, "warning: converge %s: %v\n", r.File, r.Err)
			errs = append(errs, fmt.Errorf("%s: %w", r.File, r.Err))
			continue
		case r.Skipped:
			_, _ = fmt.Fprintf(stdout, "converge framework=%s file=%s skipped: not imported on this install\n", r.FrameworkSlug, r.File)
			continue
		}
		rep := r.Report
		_, _ = fmt.Fprintf(stdout, "converge framework=%s:%s file=%s edges created=%d updated=%d unchanged=%d retired=%d restored=%d kept=%d requirements kept=%d\n",
			r.FrameworkSlug, rep.FrameworkVersion, r.File,
			rep.EdgesCreated, rep.EdgesUpdated, rep.EdgesUnchanged,
			len(rep.EdgesRetired), len(rep.EdgesRestored), len(rep.EdgesUnlisted), len(rep.RequirementsDiffered))
		for _, re := range rep.EdgesRetired {
			_, _ = fmt.Fprintf(stdout, "converge framework=%s edge retired %s->%s: no longer in %s (source=%s source_attribution=%s mapping_tier=%s)\n",
				r.FrameworkSlug, re.RequirementCode, re.SCFAnchor, r.File, re.Source, re.Attribution, re.Tier)
		}
		for _, ce := range rep.EdgesCarriedRetired {
			_, _ = fmt.Fprintf(stdout, "converge framework=%s edge retired %s->%s: carried by an SCF edge remap from an edge no longer in %s (source=%s mapping_tier=%s)\n",
				r.FrameworkSlug, ce.RequirementCode, ce.SCFAnchor, r.File, ce.Source, ce.Tier)
		}
		for _, ce := range rep.EdgesCarriedRestored {
			_, _ = fmt.Fprintf(stdout, "converge framework=%s edge restored %s->%s: carried by an SCF edge remap from an edge listed again in %s (source=%s mapping_tier=%s)\n",
				r.FrameworkSlug, ce.RequirementCode, ce.SCFAnchor, r.File, ce.Source, ce.Tier)
		}
		for _, ce := range rep.EdgesCarriedSynced {
			_, _ = fmt.Fprintf(stdout, "converge framework=%s edge updated %s->%s: carried by an SCF edge remap, content set from %s (source=%s source_attribution=%s mapping_tier=%s)\n",
				r.FrameworkSlug, ce.RequirementCode, ce.SCFAnchor, r.File, ce.Source, ce.Attribution, ce.Tier)
		}
		for _, ce := range rep.EdgesCarriedHeldBack {
			_, _ = fmt.Fprintf(stdout, "converge framework=%s edge kept %s->%s: carried by an SCF edge remap, content differs from %s but is held back (reason=%s source=%s source_attribution=%s mapping_tier=%s)\n",
				r.FrameworkSlug, ce.RequirementCode, ce.SCFAnchor, r.File, ce.Reason, ce.Source, ce.Attribution, ce.Tier)
		}
		for _, w := range rep.Warnings {
			_, _ = fmt.Fprintf(stderr, "warning: converge framework=%s: %s\n", r.FrameworkSlug, w)
		}
		printDifferedWarnings(stderr, rep.FrameworkVersion, rep.RequirementsDiffered, rep.RequirementsDifferedFields)
		if w := rep.LegacyPointsOfFocusWarning(); w != "" {
			_, _ = fmt.Fprintf(stderr, "warning: converge framework=%s: %s\n", r.FrameworkSlug, w)
		}
	}
	return errors.Join(errs...)
}
