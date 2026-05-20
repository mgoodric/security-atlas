# Slice 172 — MCP server foundation + read-only tools — decisions log

**Slice type:** `JUDGMENT`. Four open design calls were explicitly delegated
to the implementing engineer by the slice doc's "Notes for the implementing
agent" section. This log records each call, the alternatives weighed, and
the tiebreaker. None of these blocked merge; the maintainer iterates
post-deployment.

## Context

The slice doc fixes the WHAT (six read-only MCP tools wrapping existing
HTTP endpoints) and the WHY (Claude Desktop / Claude Code operators today
copy/paste data into chat or curl). It deliberately delegates four HOW
calls — D1 transport, D2 framework, D3 tool surface, D4 audit strategy
— because each has at least one credible alternative and the trade-offs
are not load-bearing on the rest of v1.

All four anti-criteria interactions (P0-A4 User-Agent, P0-A9 list caps,
P0-A11 no platform-binary bundling, P0-A12 advisory CI) constrained the
decision space; the chosen path honors all 13 P0 entries verbatim.

## Decisions made

### D1 — Transport: stdio (chosen) vs HTTP+SSE

**Chose: stdio.**

Both Claude Desktop and Claude Code launch MCP servers as local
subprocesses with bearer credentials passed via env. That's the dominant
deployment shape today; HTTP+SSE is the multi-host story for a future
"shared MCP gateway" use case the slice doc explicitly defers.

- _stdio_ wins on:
  - **Auth surface.** Bearer-token-per-process bound to one tenant.
    Same posture as the existing CLI + push SDK. No new wire-auth code
    to threat-model.
  - **Network surface.** Zero. No TLS termination to harden, no
    listener to firewall.
  - **Failure mode.** Process exits → MCP client sees EOF → user
    restarts. Simple.
  - **Debuggability.** stdin/stdout are inspectable by hand; a
    `printf '{"jsonrpc":"2.0"...}' | atlas-mcp` smoke test is one
    line in CI.
- _HTTP+SSE_ wins on:
  - **Multi-host.** Operator can run one MCP server in their k8s
    cluster and have N clients connect. Not a v1 use case for the
    target solo operator persona.
  - **Live updates.** SSE supports server-push of resource changes
    (e.g., evidence freshness flips). The six tools in scope are
    pure request/response; no push needed.

**Tiebreaker.** Anti-criterion P0-A12 ("advisory until 30-day soak")
binds this PR to a low-risk profile. stdio's zero-network-surface
posture is the safer first step. HTTP+SSE is filed as a v2 follow-on
when multi-host demand surfaces (no slice number yet — the maintainer
files when the demand materializes).

**Confidence: high.** The slice doc itself recommended stdio with
rationale; this engineer agrees and adopts.

### D2 — MCP framework: hand-rolled stdlib JSON-RPC (chosen) vs `mark3labs/mcp-go` vs `modelcontextprotocol/go-sdk`

**Chose: hand-rolled stdlib JSON-RPC 2.0 implementation.**

The slice doc named two viable Go MCP libraries:

- `github.com/mark3labs/mcp-go` — Apache 2.0; ~3k stars; popular.
- `github.com/modelcontextprotocol/go-sdk` — MIT; official Anthropic
  SDK; smaller adoption; newer.

Both ship the full MCP protocol surface (resources, prompts, tools,
sampling, completion, notifications). This slice uses **only** three
message types: `initialize`, `tools/list`, `tools/call`. The
hand-rolled implementation lives in `internal/mcp/jsonrpc.go` and is
~250 LoC of pure stdlib.

- _Hand-rolled_ wins on:
  - **P0-A11 fidelity.** Anti-criterion P0-A11 says the MCP framework
    dependency MUST NOT be bundled into the platform binary. With
    security-atlas's single-module workspace (`go.work` = `use .`),
    pulling either named library into go.mod adds the dep to every
    build target — platform, CLI, OSCAL bridge, openapi gen. A
    sibling go.mod under `cmd/atlas-mcp/` was considered (the slice
    doc explicitly permits this) but compounds CI complexity (two
    modules, two `go test ./...` invocations, two `go mod tidy`
    targets, two pre-commit hooks). Hand-rolling sidesteps the
    whole question and is the cleanest honor of P0-A11.
  - **Surface area.** Three MCP message types out of ~15 in the spec.
    A library buys us nothing for tools-only servers — the
    `notifications/cancel`, `resources/*`, `prompts/*`,
    `sampling/*`, `completion/*`, `logging/*` surface is dead code.
  - **Stability.** MCP spec churn (v0.1 → v1.0 in 2025) was real;
    pinning to a single dependency exposes us to their migration
    cadence. Hand-rolled lets us pin to the JSON-RPC 2.0 standard
    (frozen since 2010) + the small MCP subset we use.
  - **Threat surface.** Every transitive dep of a 3rd-party MCP
    framework is a supply-chain risk. Hand-rolled = zero new
    transitive deps.
  - **Read-only constraint amplifies the win.** This slice ships
    NO server-pushed notifications, NO resource subscriptions, NO
    streaming. The MCP types we need are all request/response.
- _Library_ wins on:
  - **Spec drift coverage.** If MCP v2 changes `initialize`'s
    handshake fields, the library author tracks it. We'd track it
    ourselves. Tradeoff: small surface = small re-track work.
  - **Documentation.** The library README is the operator's
    primary onboarding. Our README has to do that lift.
  - **Slice 173 (writes) might need more surface.** Possible. If
    writes need notifications/cancel, hand-rolled extends; if
    they need a new transport, we revisit. Today's six read tools
    don't.

**Tiebreaker.** P0-A11 ("does NOT bundle the MCP framework dependency
into the platform binary") was decisive. The single-module workspace
plus six tools plus the stable JSON-RPC 2.0 wire format made
hand-rolling not just acceptable but actively preferable. We document
the MCP subset we implement against in `cmd/atlas-mcp/README.md`'s
"Protocol coverage" section so future authors know what's intentionally
absent.

**Confidence: high.** Marcus Webb / battle-scarred read: I have seen
this exact pattern before — pull in a heavy framework for a small surface
and inherit their roadmap, their breaking changes, their dep tree. For
six read-only tools over a stable JSON-RPC standard, the long-term
right call is hand-rolled. If slice 173 (writes) needs notifications,
we revisit then with concrete pressure.

### D3 — Tool surface: ship exactly the six listed (chosen) vs add 1-2 more

**Chose: ship exactly the six listed.**

The slice doc lists:

1. `list_controls(framework_id?, scope?)`
2. `get_control(anchor_id)`
3. `list_risks(scope?, status?)`
4. `get_risk(risk_id)`
5. `list_evidence(control_id?, kind?, freshness?)`
6. `list_audit_periods(status?)`

The engineer is explicitly permitted to add 1-2 more non-admin read
tools if the operator use-case is compelling. Candidates evaluated:

- `list_policies` — useful for board-prep narrative ("policies acked
  this quarter"); declined because the v1 backlog already includes
  full policy reporting via web UI (slice 022 + 062 + 098); MCP
  consumers can ask for evidence-of-policy-acknowledgment rather
  than policy metadata.
- `list_exceptions` — useful for audit prep; declined because the
  exceptions endpoint (slice 042) requires admin-or-grc_engineer
  role gates that don't map cleanly to a 1:1 tool wrapper; admin
  tools require their own threat-model pass per P0-A10.
- `get_evidence(record_id)` — sibling of `list_evidence`; declined
  because `list_evidence` already supports the discovery use case
  and `get_evidence` would multiply the payload-redaction surface
  (P0-A5) for marginal LLM value.

**Tiebreaker.** "Each addition is an AC, a test, a schema snapshot."
Six is enough to prove the foundation. Slice-174 (the spillover
number) is the right home for additional read tools once an LLM
operator surfaces a real use case the six don't cover.

**Confidence: medium.** Six is conservative; we could plausibly ship
eight. Conservative-now keeps the surface auditable in 30-day soak.

### D4 — Audit strategy: User-Agent header (chosen) vs per-call meta-audit row

**Chose: User-Agent header on every outbound HTTP request.**

The MCP server makes HTTP calls to the platform's existing endpoints
(no new endpoints; tool definitions are 1:1 with HTTP routes). The
question is: how does the platform-side audit log distinguish MCP
traffic from CLI / browser / SDK traffic?

- _User-Agent header_ (chosen): the MCP server sets
  `User-Agent: atlas-mcp/<version> (mcp; ai_assisted=read-only)`
  on every outbound request. The platform's existing structured
  access log captures the User-Agent into the per-request log
  field. Future server-side aggregator (slice 124 already exists)
  can build an "MCP-originated reads" dashboard from this field
  without a per-tool audit-log write.
- _Per-call meta-audit row_ (rejected for reads): each MCP tool call
  emits a row into `me_audit_log` (slice 135) tagged with
  `actor_type=mcp`, `tool_name=<name>`, `caller_user=<sub>`. This is
  the right pattern for **writes** (slice 173 — every audit-binding
  write commits an audit row alongside the data row, as the
  existing platform pattern). For **reads**, adding a row per call
  doubles the write load on the audit log for what is structurally
  identical to the existing HTTP access log — duplicative.

**The asymmetry matches the existing platform pattern:** reads don't
write audit rows by default (no GET endpoint writes to `me_audit_log`);
writes do (every POST / PATCH / DELETE writes a row). Carrying this
asymmetry into MCP keeps the platform's mental model consistent.

**Tiebreaker.** P0-A4 mandates the User-Agent header anyway. Choosing
"User-Agent only" simply means we don't ALSO write a per-call audit
row. The header gives the platform-side aggregator everything it needs
for a "MCP-originated reads" dashboard (slice 124-style).

**Confidence: high.** Slice 173 (writes) will add the per-call audit
row pattern for write tools — the boundary is exactly where the
existing platform's read/write asymmetry lives.

## Implementation choices NOT in the JUDGMENT scope

These are mechanical resolutions of the slice doc's WHAT, recorded for
the slice-173 engineer's benefit (so write-tool implementation has the
same vocabulary).

### Result-cap defaults

Default 100, max 500 (P0-A9). Both are configurable via the tool
input schema's `limit` field; bad input rejected at tool-input
validation.

### Schema snapshot location

`internal/mcp/testdata/<tool_name>.golden.json` — one file per tool;
each captures the tool's input + output JSON Schema as the canonical
shape. The schema-stability test (AC-15) gates regression. New fields
are additive (extend the snapshot); removed fields fail CI.

### Goroutine hygiene

Each `tools/call` opens an `http.Request` with a context derived
from the request lifetime; `resp.Body.Close()` is `defer`-ed
immediately; `io.LimitReader(resp.Body, 1<<20)` caps per-response
bytes to bound the LLM's context-flood risk. `-race` clean test
gates this.

### Bearer-token handling

Read from env (`ATLAS_BEARER_TOKEN`) or from a file path passed via
positional argument (`atlas-mcp --token-file /path/to/token`).
**Never** via `-t<token>` (visible in `ps`) — P0-A1. Loaded at
startup, never re-read; rotation = restart the MCP subprocess.

### CI integration test posture

Per slice 116's advisory-vs-required pattern, this slice's
integration tests under `internal/mcp/` are NOT added to the
canonical CI integration-test package list at this iteration. They
run via `go test -tags=integration ./internal/mcp/...` locally and
via a NEW advisory CI step (`Go · integration (MCP advisory)`). 30-day
soak before promotion to required-checks.

## Spillovers filed

None. Six tools is the contract; additional reads file as slice 174
when operator demand materializes. Slice 173 (writes) is already
filed and gated on this slice's merge.

## P0 anti-criteria audit

| P0     | Compliance                                                                                                                                       |
| ------ | ------------------------------------------------------------------------------------------------------------------------------------------------ |
| P0-A1  | Credentials only via `ATLAS_BEARER_TOKEN` env or `--token-file <path>` — never as a `--token=<value>` flag. Enforced in `cmd/atlas-mcp/main.go`. |
| P0-A2  | Each tool's wire shape mirrors the canonical column set of its source endpoint. No MCP-only wide columns.                                        |
| P0-A3  | Tool inputs pass through stdlib `encoding/json` strict decoding (rejects unknown fields); UUID / enum / cap-bound validators reused.             |
| P0-A4  | `User-Agent: atlas-mcp/<version> (mcp; ai_assisted=read-only)` set in `internal/mcp/client.go`'s round-tripper.                                  |
| P0-A5  | `list_evidence` wire shape excludes `payload_json` — uses the existing `?include_payload=false` query param semantics from slice 145.            |
| P0-A6  | Tool wire shapes carry only canonical-allowlist fields; no `token_hash`, `password*`, `secret*`, `*key_material`.                                |
| P0-A7  | Stderr logging limited to one line per tool call: `mcp tool=<name> duration_ms=<n> status=<ok\|err>`. No bodies.                                 |
| P0-A8  | HTTP 429 / Retry-After surfaces to LLM as a tool error with the Retry-After value preserved; no silent retry loop.                               |
| P0-A9  | Default `limit=100`, max `limit=500`. Tool-input validation rejects out-of-range values.                                                         |
| P0-A10 | Exactly six tools shipped. No admin endpoints. No write tools.                                                                                   |
| P0-A11 | Hand-rolled JSON-RPC; zero MCP framework dep added to go.mod. Platform binary's dep tree unchanged.                                              |
| P0-A12 | `cmd/atlas-mcp` is NOT added to required-checks. Advisory CI only. Soak plan: 30 days before re-evaluation.                                      |
| P0-A13 | Test tokens use neutral `test-*` prefix (e.g., `test-atlas-mcp-bearer`). No vendor prefixes.                                                     |
