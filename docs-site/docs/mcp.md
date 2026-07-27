# MCP server — assistant access to your program

security-atlas ships an **MCP (Model Context Protocol) server**, `atlas-mcp`, that
lets your security team query and update the program from an AI assistant —
[Claude Desktop](https://www.anthropic.com/claude), [Claude Code](https://docs.anthropic.com/en/docs/claude-code),
or any MCP-aware client — instead of clicking through the UI.

Once connected, the assistant can answer questions from your **live data**:

- _"What are my top risks in treatment right now?"_
- _"Which controls have stale or missing evidence?"_
- _"Show me the audit periods and which ones are frozen."_
- _"Draft a new risk for the unencrypted-backups finding"_ — filed as a proposal for you to approve.

!!! warning "Status: experimental"

    The tool surface is in soak and its input/response shapes may still change.
    Pin your MCP client to a specific `atlas-mcp` version and re-check this page
    after upgrades. It is safe to use — reads are read-only and writes are
    human-approved (below) — but treat the exact schemas as unstable.

## How access and safety work

- **It uses your existing auth.** `atlas-mcp` authenticates with a normal atlas
  **bearer token**. The assistant sees exactly what that credential is allowed to
  see — the same tenant isolation (row-level security) and role checks as the web
  UI. Give the assistant a scoped, revocable token, not your most privileged one.
- **Reads are read-only.** The read tools only call `GET` endpoints. Evidence
  reads never return raw evidence payloads.
- **Writes are never unattended.** Every write tool files a **proposal**; nothing
  reaches your canonical records until a human approver confirms it (see
  [The write-approval flow](#the-write-approval-flow)). This is enforced by a
  database constraint, not just by convention — an AI cannot publish an
  audit-binding change on its own.

## What the assistant can do

### Read tools

| Tool                 | Answers questions like…                                           |
| -------------------- | ----------------------------------------------------------------- |
| `list_controls`      | "List my active controls and their framework mappings."           |
| `get_control`        | "Show me control IAC-06 and where its evidence stands."           |
| `list_risks`         | "What risks are in the mitigate treatment?" (filter by treatment) |
| `get_risk`           | "Show risk R-14 with its linked controls and residual score."     |
| `list_evidence`      | "What evidence landed in the last 30 days?" (never raw payloads)  |
| `list_audit_periods` | "Which audit periods exist and which are frozen?"                 |

Every list tool returns up to 100 rows by default; pass `limit=N` (max 500) to
widen. There is no "return everything" option — a request over 500 is rejected.

### Write tools (human-approved)

| Tool                    | Proposes…                                                    |
| ----------------------- | ------------------------------------------------------------ |
| `create_risk`           | A new risk (draft)                                           |
| `update_control_state`  | A control-state override (recorded as evidence)              |
| `push_evidence`         | A new evidence record                                        |
| `update_risk_treatment` | A change to a risk's treatment and owner                     |
| `confirm_write`         | **Approver-only** — approves a pending proposal, applying it |

## The write-approval flow

1. The assistant calls a write tool (say `create_risk`). Instead of writing to
   your risk register, it files a **proposal** in the `mcp_write_proposals`
   table with `state = ai_proposed`, `ai_assisted = true`, `human_approved = false`.
2. Nothing is in your canonical data yet. The proposal is visible to approvers in
   the web UI and via the assistant.
3. An authorized approver (a credential with approver or admin rights) confirms —
   either by the **Approve** button in the web UI, or by the assistant calling
   `confirm_write` with the proposal ID. Only then does the platform apply the
   change, inside a single transaction.
4. A database CHECK constraint (`mcp_wp_ai_assist_invariant`) blocks any attempt
   to mark a proposal approved without recording who approved it. This is the
   database-level peer to the platform's [AI-assist boundary](https://github.com/mgoodric/security-atlas/blob/main/CLAUDE.md).

Each credential may hold at most **5 pending proposals** at once; a sixth is
rejected until you approve or reject the existing ones.

## Setup

### 1. Build the binary

`atlas-mcp` currently ships from source:

```bash
go build -o /usr/local/bin/atlas-mcp ./cmd/atlas-mcp
```

### 2. Get a bearer token

Use a scoped atlas API credential (see
[Tenant membership & credentials](tenant-membership.md)). Prefer a token limited
to what the assistant needs, and one you can revoke independently.

### 3. Provide the token — never on the command line

`atlas-mcp` reads the token from an environment variable or a file. **There is no
`--token=<value>` flag**, because command-line flags are visible to every process
on the host (`ps`), which would leak the token.

=== "Environment variable"

    ```bash
    export ATLAS_BEARER_TOKEN="<your-bearer-token>"
    export ATLAS_BASE_URL="https://atlas.example.com"   # defaults to http://localhost:8080
    atlas-mcp
    ```

=== "Token file"

    ```bash
    echo "$YOUR_BEARER_TOKEN" > ~/.config/atlas/mcp-token
    chmod 600 ~/.config/atlas/mcp-token
    atlas-mcp --token-file ~/.config/atlas/mcp-token --base-url https://atlas.example.com
    ```

The token is read once at startup; to rotate it, restart the client so it spawns a
fresh `atlas-mcp` subprocess.

If your operators run write tools against a local model, set the optional
`ATLAS_MCP_AI_MODEL_NAME` and `ATLAS_MCP_AI_MODEL_VERSION` variables — the write
tools record that (name, version) pair on every proposal for the audit log.

### 4. Connect your assistant

=== "Claude Desktop"

    Add to your `claude_desktop_config.json`
    ([location by OS](https://modelcontextprotocol.io/quickstart/user)):

    ```json
    {
      "mcpServers": {
        "security-atlas": {
          "command": "/usr/local/bin/atlas-mcp",
          "args": ["--base-url", "https://atlas.example.com"],
          "env": { "ATLAS_BEARER_TOKEN": "your-bearer-token-here" }
        }
      }
    }
    ```

=== "Claude Code"

    ```bash
    claude mcp add security-atlas /usr/local/bin/atlas-mcp \
      --env ATLAS_BEARER_TOKEN=your-bearer-token-here \
      --env ATLAS_BASE_URL=https://atlas.example.com
    ```

Restart the assistant. Ask it _"list my top risks"_ to confirm the connection.

## Security notes

- The token never appears on the command line (env or file only).
- Reads carry `User-Agent: atlas-mcp/<version> (mcp; ai_assisted=read-only)`; writes
  carry `(mcp; ai_assisted=write)`, so your audit aggregators can tell them apart.
- The MCP server holds no state and adds no new mutation path — it is a thin veneer
  over the same authenticated HTTP API the web UI uses.
- Because tokens grant real access, treat the assistant like any other client that
  holds a credential: scope it, rotate it, and revoke it when a teammate leaves.

## Reference

The developer-facing reference — exit codes, the exact per-call log envelope, and
the design rationale — lives in [`cmd/atlas-mcp/README.md`](https://github.com/mgoodric/security-atlas/blob/main/cmd/atlas-mcp/README.md)
and the decision logs for [slice 172 (server)](https://github.com/mgoodric/security-atlas/blob/main/docs/audit-log/172-mcp-server-decisions.md)
and [slice 173 (write tools)](https://github.com/mgoodric/security-atlas/blob/main/docs/audit-log/173-mcp-write-tools-decisions.md).
