# cmd/

Main entrypoints. Each `cmd/<name>/main.go` is a binary build target.

| Binary               | Purpose                                                                                       | Filled by slice        |
| -------------------- | --------------------------------------------------------------------------------------------- | ---------------------- |
| `cmd/atlas/`         | Platform HTTP/gRPC server (release binary: `security-atlas`)                                  | 013, 008, 030, ongoing |
| `cmd/atlas-cli/`     | CLI (release binary: `security-atlas-cli` — `evidence push`, `credentials issue`, …)          | 003                    |
| `cmd/atlas-mcp/`     | MCP (Model Context Protocol) server — stdio; ten read tools + five write tools                | 172, 173               |
| `cmd/atlas-oscal/`   | OSCAL bridge client + `import-catalog` (talks to Python `compliance-trestle` via gRPC)        | 030, 492               |
| `cmd/atlas-openapi/` | Generates `docs/openapi.yaml` from the canonical RouteSpecs (`just openapi-generate`)         | 140                    |
| `cmd/scripts/`       | CI-only helper binaries (`coverage-check`, `coverage-gate`, `duphelper-lint`, `errleak-lint`) | ongoing                |

`just build-go` runs `go build ./...`, which is a compile check and writes no
binaries. To get executables, build them explicitly:

```sh
go build -o ./bin/ ./cmd/atlas ./cmd/atlas-cli ./cmd/atlas-mcp ./cmd/atlas-oscal
```

Release artifacts (binary names, archives, SBOMs) are defined in
`.goreleaser.yaml`, which builds exactly two targets: `cmd/atlas` and
`cmd/atlas-cli`. `cmd/atlas-mcp`, `cmd/atlas-oscal`, and `cmd/atlas-openapi`
ship from source only.
