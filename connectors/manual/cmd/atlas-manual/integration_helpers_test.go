package main

import (
	"golang.org/x/crypto/ssh"

	"github.com/mgoodric/security-atlas/connectors/manual/internal/manualsftp"
)

// readKeyFileForTest is a thin shim over manualsftp.LoadPrivateKey so
// integration_test.go can exercise the redaction guarantee without
// importing internal packages directly into its block.
func readKeyFileForTest(path string) ([]byte, error) {
	return manualsftp.LoadPrivateKey(path)
}

// parseKeyForTest funnels through BuildSSHConfig with a non-insecure
// callback so we exercise the parse path and any error it can produce.
func parseKeyForTest(pem []byte) error {
	_, err := manualsftp.BuildSSHConfig(manualsftp.BuildOpts{
		User:            "atlas",
		PrivateKeyPEM:   pem,
		HostKeyCallback: ssh.FixedHostKey(nil),
	})
	return err
}
