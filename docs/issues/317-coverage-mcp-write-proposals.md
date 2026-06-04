# 317 — Coverage lift — MCP write-proposals stack (2 packages)

**Cluster:** Quality
**Estimate:** 2d (2 packages, one feature surface)
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Surfaced during slice 312's round-3 coverage audit, captured per the continuous-batch policy. The audit at `docs/coverage-audit-2026-05-round-3.md` identified 2 untracked MCP write-proposals packages below 70% merged coverage:

| Package                          | Unit-only % | Merged % | Statements |
| -------------------------------- | ----------- | -------- | ---------- |
| `internal/api/mcpwriteproposals` | 0.0         | 0.9      | 108        |
| `internal/mcp/writeproposals`    | 0.0         | 1.8      | 218        |

Both belong to the MCP (Model Context Protocol) write-proposals feature added by slices ~199-220 (MCP server family). `internal/api/mcpwriteproposals` is the HTTP handler; `internal/mcp/writeproposals` is the engine. Grouped because they form one cohesive feature surface that should be lifted together.

**Disposition:** `unit-add` + `integration-enrollment`

**Notes:** MCP-server testing is more complex than standard HTTP handler testing because the MCP protocol layer mediates everything. Need to read `internal/mcp` (slice ~199 foundation — already at 82.8% merged) for the test patterns.

## What ships in this slice

1. **Enroll the 2 packages in CI's `tests-integration` job**.
2. **New unit tests** for both packages — `mcpwriteproposals` HTTP handler pre-DB branches + `writeproposals` engine pure-Go state machine (approve / reject / merge transitions, proposal lifecycle).
3. **Floor lifts in `cmd/scripts/coverage-thresholds.json`** — add 2 new entries at `floor(merged_measured - 2pp)` each.

## Acceptance criteria

- [ ] **AC-1.** Both packages enrolled in CI's `tests-integration` job package list.
- [ ] **AC-2.** Both packages reach ≥ 70% merged coverage.
- [ ] **AC-3.** Each new test file's first comment block names load-bearing functions + branches covered.
- [ ] **AC-4.** `coverage-thresholds.json` adds 2 new floors at `max(0, floor(measured - 2pp))` each.

## Constitutional invariants honored

- **Testing discipline (CLAUDE.md).** Ratchet contract.
- **Slice 069 methodology.** Floor at `max(0, floor(measured - 2pp))`.
- **AI-assist boundary.** Write-proposals are the human-in-the-loop gate for MCP-suggested changes — testing them is testing the gate itself. No vacuous tests.

## Dependencies

- **#312** (round-3 coverage audit + lift) — `ready`.
- The MCP foundation slices (~199+) must be `merged`.

## Anti-criteria (P0 — block merge)

- **P0-317-1.** Does NOT raise any floor without writing the unit tests + integration enrollment.
- **P0-317-2.** Does NOT lower any existing floor.
- **P0-317-3.** Does NOT modify `_STATUS.md` from inside this slice's own commits.
- **P0-317-4.** Does NOT bundle `cmd/atlas-mcp` work — that binary stays exempt-leaning per the slice 312 audit's tier doctrine.

## Notes for the implementing agent

Read `internal/mcp` test patterns (already at 82.8% merged — slice ~199) for the MCP-server test idioms before starting. The slice 290 / 291 / 293 / 297 / 310 PRs are the reference HTTP-handler enrollment pattern.
