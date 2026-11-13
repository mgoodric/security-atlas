package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/internal/auth/oauthclient"
	"github.com/mgoodric/security-atlas/internal/auth/oauthcode"
	"github.com/mgoodric/security-atlas/internal/tenancy"
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
	cmd.AddCommand(newOAuthBindClientCmd())
	cmd.AddCommand(newOAuthAddRedirectURICmd())
	cmd.AddCommand(newOAuthMigrateAPIKeyCmd())
	return cmd
}

// newOAuthMigrateAPIKeyCmd wires `atlas-cli oauth migrate-api-key
// <api_key>` — slice 191 AC-24..AC-26.
//
// The command takes a legacy slice-034 bearer token, looks up the
// credential's identity via apikeystore, then issues a NEW OAuth
// client_credentials pair under a name derived from the legacy
// credential. The plaintext client_id + client_secret are printed
// to stdout ONCE; the operator MUST record them. Source API key
// is NOT auto-deleted (P0-191-9 implicit + AC-26) — the operator
// runs 'atlas credentials revoke' separately after confirming the
// new credentials work.
//
// EXIT codes:
//
//	0 — issued; client_id + client_secret printed
//	1 — unknown source key OR duplicate name OR any other failure
//
// P0-191-9: secret prints to stdout EXACTLY ONCE; never persisted
// to disk by this command.
func newOAuthMigrateAPIKeyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate-api-key <api_key>",
		Short: "issue an OAuth client mirroring the supplied legacy API key",
		Long: `Issue a new OAuth client_credentials pair scoped to the same
identity as the supplied slice-034 legacy API key.

This is the operator-facing migration tool: take an existing API key
that an SDK consumer is using, generate an equivalent OAuth client,
hand the new credentials to the consumer, then revoke the old API
key separately.

The plaintext client_secret is printed to stdout EXACTLY ONCE
(P0-191-9). Record it now; it is unrecoverable.

Requires DATABASE_URL in the environment (atlas_app role connection).

Example:
  DATABASE_URL=postgres://... atlas-cli oauth migrate-api-key atlas_...
  source: name=ci-pipeline tenant=...
  client_id: 0e3b4a7e-...
  client_secret: A1b2C3d4...

Note: the source API key is NOT auto-deleted. Run
'atlas-cli credentials revoke <key_id>' separately after the new
credentials are confirmed working.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			apiKey := args[0]
			return runMigrateAPIKey(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), apiKey)
		},
	}
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
	var (
		tenantFlag string
		unbound    bool
	)
	cmd := &cobra.Command{
		Use:   "issue-client <name>",
		Short: "issue a new OAuth client_credentials identity",
		Long: `Issue a new OAuth client_id + client_secret pair and persist the
argon2id hash to the oauth_clients table. The plaintext secret is
printed ONCE to stdout — record it now; it is unrecoverable.

The client is bound to a tenant: its client_credentials tokens act in
that tenant, and its writes land there. --tenant names the tenant; without
it, the install's only tenant is used, and an install with several tenants
(or none) is refused. --unbound issues a client bound to no tenant, for a
public client used only by the authorization-code or device flows; its
client_credentials tokens cannot write.

Requires DATABASE_URL in the environment. Defaulting the tenant reads
every tenant, so it needs the BYPASSRLS (atlas_migrate) connection; with
the atlas_app role, pass --tenant.

Example:
  DATABASE_URL=postgres://... atlas-cli oauth issue-client ci-pipeline
  client_id: 0e3b4a7e-...
  client_secret: A1b2C3d4...
  tenant_id: 00000000-0000-4000-8000-000000000001`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if name == "" {
				return fmt.Errorf("name argument is required")
			}
			if unbound && tenantFlag != "" {
				return errors.New("--tenant and --unbound are mutually exclusive")
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
			var (
				client *oauthclient.Client
				secret string
			)
			if unbound {
				client, secret, err = store.Issue(ctx, name)
			} else {
				tenant, terr := chooseClientTenant(ctx, tenantFlag, poolTenantResolver(pool))
				if terr != nil {
					return terr
				}
				client, secret, err = store.IssueForTenant(ctx, name, tenant)
			}
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
			if client.TenantID != nil {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "tenant_id: %s\n", *client.TenantID)
			} else {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"\nThis client is bound to no tenant; its client_credentials tokens cannot write.\n")
			}
			// Best-effort note to stderr; failure here does not block the
			// command (the secret has already landed on stdout).
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\nRecord the client_secret NOW — it is unrecoverable.\n")
			return nil
		},
	}
	cmd.Flags().StringVar(&tenantFlag, "tenant", "", "tenant UUID the client acts in (default: the install's only tenant)")
	cmd.Flags().BoolVar(&unbound, "unbound", false, "issue a client bound to no tenant (public clients only; cannot write)")
	return cmd
}

// newOAuthBindClientCmd wires `atlas-cli oauth bind-client <client_id>`
// (security-atlas#1739): it binds an existing OAuth client to a tenant so
// its client_credentials tokens act there. Clients issued before clients
// were bound are unbound on a multi-tenant install (the upgrade migration
// binds them only when there is one tenant), and their writes are refused
// until they are bound.
func newOAuthBindClientCmd() *cobra.Command {
	var tenantFlag string
	cmd := &cobra.Command{
		Use:   "bind-client <client_id>",
		Short: "bind an OAuth client to a tenant",
		Long: `Bind an existing OAuth client to a tenant, so its client_credentials
tokens act in that tenant and its writes land there. --tenant names the
tenant; without it, the install's only tenant is used, and an install with
several tenants is refused. A client already bound to a different tenant is
refused: issue a new client instead.

Requires DATABASE_URL in the environment (the BYPASSRLS atlas_migrate
connection when --tenant is omitted).

Example:
  DATABASE_URL=postgres://... atlas-cli oauth bind-client 0e3b4a7e-... --tenant 00000000-0000-4000-8000-000000000001`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dsn := os.Getenv("DATABASE_URL")
			if dsn == "" {
				return errors.New("DATABASE_URL is required for `oauth bind-client`")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			pool, err := pgxpool.New(ctx, dsn)
			if err != nil {
				return fmt.Errorf("open pgxpool: %w", err)
			}
			defer pool.Close()
			return runOAuthBindClient(ctx, cmd.OutOrStdout(), oauthclient.New(pool),
				poolTenantResolver(pool), args[0], tenantFlag)
		},
	}
	cmd.Flags().StringVar(&tenantFlag, "tenant", "", "tenant UUID to bind to (default: the install's only tenant)")
	return cmd
}

// clientBinder is the slice of oauthclient.Store that bind-client needs,
// so the command body can be tested without a database.
type clientBinder interface {
	Bind(ctx context.Context, clientID string, tenant uuid.UUID) (*oauthclient.Client, error)
}

// tenantResolver answers "which tenant is implied?" — in production
// tenancy.ResolveImplicitTenant over the CLI's pool.
type tenantResolver func(ctx context.Context) (tenancy.TenantRef, error)

func poolTenantResolver(pool *pgxpool.Pool) tenantResolver {
	return func(ctx context.Context) (tenancy.TenantRef, error) {
		return tenancy.ResolveImplicitTenant(ctx, pool)
	}
}

func runOAuthBindClient(ctx context.Context, out io.Writer, b clientBinder, resolve tenantResolver, clientID, tenantFlag string) error {
	if clientID == "" {
		return errors.New("client_id argument is required")
	}
	tenant, err := chooseClientTenant(ctx, tenantFlag, resolve)
	if err != nil {
		return err
	}
	c, err := b.Bind(ctx, clientID, tenant)
	switch {
	case errors.Is(err, oauthclient.ErrUnknownClient):
		return fmt.Errorf("no active OAuth client with client_id %q", clientID)
	case errors.Is(err, oauthclient.ErrAlreadyBound):
		return fmt.Errorf("client %q is already bound to a different tenant; issue a new client for tenant %s", clientID, tenant)
	case err != nil:
		return fmt.Errorf("bind: %w", err)
	}
	_, err = fmt.Fprintf(out, "client_id: %s\ntenant_id: %s\n", c.ClientID, tenant)
	return err
}

// chooseClientTenant picks the tenant an OAuth client is bound to: the
// --tenant flag when set, otherwise the install's only tenant. It never
// returns uuid.Nil, and it refuses rather than guesses when several
// tenants exist (security-atlas#1739).
func chooseClientTenant(ctx context.Context, tenantFlag string, resolve tenantResolver) (uuid.UUID, error) {
	if tenantFlag != "" {
		id, err := uuid.Parse(tenantFlag)
		if err != nil {
			return uuid.Nil, fmt.Errorf("--tenant %q is not a UUID: %w", tenantFlag, err)
		}
		if id == uuid.Nil {
			return uuid.Nil, errors.New("--tenant must not be the all-zeros tenant")
		}
		return id, nil
	}
	ref, err := resolve(ctx)
	if err != nil {
		var amb *tenancy.AmbiguousTenantError
		switch {
		case errors.As(err, &amb):
			return uuid.Nil, fmt.Errorf("pass --tenant: %w", err)
		case errors.Is(err, tenancy.ErrNoTenants):
			return uuid.Nil, errors.New("no tenant is visible: pass --tenant, or use the BYPASSRLS (atlas_migrate) DATABASE_URL so the only tenant can be found")
		}
		return uuid.Nil, fmt.Errorf("resolve tenant: %w", err)
	}
	return ref.ID, nil
}
