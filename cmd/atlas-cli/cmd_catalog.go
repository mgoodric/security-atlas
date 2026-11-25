package main

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	cmd.AddCommand(newCatalogConvergeCrosswalksCmd())
	cmd.AddCommand(newCatalogRemapCmd())
	return cmd
}

func newCatalogImportSCFCmd() *cobra.Command {
	var dsn string
	var releaseVersion string
	var releaseDate string
	var keepCurrent bool
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

			report, err := scfimport.ImportWithOptions(ctx, pool, cat, scfimport.Options{KeepCurrent: keepCurrent})
			if err != nil {
				return err
			}
			fmt.Printf("release_version=%s framework_version_id=%s new_version=%v status=%s\n",
				report.ReleaseVersion, report.FrameworkVersionID, report.IsNewVersion, report.Status)
			fmt.Printf("created=%d updated=%d unchanged=%d\n",
				report.Created, report.Updated, report.Unchanged)

			// #1971 D3: after a new release, move controls whose anchor kept
			// its code and title; list the rest for re-anchoring.
			rebind, err := scfimport.RebindControls(ctx, pool)
			if err != nil {
				return err
			}
			printRebindReport(os.Stdout, rebind)

			// #1777: queue every catalog crosswalk edge left on an older
			// release's anchor for a super-admin's review; #2315: carry the
			// ones whose anchor kept its code and title forward.
			edges, err := scfimport.QueueEdgeRemapProposals(ctx, pool)
			if err != nil {
				return err
			}
			printEdgeRemapCounts(os.Stdout, edges.Queued)
			fmt.Printf("crosswalk_edges carried_forward=%d closed=%d\n", edges.Carried, edges.Closed)

			// #1804: queue every tenant's active overlay edge left on an
			// older release's anchor for that tenant's writers; #2554:
			// carry the ones whose anchor kept its code and title forward,
			// in each tenant's own context.
			overlays, err := scfimport.QueueOverlayRemapProposals(ctx, pool)
			if err != nil {
				return err
			}
			printOverlayRemapCounts(os.Stdout, overlays.Queued)
			// #2568: a tenant whose carry failed is named and the rest
			// still carry; the exit is non-zero so the operator re-runs.
			return printOverlayCarryReport(os.Stdout, overlays)
		},
	}
	cmd.Flags().StringVar(&dsn, "dsn", "", "Postgres DSN (env: DATABASE_URL)")
	cmd.Flags().StringVar(&releaseVersion, "release-version", "", "release version override for .xlsx workbooks (default: from the \"SCF <release>\" sheet name)")
	cmd.Flags().StringVar(&releaseDate, "release-date", "", "release date (YYYY-MM-DD) for .xlsx workbooks, which do not carry one")
	// #3061: the docker bootstrap loads the bundled sample this way beside
	// an operator's own release, so the stock content's pin resolves.
	cmd.Flags().BoolVar(&keepCurrent, "keep-current", false, "load a new release as legacy when another SCF release is current, instead of making it current; a re-import never promotes it, so use it only for a release that must stay legacy (the bootstrap's bundled sample)")
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
	var titlesPath string
	var allowCodeTitles bool
	var scfRelease string
	var strict bool
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
					TitlesPath:       titlesPath,
					AllowCodeTitles:  allowCodeTitles,
					SCFRelease:       scfRelease,
				})
			} else {
				cw, err = soc2import.Load(path)
				switch {
				case err != nil, scfRelease == "":
				case cw.SCFRelease == "":
					cw.SCFRelease = scfRelease
				case cw.SCFRelease != scfRelease:
					err = fmt.Errorf("crosswalk: --scf-release %q contradicts the file's scf_release %q", scfRelease, cw.SCFRelease)
				}
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
			if report.SCFRelease != "" {
				fmt.Printf("scf_release=%s\n", report.SCFRelease)
			}
			for _, w := range report.Warnings {
				fmt.Printf("warning: %s\n", w)
			}
			fmt.Printf("requirements created=%d updated=%d unchanged=%d differed=%d\n",
				report.RequirementsCreated, report.RequirementsUpdated, report.RequirementsUnchanged, len(report.RequirementsDiffered))
			if n := soc2import.CodeOnlyTitleCount(cw); n > 0 {
				fmt.Printf("requirements titled with their own code=%d\n", n)
			}
			printDifferedWarnings(os.Stderr, report.FrameworkVersion, report.RequirementsDiffered, report.RequirementsDifferedFields)
			if w := report.LegacyPointsOfFocusWarning(); w != "" {
				_, _ = fmt.Fprintf(os.Stderr, "warning: %s\n", w)
			}
			for _, line := range cw.PointsOfFocusRollUp.Summary() {
				fmt.Println(line)
			}
			fmt.Printf("edges        created=%d updated=%d unchanged=%d skipped=%d retired=%d restored=%d\n",
				report.EdgesCreated, report.EdgesUpdated, report.EdgesUnchanged, len(report.EdgesSkipped), len(report.EdgesRetired), len(report.EdgesRestored))
			for _, re := range report.EdgesRetired {
				fmt.Printf("edge retired %s->%s: no longer in the crosswalk (source=%s source_attribution=%s mapping_tier=%s)\n",
					re.RequirementCode, re.SCFAnchor, re.Source, re.Attribution, re.Tier)
			}
			retired := report.RetiredBySource()
			sources := make([]string, 0, len(retired))
			for s := range retired {
				sources = append(sources, s)
			}
			sort.Strings(sources)
			for _, s := range sources {
				fmt.Printf("edges retired by source[%s]=%d\n", s, retired[s])
			}
			for _, rs := range report.EdgesRestored {
				fmt.Printf("edge restored %s->%s: listed again (mapping_tier=%s)\n",
					rs.RequirementCode, rs.SCFAnchor, rs.Tier)
			}
			for _, un := range report.EdgesUnlisted {
				fmt.Printf("edge kept %s->%s: not in the crosswalk, but written by another source (source=%s source_attribution=%s)\n",
					un.RequirementCode, un.SCFAnchor, un.Source, un.Attribution)
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
			return errors.Join(
				strictDifferedErr(strict, report.RequirementsDiffered),
				strictLegacyPointsOfFocusErr(strict, report.LegacyPointsOfFocus),
			)
		},
	}
	cmd.Flags().StringVar(&dsn, "dsn", "", "Postgres DSN (env: DATABASE_URL)")
	cmd.Flags().StringArrayVar(&frameworkColumns, "framework-column", nil, "SCF workbook column header to import, as HEADER or HEADER=PREFIX to prefix its requirement codes; repeat to combine columns into one framework (required when an .xlsx has multiple framework columns)")
	cmd.Flags().StringVar(&frameworkSlug, "framework-slug", "", "framework slug override for SCF workbook imports (required when the first framework column has no Focal Documents metadata)")
	cmd.Flags().StringVar(&frameworkName, "framework-name", "", "framework name override for SCF workbook imports")
	cmd.Flags().StringVar(&frameworkIssuer, "framework-issuer", "", "framework issuer override for SCF workbook imports")
	cmd.Flags().StringVar(&frameworkVersion, "framework-version", "", "framework version override for SCF workbook imports")
	cmd.Flags().StringVar(&releaseDate, "release-date", "", "framework release date override (YYYY-MM-DD) for SCF workbook imports")
	cmd.Flags().StringVar(&titlesPath, "titles", "", "crosswalk-format YAML whose requirement titles and bodies override the bundled crosswalk's for SCF workbook imports (its mappings are ignored)")
	cmd.Flags().BoolVar(&allowCodeTitles, "allow-code-titles", false, "let SCF workbook requirements that no title source covers import titled with their code")
	cmd.Flags().StringVar(&scfRelease, "scf-release", "", "SCF release the crosswalk's SCF codes belong to; the only way to declare it for an OLIR STRM workbook, which names none (a wide workbook takes it from its \"SCF <release>\" sheet, a YAML file from scf_release)")
	cmd.Flags().BoolVar(&strict, "strict", false, "exit non-zero when a frozen framework version kept its stored text for a requirement whose incoming title, body or points of focus differ, or when the target version keeps point-of-focus requirement rows from before the roll-up")
	return cmd
}

// printDifferedWarnings writes one warning per requirement whose incoming
// title, body or points of focus differed from a frozen framework version's
// stored text (#2008), naming the fields that differed (#2026). It goes to
// stderr so scripted imports and the bootstrap log surface it instead of
// burying it in the stdout summary.
func printDifferedWarnings(w io.Writer, frameworkVersion string, codes []string, fields map[string][]string) {
	for _, code := range codes {
		_, _ = fmt.Fprintf(w, "warning: requirement kept %s: incoming %s, but framework version %s is frozen; ship a new version to change it\n",
			code, differedPhrase(fields[code]), frameworkVersion)
	}
}

// differedPhrase renders the differed fields as "title differs", "title and
// body differ" or "title, body and points of focus differ". With none
// recorded it falls back to the pre-#2026 "title or body differs".
func differedPhrase(fields []string) string {
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = strings.ReplaceAll(f, "_", " ")
	}
	switch {
	case len(names) == 0:
		return "title or body differs"
	case len(names) == 1 && names[0] == "points of focus":
		return "points of focus differ"
	case len(names) == 1:
		return names[0] + " differs"
	default:
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1] + " differ"
	}
}

// strictLegacyPointsOfFocusErr fails a --strict import whose target version
// keeps point-of-focus requirement rows from before the roll-up (#2026). The
// import has already committed; the error changes only the exit status.
func strictLegacyPointsOfFocusErr(strict bool, codes []string) error {
	if !strict || len(codes) == 0 {
		return nil
	}
	return fmt.Errorf("crosswalk: --strict: %d point-of-focus requirement row(s) from before the roll-up still count toward coverage (the import committed); import into a new --framework-version: %s",
		len(codes), strings.Join(codes, ", "))
}

// strictDifferedErr fails a --strict import whose frozen framework version
// kept its stored text for any requirement (#2008). The import has already
// committed; the error changes only the exit status.
func strictDifferedErr(strict bool, codes []string) error {
	if !strict || len(codes) == 0 {
		return nil
	}
	return fmt.Errorf("crosswalk: --strict: %d requirement(s) kept their stored text because the framework version is frozen (the import committed): %s",
		len(codes), strings.Join(codes, ", "))
}

// printEdgeRemapCounts writes how many crosswalk-edge remap proposals
// (#1777) an SCF import queued, by class.
func printEdgeRemapCounts(w io.Writer, counts map[scfimport.RemapClass]int) {
	printRemapCounts(w, "edge_remap_proposals", counts)
}

// printOverlayRemapCounts writes how many overlay-edge remap proposals
// (#1804) an SCF import queued across every tenant, by class.
func printOverlayRemapCounts(w io.Writer, counts map[scfimport.RemapClass]int) {
	printRemapCounts(w, "overlay_remap_proposals", counts)
}

// printOverlayCarryReport writes the overlay carry-forward outcome (#2554),
// then one line per tenant whose carry failed (#2568). It returns an error
// when any tenant failed: those tenants' proposals are still open, and a
// re-run of the import carries them.
func printOverlayCarryReport(w io.Writer, r scfimport.OverlayRemapReport) error {
	_, _ = fmt.Fprintf(w, "overlay_edges carried_forward=%d closed=%d failed=%d\n", r.Carried, r.Closed, len(r.Failed))
	for _, f := range r.Failed {
		_, _ = fmt.Fprintf(w, "overlay carry failed tenant=%s: %v\n", f.TenantID, f.Err)
	}
	if len(r.Failed) > 0 {
		return fmt.Errorf("overlay carry-forward failed in %d tenant(s); their proposals stay open, re-run the import to carry them", len(r.Failed))
	}
	return nil
}

func printRemapCounts(w io.Writer, label string, counts map[scfimport.RemapClass]int) {
	_, _ = fmt.Fprint(w, label)
	for _, class := range scfimport.RemapClasses {
		_, _ = fmt.Fprintf(w, " %s=%d", class, counts[class])
	}
	_, _ = fmt.Fprintln(w)
}

// printRebindReport writes the control re-binding outcome of an SCF import
// (#1971 D3): a count, then one line per control left needing re-anchoring,
// so the bootstrap log names every control that lost framework coverage.
func printRebindReport(w io.Writer, r scfimport.RebindReport) {
	_, _ = fmt.Fprintf(w, "controls rebound=%d needs_reanchoring=%d pinned=%d superseded_mid_run=%d\n", len(r.Rebound), len(r.NeedsReanchoring), len(r.Pinned), r.SupersededMidRun)
	// #1775: the remap queue this run filled, by class.
	_, _ = fmt.Fprint(w, "remap_proposals")
	for _, class := range scfimport.RemapClasses {
		_, _ = fmt.Fprintf(w, " %s=%d", class, r.Proposals[class])
	}
	_, _ = fmt.Fprintln(w)
	for _, c := range r.NeedsReanchoring {
		_, _ = fmt.Fprintf(w, "control needs re-anchoring tenant=%s bundle=%s anchor=%s@%s: %s\n",
			c.TenantID, c.BundleID, c.LegacySCFID, c.LegacyRelease, c.Note)
	}
}
