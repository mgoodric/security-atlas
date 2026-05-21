# atlas-mcp

Model Context Protocol (MCP) server for security-atlas — stdio transport,
six read-only tools (slice 172) plus five write tools (slice 173) that
wrap the platform's existing HTTP API.

**Status: EXPERIMENTAL (slices 172 + 173).** The tool surface, input
schemas, and response shapes are subject to change while the surface is
in 30-day soak (per the slice 116 advisory-vs-required pattern). Pin your
MCP client config to a specific `atlas-mcp` version while the surface
stabilizes.

**Write tools require HITL approval.** Every write tool files a proposal
at `state='ai_proposed'`; no audit-binding artifact is published until
an authorized approver calls `confirm_write` (or clicks Approve in the
web UI). The schema-level CHECK constraint `mcp_wp_ai_assist_invariant`
guarantees this contract at the database layer. See
`docs/audit-log/173-mcp-write-tools-decisions.md` for the full design.

## What it does

`atlas-mcp` is a small Go binary that speaks
[MCP](https://modelcontextprotocol.io) over stdin/stdout. An MCP
client (Claude Desktop, Claude Code, or any other MCP-aware tool)
launches it as a subprocess, passes credentials via env, and consumes
JSON-RPC responses from stdout.

All eleven tools wrap existing security-atlas HTTP endpoints; nothing
new is mutated at the platform layer without a corresponding handler.
RLS-based tenant isolation is enforced by the platform; the MCP server
is a 1-tenant-per-process veneer over that surface.

### Read tools (slice 172)

| Tool                 | Wraps endpoint                                              | Returns                                                   |
| -------------------- | ----------------------------------------------------------- | --------------------------------------------------------- |
| `list_controls`      | `GET /v1/controls`                                          | Tenant's active controls (canonical row shape)            |
| `get_control`        | `GET /v1/anchors/{shortcode}` or list-then-filter for UUIDs | One control row                                           |
| `list_risks`         | `GET /v1/risks`                                             | Tenant risks; status filter forwarded as `?treatment=`    |
| `get_risk`           | `GET /v1/risks/{id}`                                        | One risk + linked controls + (optional) residual derive   |
| `list_evidence`      | `GET /v1/evidence`                                          | Evidence ledger window; **never includes `payload_json`** |
| `list_audit_periods` | `GET /v1/audit-periods`                                     | Tenant audit periods with freeze metadata                 |

Defaults: every list tool returns up to 100 results; pass `limit=N`
(max 500) to override. Asking for more than 500 returns a tool error
(P0-A9 — no "give me all" footgun).

### Write tools (slice 173 — HITL approval, Pattern A draft-then-confirm)

| Tool                    | Wraps endpoint                              | Effect                                                                |
| ----------------------- | ------------------------------------------- | --------------------------------------------------------------------- |
| `create_risk`           | `POST /v1/mcp/write-proposals`              | Files a draft risk; state=`ai_proposed`                               |
| `update_control_state`  | `POST /v1/mcp/write-proposals`              | Files a draft control-state override (synthesised as evidence)        |
| `push_evidence`         | `POST /v1/mcp/write-proposals`              | Files a draft evidence record append                                  |
| `update_risk_treatment` | `POST /v1/mcp/write-proposals`              | Files a draft treatment + owner change                                |
| `confirm_write`         | `POST /v1/mcp/write-proposals/{id}/confirm` | Approver-only: flips state to `applied`; the Applier writes canonical |

**HITL flow.** Write tools NEVER commit to the canonical tables directly.
Each write inserts a row into `mcp_write_proposals` with `state=
'ai_proposed', ai_assisted=true, human_approved=false, human_approver=
NULL`. An authorized approver (the operator's `IsApprover` or `IsAdmin`
credential) confirms via either (a) the `confirm_write` MCP tool or
(b) the web-UI Approve button. The platform then runs the canonical
Applier inside the same transaction as the state flip; a failure rolls
the proposal back to `ai_proposed`.

**Schema-level enforcement.** The CHECK constraint
`mcp_wp_ai_assist_invariant` blocks any row that claims `ai_assisted=true
AND human_approved=true` without `human_approver` set. This is the
database-level peer to CLAUDE.md's "AI-assist boundary (hard)".

**User-Agent.** Write tools send `User-Agent: atlas-mcp/<version> (mcp;
ai_assisted=write)` — distinct from the read tools'
`atlas-mcp/<version> (mcp; ai_assisted=read-only)`. Platform-side audit
aggregators distinguish the two.

**Pending-cap.** Each (tenant, operator) credential may have at most 5
proposals at `state=ai_proposed` at once. A sixth proposal returns a
`429 Too Many Requests` tool error; the operator must confirm or reject
existing proposals before filing more.

## Install

Build from source:

```bash
go build -o /usr/local/bin/atlas-mcp ./cmd/atlas-mcp
```

Or use the release binary (when released — slice 172 ships from
source only):

```bash
# Future: gh release download v0.X.0 --pattern 'atlas-mcp_*'
```

## Configure credentials

`atlas-mcp` reads the bearer token in one of two ways. **There is no
`--token=<value>` CLI flag** — flags are visible in `ps`, which would
leak the token to any process on the host (P0-A1).

### Option 1: environment variable

```bash
export ATLAS_BEARER_TOKEN="<your-bearer-token>"
export ATLAS_BASE_URL="https://atlas.example.com"   # defaults to http://localhost:8080

# Slice 173 — write tool AI provenance (optional; defaults are placeholders).
# Operators running a local Ollama model set these to the model tag + a
# version string the audit log can reference. The write tool stores the
# (name, version) pair on every proposal it files.
export ATLAS_MCP_AI_MODEL_NAME="llama3.1:8b-instruct-q5"
export ATLAS_MCP_AI_MODEL_VERSION="2026-05-01"
atlas-mcp
```

### Option 2: token file

```bash
echo "$YOUR_BEARER_TOKEN" > ~/.config/atlas/mcp-token
chmod 600 ~/.config/atlas/mcp-token
atlas-mcp --token-file ~/.config/atlas/mcp-token --base-url https://atlas.example.com
```

The token is loaded once at startup. To rotate, restart the subprocess
(your MCP client will spawn a fresh one on the next session).

## Claude Desktop config

Add to your `claude_desktop_config.json` (location varies by OS — see
[Anthropic docs](https://modelcontextprotocol.io/quickstart/user)):

```json
{
  "mcpServers": {
    "security-atlas": {
      "command": "/usr/local/bin/atlas-mcp",
      "args": ["--base-url", "https://atlas.example.com"],
      "env": {
        "ATLAS_BEARER_TOKEN": "your-bearer-token-here"
      }
    }
  }
}
```

## Claude Code config

Use the [`claude mcp` CLI](https://docs.anthropic.com/en/docs/claude-code/mcp):

```bash
claude mcp add security-atlas /usr/local/bin/atlas-mcp \
  --env ATLAS_BEARER_TOKEN=your-bearer-token-here \
  --env ATLAS_BASE_URL=https://atlas.example.com
```

Or hand-edit `~/.config/claude/mcp-servers.json` with the same shape
as the Claude Desktop example above.

## What gets logged where

The MCP server emits two distinct log streams:

- **stdout** is the JSON-RPC wire — every byte goes to the MCP client.
  Never log anything else here.
- **stderr** is captured by the MCP client's session log. The MCP
  server writes exactly one line per tool call:

  ```
  atlas-mcp tool=list_controls duration_ms=42 status=ok
  ```

  Per P0-A7, **tool arguments and result bodies are NEVER logged to
  stderr.** The platform's per-request HTTP access log (server side)
  is the authoritative record of who asked for what; correlate via
  the `atlas-mcp/<version> (mcp; ai_assisted=read-only)` User-Agent.

## Protocol coverage

`atlas-mcp` implements the subset of MCP needed for tool-only servers
at protocol revision `2024-11-05`:

- `initialize` (handshake)
- `tools/list`
- `tools/call`
- `notifications/initialized` (client → server, no response)

Resources, prompts, sampling, completion, and logging notifications
are NOT implemented. Adding any of them is a separately-merged
follow-on (see [`docs/audit-log/172-mcp-server-decisions.md`](../../docs/audit-log/172-mcp-server-decisions.md)
"D2 — MCP framework" for the hand-rolled-vs-library rationale).

## Versioning + stability

Slice 172 ships the foundation as **experimental**. While in soak:

- The set of six tools is fixed. New tools require a follow-on slice
  (currently slice 174 is the spillover number — see the slice doc).
- Input schemas are governed by the snapshot at
  `internal/mcp/testdata/tools.golden.json`. The CI gate (slice 172
  AC-15) blocks unintentional drift; intentional changes must
  regenerate the snapshot AND be additive.
- The protocol revision is pinned to `2024-11-05`. A protocol bump
  will be a separately-merged slice.
- The `User-Agent` template is part of the platform/MCP contract —
  do not change `(mcp; ai_assisted=read-only)` without coordinating
  with the platform-side log filter that consumes it.

Write tools land in slice 173 (gated on this slice merging + a fresh
STRIDE pass + HITL approval flow per CLAUDE.md "AI-assist boundary").

## Security posture (slice 172 anti-criteria)

This server is deliberately small. The 13 P0 anti-criteria that
govern its behavior are documented in
[`docs/issues/172-mcp-server-foundation-read-tools.md`](../../docs/issues/172-mcp-server-foundation-read-tools.md);
the runtime-enforceable subset is summarized here so operators can
audit:

- **No CLI flag carries the bearer token.** Env var or file path only.
- **No `payload_json` is ever in a tool response.** The evidence list
  tool's typed wire shape omits the field, even when the platform
  endpoint would carry one.
- **List tools cap at 500.** A `limit > 500` is a tool error.
- **HTTP 429 surfaces verbatim.** No silent retries; the LLM agent
  observes the `Retry-After` and backs off.
- **Goroutine-leak test under `-race`.** A 100-call loop must not
  grow the runtime's goroutine count by more than 10.

## Troubleshooting

`atlas-mcp: bearer token required: set ATLAS_BEARER_TOKEN env var or pass --token-file <path>`
: The token resolver found nothing. Set the env var or pass
`--token-file`.

`atlas-mcp: init client: base url scheme must be http or https, got "..."`
: The base URL doesn't have a valid scheme. Set `ATLAS_BASE_URL` to
a full URL like `https://atlas.example.com`.

`platform http 401: ...`
: The bearer token is rejected by the platform. Confirm the token is
valid by hitting the platform's `/v1/me` endpoint with the same
bearer via curl.

`platform http 429 (retry after Ns): ...`
: The platform rate-limited the request. The LLM agent should observe
this and back off; restarting `atlas-mcp` does not help (the
bucket is per-credential, server-side).

`response body exceeds 1048576 bytes (P0 cap)`
: The platform endpoint returned more than 1 MiB. Surface to the
maintainer; this indicates the platform endpoint needs a
server-side cap (file as a slice-174 spillover).
