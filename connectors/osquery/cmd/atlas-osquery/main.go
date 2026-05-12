// Package main is the security-atlas osquery/Fleet endpoint connector
// binary. One subcommand per operation:
//
//	register  — announce this connector instance to the platform
//	run       — pull host posture and push evidence records
//	            --mode=fleet  (default): Fleet REST API
//	            --mode=local            : local osqueryd Unix socket
//	scopes    — print the canonical least-privilege Fleet API roles
//
// Constitutional invariants honored:
//   - Two SDK profiles (canvas §4.1): slice 047 ships the pull profile.
//   - No proprietary collector agents (anti-pattern, canvas §1.6): the
//     agent IS osquery (open-source); we are a read-only consumer of
//     Fleet's REST surface or the local osqueryd extension socket.
//
// Anti-criteria (slice 047 P0): least-privilege Fleet roles documented
// and enforced (observer / observer_plus, no admin/maintainer); Fleet
// API token never logged (osqueryauth.Credential.String redacts);
// idempotency_key derived from sha256("osquery.host_posture" + host_uuid
// + hour); CLI help text never accepts the token through a visible flag
// that would put it in shell history.
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
