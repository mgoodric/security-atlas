package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/auth/apikeystore"
	"github.com/mgoodric/security-atlas/internal/auth/bearer"
	"github.com/mgoodric/security-atlas/internal/auth/oauthclient"
)

// runMigrateAPIKey implements the `atlas oauth migrate-api-key`
// command body. Split out of cmd_oauth.go so it can be tested
// against an in-memory pgxpool without going through cobra plumbing.
//
// D2 (decisions log): the identity-mapping shape is "name-only
// inheritance" — the new OAuth client adopts a name derived from
// the legacy credential id so the credential's audit lineage is
// preserved. Tenant grants are NOT copied because oauth_clients
// is platform-global by design (slice 188 D1); operators with
// per-tenant identity needs should issue OAuth clients per tenant
// explicitly.
//
// P0-191-9: the secret is printed to stdout exactly ONCE via
// fmt.Fprintf. The function never persists the plaintext anywhere
// — even on partial success, the stdout write is the only sink.
func runMigrateAPIKey(ctx context.Context, stdout, stderr io.Writer, apiKey string) error {
	if apiKey == "" {
		return errors.New("api_key argument is required")
	}
	if !looksLikeLegacyBearer(apiKey) {
		return fmt.Errorf("api_key must be a legacy slice-034 bearer (prefix %q)", bearer.PrefixProd)
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL is required for `oauth migrate-api-key`")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("open pgxpool: %w", err)
	}
	defer pool.Close()

	// Look up the source credential via apikeystore.Authenticate.
	// The hash-on-presentation path is the only way to map a
	// legacy bearer plaintext back to its identity (the DB never
	// stores plaintext). A non-nil credential confirms the key is
	// real, active, and not retired.
	hkey := os.Getenv("BEARER_HASH_KEY")
	if hkey == "" {
		return errors.New("BEARER_HASH_KEY is required to verify the legacy bearer")
	}
	hasher, err := bearer.NewHasher([]byte(hkey))
	if err != nil {
		return fmt.Errorf("init hasher: %w", err)
	}
	store := apikeystore.NewStore(pool, pool, hasher, 0)
	src, err := store.Authenticate(ctx, apiKey)
	if err != nil {
		return fmt.Errorf("look up source API key: %w", err)
	}
	_, _ = fmt.Fprintf(stdout, "source: id=%s tenant=%s admin=%t approver=%t roles=%v\n",
		src.ID, src.TenantID, src.IsAdmin, src.IsApprover, src.OwnerRoles)

	// Issue a new OAuth client. The name carries the legacy
	// credential id so an operator can audit which OAuth clients
	// were issued via migration vs direct `oauth issue-client`.
	name := fmt.Sprintf("migrated-from-%s", src.ID)
	clients := oauthclient.New(pool)
	newClient, secret, err := clients.Issue(ctx, name)
	if err != nil {
		if errors.Is(err, oauthclient.ErrDuplicateName) {
			return fmt.Errorf("an OAuth client named %q already exists — has this key been migrated before?", name)
		}
		return fmt.Errorf("issue OAuth client: %w", err)
	}

	// P0-191-9: print plaintext secret to stdout EXACTLY ONCE.
	_, _ = fmt.Fprintf(stdout, "client_id: %s\n", newClient.ClientID)
	_, _ = fmt.Fprintf(stdout, "client_secret: %s\n", secret)
	_, _ = fmt.Fprintln(stderr)
	_, _ = fmt.Fprintln(stderr, "Record the client_secret NOW — it is unrecoverable.")
	_, _ = fmt.Fprintln(stderr, "After confirming the new credentials work, revoke the legacy")
	_, _ = fmt.Fprintf(stderr, "API key with: atlas-cli credentials revoke %s\n", src.ID)
	return nil
}

// looksLikeLegacyBearer is a coarse pre-flight check so the
// command can return a quick "you passed the wrong arg" error
// before opening a DB connection.
func looksLikeLegacyBearer(s string) bool {
	return strings.HasPrefix(s, bearer.PrefixProd) ||
		strings.HasPrefix(s, bearer.PrefixTest)
}
