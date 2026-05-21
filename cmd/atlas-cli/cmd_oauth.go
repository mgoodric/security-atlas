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
	"github.com/mgoodric/security-atlas/internal/auth/oauthcode"
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
	cmd.AddCommand(newOAuthAddRedirectURICmd())
	return cmd
}

// newOAuthAddRedirectURICmd wires `atlas-cli oauth add-redirect-uri
// <client_id> <redirect_uri>` — slice 189 AC-14.
//
// Registers a redirect URI for a given OAuth client. The
// `/oauth/authorize` handler validates incoming `redirect_uri` query
// parameters against this registry — unregistered URIs are rejected
// before any browser redirect, preventing open-redirect abuse (P0-189-2).
//
// Security policy: URIs MUST start with `https://` OR with
// `http://localhost` (self-host dev allowance). Plain-HTTP non-
// localhost URIs are rejected at the CLI layer to prevent
// misconfiguration that would silently bypass HTTPS for the auth-
// code transport.
//
// EXIT codes:
//
//	0 — URI registered
//	1 — duplicate (oauthcode.ErrDuplicateRedirectURI), policy
//	    violation (non-https non-localhost), or any other failure
func newOAuthAddRedirectURICmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add-redirect-uri <client_id> <redirect_uri>",
		Short: "register a redirect URI for an OAuth client",
		Long: `Register a redirect URI that the /oauth/authorize handler will accept
for the given client_id. Unregistered URIs are rejected with 400 —
this is the open-redirect prevention gate (P0-189-2).

URIs MUST start with https:// or http://localhost. Plain-HTTP non-
localhost URIs are rejected.

Requires DATABASE_URL in the environment (atlas_app role connection).

Example:
  DATABASE_URL=postgres://... atlas-cli oauth add-redirect-uri \
    0e3b4a7e-...  https://atlas.example.com/oauth/callback`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientID := args[0]
			redirectURI := args[1]
			if clientID == "" || redirectURI == "" {
				return fmt.Errorf("client_id and redirect_uri are required")
			}
			if !isAllowedRedirectURIScheme(redirectURI) {
				return fmt.Errorf(
					"redirect_uri must start with https:// or http://localhost (got %q)",
					redirectURI)
			}
			dsn := os.Getenv("DATABASE_URL")
			if dsn == "" {
				return fmt.Errorf("DATABASE_URL is required for `oauth add-redirect-uri`")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			pool, err := pgxpool.New(ctx, dsn)
			if err != nil {
				return fmt.Errorf("open pgxpool: %w", err)
			}
			defer pool.Close()

			store := oauthcode.New(pool)
			if err := store.RegisterRedirectURI(ctx, clientID, redirectURI); err != nil {
				if errors.Is(err, oauthcode.ErrDuplicateRedirectURI) {
					return fmt.Errorf("redirect_uri %q already registered for client %q",
						redirectURI, clientID)
				}
				return fmt.Errorf("register: %w", err)
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(),
				"registered: client_id=%s redirect_uri=%s\n",
				clientID, redirectURI); err != nil {
				return fmt.Errorf("write to stdout: %w", err)
			}
			return nil
		},
	}
}

// isAllowedRedirectURIScheme returns true iff the URI starts with
// `https://` OR `http://localhost`. Defense-in-depth at the CLI
// layer; the DB layer accepts any non-empty string, so this gate
// runs before the persistence call.
func isAllowedRedirectURIScheme(uri string) bool {
	const httpsPrefix = "https://"
	const localhostPrefix = "http://localhost"
	if len(uri) >= len(httpsPrefix) && uri[:len(httpsPrefix)] == httpsPrefix {
		return true
	}
	if len(uri) >= len(localhostPrefix) && uri[:len(localhostPrefix)] == localhostPrefix {
		return true
	}
	return false
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
