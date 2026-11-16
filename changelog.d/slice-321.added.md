- **test(coverage):** slice 321 — `pkg/sdk-go` coverage lift from 67.6%
  to 94.6% merged. Round-3 coverage spillover surfaced during slice 312's
  audit (`docs/coverage-audit-2026-05-round-3.md`) — `pkg/sdk-go` (the
  public Go push-SDK from slice 003) measured 67.6% with no entry in
  `cmd/scripts/coverage-thresholds.json` (untracked). 37 statements, so a
  one-statement gap below the 70% aspirational target. A single new
  `pkg/sdk-go/client_test.go` adds seven branch-targeted unit tests
  against the public surface (the in-tree harness at
  `internal/api/server_test.go` already exercises the bufconn happy
  path — slice 321's tests cover what that harness does not):
  `WithTLSConfig` (0% → 100%) via a `*tls.Config` with a distinctive
  `MinVersion: TLS13` + `ServerName` passed through `NewClient`'s default
  TLS path; `NewClient` (61.1% → 94.4%) via the empty-bearer guard, the
  `WithInsecure` non-loopback refuse path (table-driven across RFC-5737
  IPv4, RFC-3849 IPv6, RFC-6761 + RFC-2606 reserved TLDs), and the
  default-TLS construction path; `isLoopback` (66.7% → 100%) via the
  same non-loopback table (final `return false` branch) plus a bare
  `localhost` no-port case exercising the `net.SplitHostPort` error
  fallback and the four loopback hosts the switch arms accept
  (`localhost`, `127.0.0.1`, `[::1]`, `:7777` → `""`); `Close` (66.7% →
  100%) via a `NewClient`-constructed (owned-conn) close. Tests are
  pure-Go — `grpc.NewClient` is a lazy constructor in grpc-go v1.59+ so
  no server bind is required to exercise option-validation branches.
  Floor in `cmd/scripts/coverage-thresholds.json` adds a new entry at
  `pkg/sdk-go: 92` per slice 069's `floor(measured - 2pp)` methodology
  (`floor(94.6 - 2) = 92`) — the package was previously untracked, so
  the ratchet adds a floor where there was none. No
  vendor-prefixed tokens in fixtures per `CLAUDE.md`'s hard rule — the
  new test bearer (`test-bearer-321`) is neutral. Closes
  [#321](docs/issues/321-coverage-pkg-sdk-go.md).
