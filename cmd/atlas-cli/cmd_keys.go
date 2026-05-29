package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/internal/auth/keystore/fsstore"
)

// newKeysCmd wires `atlas-cli keys ...` — operator-facing JWT signing
// key lifecycle commands (slice 366). Unlike the `oauth` subcommands,
// these operate DIRECTLY on the filesystem keystore (the directory
// resolved by ATLAS_KEYSTORE_PATH, the --keystore flag, or the
// compiled-in default) and require NO DATABASE_URL — key material lives
// on disk, not in Postgres.
//
// P0-366-1: none of these commands print private key material. Only
// KeyIDs, ages, and roles are rendered.
func newKeysCmd() *cobra.Command {
	var keystorePath string
	cmd := &cobra.Command{
		Use:   "keys",
		Short: "JWT signing key lifecycle (list / rotate / prune)",
		Long: `Manage the OAuth Authorization Server's JWT signing keys.

These commands operate on the filesystem keystore directory (resolved
from the --keystore flag, then ATLAS_KEYSTORE_PATH, then the compiled-in
default). They require no database connection.

Routine annual rotation and emergency rotation (suspected compromise)
are documented in docs/runbooks/jwt-key-rotation.md.`,
	}
	cmd.PersistentFlags().StringVar(&keystorePath, "keystore", "",
		"keystore directory (env: ATLAS_KEYSTORE_PATH; default: compiled-in)")
	cmd.AddCommand(newKeysListCmd(&keystorePath))
	cmd.AddCommand(newKeysRotateCmd(&keystorePath))
	cmd.AddCommand(newKeysPruneCmd(&keystorePath))
	return cmd
}

// newKeysListCmd wires `atlas-cli keys list`.
func newKeysListCmd(keystorePath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "list every signing/verification key with age + role",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := fsstore.Open(fsstore.ResolvePath(*keystorePath))
			if err != nil {
				return fmt.Errorf("open keystore: %w", err)
			}
			infos, err := store.List(cmd.Context())
			if err != nil {
				return fmt.Errorf("list keys: %w", err)
			}
			for _, ki := range infos {
				role := "verifying"
				if ki.Signing {
					role = "signing"
				}
				if _, err := fmt.Fprintf(cmd.OutOrStdout(),
					"%s  role=%s  age=%s\n", ki.KeyID, role, formatAge(ki.Age)); err != nil {
					return fmt.Errorf("write: %w", err)
				}
			}
			return nil
		},
	}
}

// newKeysRotateCmd wires `atlas-cli keys rotate`.
//
// Rotation generates a fresh ES256 keypair, makes it the active signer,
// and retains the prior key(s) for the overlap window. The JWKS endpoint
// publishes the full verification set, so verifiers see both keys during
// the window and tokens signed with the prior key keep verifying until
// pruned.
func newKeysRotateCmd(keystorePath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "rotate",
		Short: "generate a new signing key (prior key retained for overlap)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := fsstore.Open(fsstore.ResolvePath(*keystorePath))
			if err != nil {
				return fmt.Errorf("open keystore: %w", err)
			}
			before, _, _ := store.Get(cmd.Context())
			if err := store.Rotate(cmd.Context()); err != nil {
				return fmt.Errorf("rotate: %w", err)
			}
			after, _, err := store.Get(cmd.Context())
			if err != nil {
				return fmt.Errorf("get after rotate: %w", err)
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(),
				"rotated: previous_signing_kid=%s new_signing_kid=%s\n",
				before.KeyID, after.KeyID); err != nil {
				return fmt.Errorf("write: %w", err)
			}
			return nil
		},
	}
}

// newKeysPruneCmd wires `atlas-cli keys prune`.
//
// Prune removes keypair files past the overlap window. It is a DRY RUN
// by default (prints what WOULD be removed, removes nothing); pass
// --confirm to actually delete. The active signing key is NEVER pruned
// regardless of age (P0-366-2).
func newKeysPruneCmd(keystorePath *string) *cobra.Command {
	var confirm bool
	var overlap time.Duration
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "remove keys past the overlap window (dry-run by default)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := fsstore.Open(fsstore.ResolvePath(*keystorePath))
			if err != nil {
				return fmt.Errorf("open keystore: %w", err)
			}
			cutoff := fsstore.PruneCutoff(time.Now(), fsstore.DefaultAccessTokenLifetime, overlap)
			if !confirm {
				// Dry run: enumerate candidates without removing.
				infos, err := store.List(cmd.Context())
				if err != nil {
					return fmt.Errorf("list keys: %w", err)
				}
				var candidates int
				for _, ki := range infos {
					if ki.Signing {
						continue
					}
					// A key older than (now - cutoff) is past the window.
					if ki.Age >= time.Since(cutoff) {
						if _, err := fmt.Fprintf(cmd.OutOrStdout(),
							"would-prune: %s (age=%s)\n", ki.KeyID, formatAge(ki.Age)); err != nil {
							return fmt.Errorf("write: %w", err)
						}
						candidates++
					}
				}
				if _, err := fmt.Fprintf(cmd.OutOrStdout(),
					"dry-run: %d key(s) eligible for prune; re-run with --confirm to remove\n",
					candidates); err != nil {
					return fmt.Errorf("write: %w", err)
				}
				return nil
			}
			removed, err := store.Prune(cmd.Context(), cutoff)
			if err != nil {
				return fmt.Errorf("prune: %w", err)
			}
			for _, kid := range removed {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "pruned: %s\n", kid); err != nil {
					return fmt.Errorf("write: %w", err)
				}
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "pruned %d key(s)\n", len(removed)); err != nil {
				return fmt.Errorf("write: %w", err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&confirm, "confirm", false, "actually remove keys (default: dry-run)")
	cmd.Flags().DurationVar(&overlap, "overlap", fsstore.DefaultRotationOverlap,
		"overlap window beyond access-token lifetime before a rotated-out key is prunable")
	return cmd
}

// formatAge renders a duration in a compact operator-friendly form.
func formatAge(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
