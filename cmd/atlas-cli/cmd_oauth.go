package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/internal/auth/oauthclient"
)

// newOAuthCmd wires `atlas-cli oauth ...` — operator-only helpers
// the atlas-bootstrap one-shot container and the
// docker-compose self-host bundle invoke during first boot. These
// are not day-to-day end-user commands; they exist so an operator
// (or a bootstrap script) can produce format-correct OAuth client
// identities without going through HTTP / gRPC.
//
// Slice 188 ships `issue-client`; later slices will add list/revoke
// surfaces when self-service tenant-level issuance becomes a
// requirement.
func newOAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "oauth",
		Short: "OAuth client lifecycle (issue / rotate)",
	}
	cmd.AddCommand(newOAuthIssueClientCmd())
	return cmd
}

// newOAuthIssueClientCmd wires `atlas-cli oauth issue-client <name>`.
//
// The command reads the DATABASE_URL env var, opens a pgxpool, calls
// internal/auth/oauthclient.Store.Issue to insert a new row, and
// prints the public client_id + plaintext client_secret EXACTLY
// ONCE to stdout. Operators MUST record the secret because the
// plaintext is unrecoverable — only the argon2id hash is persisted.
//
// EXIT codes:
//
//	0 — issued; client_id + client_secret printed
//	1 — duplicate name (oauthclient.ErrDuplicateName) OR any other
//	    operational failure (DB connect, missing env, etc.)
//
// AC-3 calls out the duplicate-name -> exit 1 mapping; the broader
// "any error -> exit 1" convention matches the bootstrap and
// credentials commands.
func newOAuthIssueClientCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "issue-client <name>",
		Short: "issue a new OAuth client_credentials identity",
		Long: `Issue a new OAuth client_id + client_secret pair and persist the
argon2id hash to the oauth_clients table. The plaintext secret is
printed ONCE to stdout — record it now; it is unrecoverable.

Requires DATABASE_URL in the environment (pointing at the atlas_app
role connection string).

Example:
  DATABASE_URL=postgres://... atlas-cli oauth issue-client ci-pipeline
  client_id: 0e3b4a7e-...
  client_secret: A1b2C3d4...`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if name == "" {
				return fmt.Errorf("name argument is required")
			}
			dsn := os.Getenv("DATABASE_URL")
			if dsn == "" {
				return fmt.Errorf("DATABASE_URL is required for `oauth issue-client`")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			pool, err := pgxpool.New(ctx, dsn)
			if err != nil {
				return fmt.Errorf("open pgxpool: %w", err)
			}
			defer pool.Close()

			store := oauthclient.New(pool)
			client, secret, err := store.Issue(ctx, name)
			if err != nil {
				if errors.Is(err, oauthclient.ErrDuplicateName) {
					return fmt.Errorf("a client with name %q already exists", name)
				}
				return fmt.Errorf("issue: %w", err)
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "client_id: %s\n", client.ClientID); err != nil {
				return fmt.Errorf("write client_id to stdout: %w", err)
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "client_secret: %s\n", secret); err != nil {
				return fmt.Errorf("write client_secret to stdout: %w", err)
			}
			// Best-effort note to stderr; failure here does not block the
			// command (the secret has already landed on stdout).
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\nRecord the client_secret NOW — it is unrecoverable.\n")
			return nil
		},
	}
	return cmd
}
