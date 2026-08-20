# Operator skills for the security-atlas MCP server

These are ready-made **Claude skills** for a security team that has connected the
[security-atlas MCP server](../docs-site/docs/mcp.md) to their assistant. Each one
turns a recurring program task ("brief me on my top risks", "which controls have
stale evidence", "how ready are we for the audit?") into a single command, driven
by the MCP server's read tools.

They are optional and self-contained — the MCP server works without them. Skills
just save your team from re-explaining the same workflow each time.

## What's here

| Skill                   | Ask it to…                                                   | Tools used                                                                                     |
| ----------------------- | ------------------------------------------------------------ | ---------------------------------------------------------------------------------------------- |
| `atlas-risk-briefing`   | Summarize your top open risks, ranked, with treatment status | `list_risks`, `get_risk`                                                                       |
| `atlas-freshness-sweep` | Find controls whose evidence is stale or missing             | `list_controls`, `list_evidence`                                                               |
| `atlas-audit-readiness` | Produce an audit-readiness snapshot for the current period   | `list_audit_periods`, `list_controls`, `list_evidence`, `list_exceptions`, `list_action_plans` |

All three are **read-only** — they only query your program. If a briefing suggests
a change (say, re-treating a risk), the assistant uses the MCP **write tools**,
which file a _proposal_ you approve before anything is committed. Nothing is
mutated unattended. See the [MCP server guide](../docs-site/docs/mcp.md) for the
approval flow.

## Install

Prerequisite: the [atlas-mcp server](../docs-site/docs/mcp.md) is connected to your
assistant.

=== "Claude Code"

    Copy the skills into your Claude skills directory:

    ```bash
    cp -r skills/atlas-* ~/.claude/skills/
    ```

    Then invoke one by name, e.g. `/atlas-risk-briefing`, or just ask in plain
    language ("give me a risk briefing") — Claude matches on the skill's
    `USE WHEN` triggers.

=== "Claude Desktop / other MCP clients"

    Claude Desktop does not yet load `SKILL.md` files directly. Use these as
    **prompt recipes**: paste a skill's body as your message, or save it as a
    project instruction. The MCP tools it references work the same way.

## A note on scope

These skills read whatever your MCP bearer token is allowed to read — the same
tenant isolation as the web UI. Give the assistant a scoped, revocable token
(see [Tenant membership & credentials](../docs-site/docs/tenant-membership.md)),
not your most privileged one.
