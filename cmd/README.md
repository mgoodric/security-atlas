# cmd/

Main entrypoints. Each `cmd/<name>/main.go` is a binary build target.

| Binary             | Purpose                                                                      | Filled by slice        |
| ------------------ | ---------------------------------------------------------------------------- | ---------------------- |
| `cmd/atlas/`       | Platform HTTP/gRPC server                                                    | 013, 008, 030, ongoing |
| `cmd/atlas-cli/`   | CLI (`security-atlas evidence push`, `credentials issue/rotate/revoke/list`) | 003                    |
| `cmd/atlas-mcp/`   | MCP (Model Context Protocol) server — stdio; six read-only tools             | 172                    |
| `cmd/atlas-oscal/` | OSCAL bridge service (talks to Python `compliance-trestle` via gRPC)         | 030                    |

Slice 001 ships hello-world `main.go` files in each — enough for `go build ./...` and `go test ./...` to succeed.
