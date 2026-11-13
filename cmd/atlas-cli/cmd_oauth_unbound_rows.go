package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/internal/auth/oauthclient"
)

// newOAuthUnboundRowsCmd wires `atlas-cli oauth unbound-rows`
// (security-atlas#1876): list, and on request purge, the rows written to
// the all-zeros tenant by OAuth clients that were unbound before #1739.
// Listing is the default and only reads; --purge deletes, and only when
// --confirm-rows restates the total the listing printed.
func newOAuthUnboundRowsCmd() *cobra.Command {
	var (
		purge       bool
		confirmRows int64
	)
	cmd := &cobra.Command{
		Use:   "unbound-rows",
		Short: "list (or purge) rows in the all-zeros tenant",
		Long: `List the rows that sit in the all-zeros tenant
(00000000-0000-0000-0000-000000000000), per table. They were written by
OAuth clients that were not bound to a tenant, before such writes were
refused; no user can see them, and any unbound client token can read them.

Without flags the command only reads. Append-only ledger tables (such as
evidence_records and every *_audit_log) are listed as retained and are never
purged (ADR-0012: ledger disposal is by tombstone only), and so is any table
whose rows a retained row references. To delete the rest, run it again with
--purge and --confirm-rows set to the purgeable total the listing printed.
The purge runs in one transaction and changes nothing when the total no
longer matches. It prints each table it purged and kept, and records the
purge in the tenant_data_purges table.

There is no reassign. To keep the data, re-create it in the right tenant
with a bound client (for controls, re-run 'atlas-cli controls upload'
with that client), then purge.

Requires DATABASE_URL in the environment: the BYPASSRLS atlas_migrate
connection, since an RLS-bound role sees none of these rows.

Examples:
  DATABASE_URL=postgres://... atlas-cli oauth unbound-rows
  DATABASE_URL=postgres://... atlas-cli oauth unbound-rows --purge --confirm-rows 42`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := validateUnboundRowsFlags(purge, cmd.Flags().Changed("confirm-rows"), confirmRows); err != nil {
				return err
			}
			dsn := os.Getenv("DATABASE_URL")
			if dsn == "" {
				return errors.New("DATABASE_URL is required for `oauth unbound-rows`")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			pool, err := pgxpool.New(ctx, dsn)
			if err != nil {
				return fmt.Errorf("open pgxpool: %w", err)
			}
			defer pool.Close()
			if purge {
				return runPurgeUnboundRows(ctx, cmd.OutOrStdout(), pool, confirmRows, currentOSUser())
			}
			return runListUnboundRows(ctx, cmd.OutOrStdout(), pool)
		},
	}
	cmd.Flags().BoolVar(&purge, "purge", false, "delete every all-zeros-tenant row (requires --confirm-rows)")
	cmd.Flags().Int64Var(&confirmRows, "confirm-rows", 0, "the total row count the listing printed; the purge refuses any other number")
	return cmd
}

// validateUnboundRowsFlags rejects a purge without a confirmation and a
// confirmation without a purge, before any database is opened.
func validateUnboundRowsFlags(purge, confirmSet bool, confirmRows int64) error {
	switch {
	case purge && !confirmSet:
		return errors.New("--purge requires --confirm-rows <total>: run without --purge first and restate the total it prints")
	case !purge && confirmSet:
		return errors.New("--confirm-rows only applies with --purge")
	case purge && confirmRows <= 0:
		return errors.New("--confirm-rows must be a positive row count")
	}
	return nil
}

// txBeginner is the slice of *pgxpool.Pool the command needs.
type txBeginner interface {
	BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
}

func runListUnboundRows(ctx context.Context, out io.Writer, db txBeginner) error {
	tx, err := db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	counts, err := oauthclient.ListUnboundRows(ctx, tx)
	if err != nil {
		return err
	}
	return writeUnboundListing(out, counts)
}

func runPurgeUnboundRows(ctx context.Context, out io.Writer, db txBeginner, confirmRows int64, osUser string) error {
	tx, err := db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	res, err := oauthclient.PurgeUnboundRows(ctx, tx, confirmRows, osUser)
	if errors.Is(err, oauthclient.ErrPurgeNotConfirmed) {
		return fmt.Errorf("%w; nothing was deleted. Run without --purge and restate the total", err)
	}
	if err != nil {
		return fmt.Errorf("purge rolled back, nothing was deleted: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit purge: %w", err)
	}
	return writeUnboundPurge(out, res)
}

// currentOSUser names who ran the purge for its tenant_data_purges row.
func currentOSUser() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return os.Getenv("USER")
}

func writeUnboundListing(out io.Writer, counts []oauthclient.UnboundTableCount) error {
	if len(counts) == 0 {
		_, err := fmt.Fprintln(out, "no rows in the all-zeros tenant")
		return err
	}
	for _, c := range counts {
		if err := writeCountLine(out, "", c); err != nil {
			return err
		}
	}
	total := oauthclient.PurgeableRows(counts)
	if total == 0 {
		_, err := fmt.Fprintln(out, "purgeable total\t0\nnothing to purge: every listed row is retained")
		return err
	}
	_, err := fmt.Fprintf(out, "purgeable total\t%d\nto delete them: atlas-cli oauth unbound-rows --purge --confirm-rows %d\n", total, total)
	return err
}

func writeUnboundPurge(out io.Writer, res oauthclient.PurgeResult) error {
	for _, c := range res.Purged {
		if err := writeCountLine(out, "purged\t", c); err != nil {
			return err
		}
	}
	for _, c := range res.Retained {
		if err := writeCountLine(out, "kept\t", c); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(out, "purged total\t%d\nrecorded in tenant_data_purges\t%s\n",
		oauthclient.PurgeableRows(res.Purged), res.AuditID)
	return err
}

func writeCountLine(out io.Writer, prefix string, c oauthclient.UnboundTableCount) error {
	if c.Retained != "" {
		_, err := fmt.Fprintf(out, "%s%s\t%d\tretained: %s\n", prefix, c.Table, c.Rows, c.Retained)
		return err
	}
	_, err := fmt.Fprintf(out, "%s%s\t%d\n", prefix, c.Table, c.Rows)
	return err
}
