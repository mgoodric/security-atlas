# 172 — MCP server foundation + read-only tools

**Cluster:** Backend / Infra (new AI integration surface)
**Estimate:** 3-4d
**Type:** JUDGMENT
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

**WHY.** Surfaced 2026-05-19 via `/idea-to-slice` from a maintainer feature ask: "add an MCP server component that allows me to both read data out of this system and update/create data in the system." MCP (Model Context Protocol) is the emerging standard for AI assistants to invoke tools against external systems via a typed contract. Today an operator using Claude Desktop or Claude Code against an atlas deployment must either copy/paste data into the chat OR ask the LLM to call HTTP curl commands (lossy, unsafe, no schema). A typed MCP surface fixes both gaps for the operator's day-to-day use cases (board prep, audit prep, control-state lookup, evidence review).

The maintainer's full ask is read AND write. Read is the foundational tracer-bullet — six small, audit-binding-free read tools that prove the auth + tenancy + transport story. Write is a separate vertical slice (filed as spillover slice 173) gated on this foundation merging AND the AI-assist boundary (CLAUDE.md "AI-assist boundary (hard)") being honored at the tool-handler level.

**WHAT.** Build the MCP server entrypoint + six read-only tools that wrap the existing platform HTTP API. The MCP server is a separate Go binary (`cmd/atlas-mcp/`) that authenticates to the platform via a bearer token (same auth surface as the CLI + push SDK). It does NOT touch the database directly — it is a tool-protocol veneer over the existing platform API, identical pattern to how the CLI consumes the same endpoints.

Read tools (six, foundational):

1. `list_controls(framework_id?, scope?)` — UCF anchors + framework satisfactions
2. `get_control(anchor_id)` — single control + scope + current state
3. `list_risks(scope?, status?)` — risk register (RLS-filtered to caller's tenant)
4. `get_risk(risk_id)` — single risk + linked controls + treatment summary
5. `list_evidence(control_id?, kind?, freshness?)` — evidence records
6. `list_audit_periods(status?)` — audit period summary (open / frozen / closed)

Each tool's response uses canonical column sets (excludes redacted fields per slice 138's `payload_json` exclusion + slice 145's hardening defaults). Each tool authenticates as the caller (bearer token in env var) — tenant isolation comes from the platform's existing RLS gating, not from the MCP layer.

The MCP server ships as a stdio binary first (Claude Desktop / Claude Code consume it via local subprocess). HTTP+SSE remote transport is a v2 follow-on if multi-host use cases emerge.

**SCOPE DISCIPLINE — what's deliberately out:**

- Does NOT expose ANY write tools in this slice. Write surface is slice 173.
- Does NOT touch the database directly. MCP server is an HTTP client of the platform; never a DB client.
- Does NOT add tenant-switching (vCISO use case from slice 141). One MCP server process binds to one bearer token → one tenant. Multi-tenant MCP is a follow-on on top of slice 141.
- Does NOT bundle MCP framework wheels into the platform binary. The MCP server is a separate `cmd/atlas-mcp/` build target.
- Does NOT add MCP to required-checks. New surface; soak before promotion (slice 116 pattern).
- Does NOT ship an HTTP+SSE remote transport. Stdio only.
- Does NOT cache cross-call data in memory. Every tool call is a fresh HTTP request to the platform.
- Does NOT pre-bundle a Claude Desktop / Claude Code config — ship a README snippet operators copy into their config.
- Does NOT promote the MCP surface as a stability contract — it's experimental until v2 (semver caveat in README).

## Threat model

STRIDE pass. The MCP server introduces a NEW authenticated surface — every category produces real considerations.

**S — Spoofing.** MCP servers in stdio mode are launched by the operator's MCP client (Claude Desktop / Claude Code) as a local subprocess. The client passes credentials via env vars. Threat: a malicious or compromised MCP client launches `atlas-mcp` with a stolen bearer token; the server faithfully authenticates as the token owner. Mitigation: same posture as the existing CLI / push SDK — bearer-token possession IS authentication. No new mitigation needed at the MCP layer; existing token-rotation + token-revocation semantics from slice 034 apply. **Anti-criterion P0-A1**: the MCP server MUST NOT accept credentials via command-line flags (visible in `ps`); MUST read only from env or a file path argument.

**T — Tampering.** MCP tools accept input from LLMs which are prompt-injection susceptible. Threat: an attacker injects a prompt into a document the LLM is summarizing, causing the LLM to invoke `list_evidence` with crafted filters that leak data the operator didn't intend to expose. Mitigation: read tools don't WRITE anything — the threat surface is bounded to "what could an LLM passively exfiltrate from this tenant's data with this operator's credentials." That's bounded by the operator's existing RBAC + the canonical column allowlists. **Anti-criterion P0-A2**: every tool's response payload MUST pass through the same column allowlist used by the corresponding HTTP endpoint (no new "MCP-only" wide columns). **Anti-criterion P0-A3**: tool input validation MUST reject inputs the platform API rejects (don't relax constraints; reuse the platform's existing validators).

**R — Repudiation.** Every MCP tool call should leave an audit trail. Threat: an LLM-driven exfil leaves no record. Mitigation: read tools that hit `/v1/admin/audit-log/*` or other meta-audit-emitting endpoints get meta-audit rows naturally (per slice 124 + 145). Read tools that don't hit those endpoints (e.g., `list_controls`) currently leave no record — that's consistent with the existing HTTP API. **Anti-criterion P0-A4**: the MCP server MUST set a `User-Agent: atlas-mcp/<version> (mcp; ai_assisted=read-only)` header on every outbound request, so platform-side logs can distinguish MCP-originated traffic from CLI / browser / SDK traffic. Future server-side aggregator (slice 124 already exists) can build a "MCP-originated reads" dashboard from this header without a per-tool audit-log write.

**I — Information disclosure.** Highest-risk category. Tools return tenant data; LLM context retains it; LLM responses can echo it to other consumers of the same context. Threat: tenant A's risk register narrative ends up in an LLM session shared with tenant B's operator. Mitigation: bearer token scopes the MCP server to ONE tenant — RLS at the DB layer enforces this. Operator running multi-tenant Claude Desktop must launch multiple `atlas-mcp` subprocesses with distinct env vars. **Anti-criterion P0-A5**: no `payload_json` in any tool response (inherit slice 138's evidence-payload exclusion as default-on for MCP responses; slice 145's `?include_payload=false` is the analogue). **Anti-criterion P0-A6**: no secret-shaped columns (`token_hash`, `api_keys.token_hash`, anything matching `password|secret|key_material`) in any response. **Anti-criterion P0-A7**: no debug logging of tool inputs OR outputs to stderr beyond a one-line `tool_called name=X duration=Yms` envelope — full input/output bodies stay in the per-call HTTP request log on the platform side, never the MCP server's stderr (which is captured by the MCP client's session log).

**D — Denial of service.** LLM agents loop. Threat: a buggy LLM agent calls `list_controls` 1000 times in 30 seconds, saturating the operator's per-(tenant, user) request budget. Mitigation: slice 145's concurrency semaphore + the platform's existing per-tenant rate limits (TBD across the codebase) apply. **Anti-criterion P0-A8**: the MCP server MUST surface 429 / Retry-After responses from the platform back to the LLM as a tool error (don't retry silently); LLM agents that respect tool errors will back off. **Anti-criterion P0-A9**: every list-shaped tool MUST cap its result count (default 100, max 500) — no "give me all controls" footgun.

**E — Elevation of privilege.** Tools execute as the bearer-token's identity. Threat: a non-admin operator's MCP server gets used to invoke a tool that internally requires admin (current design: admin tools live under `/v1/admin/*` and gate at the platform). Mitigation: tool definitions are 1:1 with platform endpoints; non-admin endpoints expose non-admin data; admin endpoints (if added in v2) require admin tokens. **Anti-criterion P0-A10**: this slice's six tools all hit non-admin endpoints (`/v1/controls`, `/v1/risks`, `/v1/evidence`, `/v1/audit-periods`). Adding an admin-only tool requires its own design pass + explicit RBAC test.

**Verdict.** has-mitigations (S/T/R/I/D/E all produce real anti-criteria — 10 P0 entries below).

## Acceptance criteria

### Server foundation

- **AC-1.** NEW binary `cmd/atlas-mcp/main.go` builds via `go build ./cmd/atlas-mcp/` and produces a runnable `atlas-mcp` binary.
- **AC-2.** Server speaks MCP over stdio (newline-delimited JSON-RPC 2.0 per MCP spec). Verifiable via `printf '<handshake>' | atlas-mcp` returns the server's `initialize` response without error.
- **AC-3.** Auth via env var `ATLAS_BEARER_TOKEN`. Missing or empty → server fails fast at startup with a one-line stderr error and exit code 2.
- **AC-4.** Server URL via env var `ATLAS_BASE_URL` (default `http://localhost:8080`). Validated as `https?://...` at startup; bad URL → exit code 2.
- **AC-5.** Outbound HTTP requests include `User-Agent: atlas-mcp/<version> (mcp; ai_assisted=read-only)` (P0-A4).

### Read tools (six)

- **AC-6.** `list_controls(framework_id?, scope?)` tool defined with strict input schema; returns canonical control list; max 100 default / 500 cap (P0-A9).
- **AC-7.** `get_control(anchor_id)` tool defined; required-arg validation; returns one anchor or `not_found`.
- **AC-8.** `list_risks(scope?, status?)` tool defined; canonical column set; max 100/500.
- **AC-9.** `get_risk(risk_id)` tool defined; returns one risk + linked controls summary.
- **AC-10.** `list_evidence(control_id?, kind?, freshness?)` tool defined; **payload_json excluded** (P0-A5); max 100/500.
- **AC-11.** `list_audit_periods(status?)` tool defined; freeze-metadata columns included.

### Tests

- **AC-12.** Per-tool unit test: input-schema validation rejects bad input; valid input dispatches to the right platform endpoint.
- **AC-13.** Integration test: spin up the platform binary in a test harness; bearer-auth-as-tenant-A's-user; each tool returns tenant-A data only; explicit cross-tenant assertion (no tenant-B rows).
- **AC-14.** Goroutine-leak test: each tool call closes its HTTP body + releases per-call resources; `go test -race` clean.
- **AC-15.** Schema-stability test: each tool's input + output schemas are snapshotted into `internal/mcp/testdata/`; CI gates schema changes (additive only).

### Documentation

- **AC-16.** NEW `cmd/atlas-mcp/README.md`: install snippet, env-var reference, claude-desktop-config sample, claude-code-config sample, semver/experimental caveat.
- **AC-17.** CHANGELOG entry under `[Unreleased] / Added`.
- **AC-18.** Decisions log at `docs/audit-log/172-mcp-server-decisions.md`: D1 (transport stdio vs HTTP+SSE — pick stdio); D2 (framework choice — mark3labs/mcp-go vs modelcontextprotocol/go-sdk; license + maturity tradeoff); D3 (tool surface — six chosen entities + rationale); D4 (audit-log strategy — User-Agent header vs per-call meta-audit row).

## Constitutional invariants honored

- **#1 One control, N framework satisfactions.** `list_controls` returns canonical anchors with framework satisfactions as a join field, never per-framework duplicates.
- **#6 Tenant isolation enforced at the DB layer via RLS.** MCP server consumes the existing HTTP API which is RLS-gated; no new tenant boundary at the MCP layer.
- **#9 Manual evidence is first-class.** `list_evidence` returns both connector-pushed and manual evidence rows uniformly.
- **AI-assist boundary (hard).** This slice ships READ-ONLY tools — zero audit-binding artifacts produced. Slice 173 (writes) must require human approval per the boundary; that requirement is documented here so the spillover engineer doesn't miss it.
- **Test discipline (slice 069 four surfaces).** Go unit + Go integration + new schema-stability test surface.

## Canvas references

- `Plans/canvas/01-vision.md` §3 — replacement-grade GRC operator surface.
- `Plans/canvas/04-evidence-engine.md` §4.6 — AI-assist; the boundary applies; read tools are pre-boundary territory.
- `Plans/canvas/09-tech-stack.md` — Go for backend binaries; new `cmd/atlas-mcp/` target.
- `Plans/canvas/11-open-questions.md` #8 — AI-assistance boundary RESOLVED 2026-05-13; CLAUDE.md is the constitutional source.

## Dependencies

- **#034** (OIDC RP + bearer auth + api_keys) — `merged`. MCP server consumes the same bearer-token surface.
- **#003** (Evidence SDK + CLI shape) — `merged`. MCP server reuses the CLI's HTTP client patterns where possible.
- **#138** (ledger entities export — payload_json exclusion pattern) — `ready`. MCP `list_evidence` inherits the column-exclusion stance.
- **#145** (export hardening — concurrency cap + redaction patterns) — `merged`. MCP server inherits the rate-limit posture transparently via shared platform endpoints.

## Anti-criteria (P0 — block merge)

- **P0-A1.** MCP server MUST NOT accept credentials via command-line flags (visible in `ps` output). Env var or file-path argument only.
- **P0-A2.** Every tool's response payload MUST pass through the canonical column allowlist used by the corresponding HTTP endpoint. No "MCP-only" wide columns.
- **P0-A3.** Tool input validation MUST reject inputs the platform API rejects. Don't relax constraints; reuse validators.
- **P0-A4.** Every outbound HTTP request MUST carry the `User-Agent: atlas-mcp/<version> (mcp; ai_assisted=read-only)` header.
- **P0-A5.** Zero `payload_json` (or equivalent free-text content payload) in any tool response.
- **P0-A6.** Zero secret-shaped columns (`token_hash`, `api_keys.token_hash`, `password*`, `secret*`, `*key_material`) in any response.
- **P0-A7.** No debug logging of tool inputs or outputs to stderr beyond a one-line `tool_called name=X duration=Yms` envelope.
- **P0-A8.** 429 / Retry-After from platform MUST surface to the LLM as a tool error. No silent retries.
- **P0-A9.** Every list-shaped tool MUST cap result count: default 100, max 500. No "give me all" footgun.
- **P0-A10.** This slice ships ONLY the six listed non-admin tools. Adding any admin tool or any write tool MUST be a follow-on slice with its own threat-model pass.
- **P0-A11.** Does NOT bundle the MCP framework dependency into the platform binary. `cmd/atlas-mcp/` is a separate build target with its own `go.mod` if cleanest, or a separate module entry under the workspace.
- **P0-A12.** Does NOT promote `cmd/atlas-mcp` to required-checks. Stay advisory until 30-day soak per the slice 116 pattern.
- **P0-A13.** Does NOT use vendor-prefixed test tokens. Neutral `test-*` per slice 069 convention.

## Skill mix (3-5)

1. **Engineer** — primary; Go MCP framework integration + tool handler implementation + tests
2. **Architect** — consulted at design time for D1/D2/D3/D4 (transport, framework, tool surface, audit strategy)
3. **Security** — STRIDE pass already inline; live invocation NOT required unless surfacing during impl

## Notes for the implementing agent

**JUDGMENT D1 — Transport (stdio vs HTTP+SSE).**

- **stdio (recommended default):** simplest; Claude Desktop / Claude Code launch as subprocess; one bearer-token-per-process bound to one tenant; trivial to debug; no network surface to harden.
- **HTTP+SSE:** required for multi-host use cases (e.g., shared MCP server reachable by a team). Adds: TLS termination, server-side auth (which is the same bearer token but now over the wire), per-request rate limits (not just per-process). Out of scope for this slice; v2 follow-on if demand emerges.
- **Pick stdio.** Document the rationale in the decisions log.

**JUDGMENT D2 — MCP framework (Go).**

Two viable Go MCP libraries:

- `github.com/mark3labs/mcp-go` — Apache 2.0; mature; popular in the Go MCP ecosystem; ~3k stars at time of design.
- `github.com/modelcontextprotocol/go-sdk` — MIT; official Anthropic SDK; newer; tighter spec conformance but smaller adoption.

Engineer evaluates both at impl time. Default recommendation: **mark3labs/mcp-go** for maturity unless the official SDK has materially better ergonomics by the time this slice is picked up. Document the choice + tiebreakers in the decisions log.

**JUDGMENT D3 — Tool surface (six entities).**

The six listed (`controls`, `control_state`, `risks`, `evidence`, `audit_periods`) cover the operator's most-frequent read use cases per canvas §1. Engineer MAY propose additions (e.g., `list_policies`, `list_exceptions`) but each adds a tool definition + test + schema snapshot — explicit AC additions, not silent scope creep. If the engineer adds 2+ tools beyond the six, file the additions as a slice-174 spillover instead.

**JUDGMENT D4 — Audit strategy (User-Agent vs per-call meta-audit row).**

User-Agent header is the lightweight choice; per-call meta-audit row is the heavyweight. Recommended: **User-Agent only for read tools** (this slice); per-call meta-audit row for write tools (slice 173). This asymmetry matches the existing platform pattern (reads don't write audit rows by default; writes do).

**Provenance.** Surfaced 2026-05-19 via `/idea-to-slice` from a maintainer feature ask. Decomposed into primary (read-only, slice 172) + spillover (writes, slice 173) per the per-slice template's "one tracer-bullet surface per slice" rule. AI-assist boundary applies — see CLAUDE.md "AI-assist boundary (hard)".

**Spillover to file alongside this PR**: slice 173 — MCP server write tools (create_risk, update_control_state, push_evidence). Gated on slice 172 merge. The write surface MUST honor the AI-assist boundary: any tool that writes an audit-binding artifact requires HITL approval flow (one-click maintainer approve before the write commits). Spillover slice doc captures this contract.
