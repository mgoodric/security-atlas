package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/internal/auth/password"
)

// newBootstrapCmd wires `atlas-cli bootstrap ...` — helpers the
// docker-compose self-host bundle's atlas-bootstrap one-shot container
// uses during first boot (slice 037). These are not day-to-day operator
// commands; they exist so the bootstrap shell script can produce
// format-correct artifacts without re-implementing platform crypto.
func newBootstrapCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bootstrap",
		Short: "first-boot helpers for the docker-compose self-host bundle",
	}
	cmd.AddCommand(newBootstrapHashPasswordCmd())
	return cmd
}

// newBootstrapHashPasswordCmd wires `atlas-cli bootstrap hash-password`.
//
// It reads a plaintext password from stdin and prints the encoded
// argon2id hash to stdout — byte-identical to what
// internal/auth/password.Hash produces, so the seeded local_credentials
// row verifies cleanly against /auth/local/login. The bootstrap script
// pipes ATLAS_DEFAULT_USER_PASSWORD in and feeds the result to seed.sql.
//
// Reading from stdin (not a flag) keeps the plaintext out of the process
// argument list / `ps` output.
func newBootstrapHashPasswordCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "hash-password",
		Short: "read a plaintext password from stdin, print its argon2id hash",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			r := bufio.NewReader(os.Stdin)
			line, err := r.ReadString('\n')
			if err != nil && line == "" {
				return fmt.Errorf("read password from stdin: %w", err)
			}
			plaintext := strings.TrimRight(line, "\r\n")
			if plaintext == "" {
				return fmt.Errorf("empty password on stdin")
			}
			hash, err := password.Hash(plaintext)
			if err != nil {
				return fmt.Errorf("hash password: %w", err)
			}
			fmt.Println(hash)
			return nil
		},
	}
}
