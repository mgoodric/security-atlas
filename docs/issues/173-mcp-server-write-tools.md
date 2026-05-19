# 173 — MCP server write tools (create / update operations)

**Cluster:** Backend / Infra (AI integration surface)
**Estimate:** 3-5d
**Type:** JUDGMENT
**Status:** `not-ready`

## Narrative

Surfaced during slice 172 design via `/idea-to-slice`, captured as follow-up per continuous-batch policy.

**WHY.** Slice 172 ships the MCP server foundation + six read-only tools. The maintainer's full feature ask is read AND write — write surface is decomposed into this slice because:

1. Write tools introduce a fundamentally different threat surface (LLM-driven mutation of audit-binding artifacts).
2. The AI-assist boundary (CLAUDE.md "AI-assist boundary (hard)") explicitly forbids publishing audit-binding artifacts without one-click human approval. Write tools that touch audit-binding state (risks, controls, evidence, decisions) MUST integrate with a human-approval workflow.
3. Sizing each surface separately keeps tracer-bullet discipline: foundation + reads (slice 172) is one vertical; writes (this slice) is another.

**WHAT (when this slice is picked up).** Add MCP write tools to the slice-172 server. Initial set:

- `create_risk(title, description, owner, scope, ...)` — files a draft risk into the register
- `update_control_state(anchor_id, state)` — proposes a control-state change
- `push_evidence(control_id, kind, payload)` — appends an evidence record (uses the same write surface as the push SDK from slice 003)
- `update_risk_treatment(risk_id, treatment, treatment_owner)` — proposes a treatment narrative + owner change

**Critical contract — AI-assist boundary integration:**

- Tools that produce audit-binding artifacts (anything that an external auditor would later cite) MUST go through a HITL approval flow. Recommended pattern: the MCP write tool creates a DRAFT row tagged `ai_assisted=true, human_approved=false, ai_approver=null`. A separate operator action (in the web UI or via a dedicated `approve_*` tool that the human operator invokes explicitly) flips `human_approved=true` + records the approver. The audit-log row records both the AI draft and the human approval.
- Tools that do NOT produce audit-binding artifacts (e.g., setting a personal-preference flag) MAY commit directly.
- Schema-level enforcement (per CLAUDE.md "AI-assist boundary"): `ai_assisted=true` rows MUST NOT have `human_approved=true` without `human_approver` set.

**SCOPE DISCIPLINE.** This slice is the full write surface; do NOT split write tools across multiple slices unless engineer surfaces a conflict-safety reason at design time. Do NOT expose admin-tier writes (no `delete_tenant`, no `delete_user`, no `revoke_credential`) — those require a separate slice with explicit RBAC review.

## Threat model

Will be re-run when this slice is picked up. Slice 172's STRIDE analysis covers the read surface; write surface requires its own pass — every category gets new threats:

- **Spoofing**: bearer-token mutation is now scoped — caller must have write permission AND the AI-assist boundary applies
- **Tampering**: LLM-driven prompt injection now reaches the WRITE surface; mitigation = HITL approval gate
- **Repudiation**: every write MUST emit a meta-audit row with `ai_assisted=true` flag + caller user ID + tool name
- **Information disclosure**: writes echo their inputs into audit logs; same redaction rules apply
- **Denial of service**: write quota is more expensive than read; per-(tenant, user) write-cap tighter than read-cap (slice 145 concurrency cap applies + a stricter write-specific limit)
- **Elevation of privilege**: write tools MUST gate on OPA-evaluated write permissions, not just possession of the bearer token

Verdict (preliminary): **HOLD-pending-review** — engineer re-runs STRIDE at impl time + maintainer reviews the HITL approval flow design before merge.

## Acceptance criteria (placeholder)

Will be expanded when this slice is picked up. Skeleton:

- AC-1..AC-4: four write tools defined (create_risk, update_control_state, push_evidence, update_risk_treatment)
- AC-5: every write commits a meta-audit row with `ai_assisted=true`, `actor_id`, `tool_name`, `tool_input_hash`
- AC-6: audit-binding writes (risk, control state, evidence) MUST go through HITL approval — the write tool creates a draft; a separate `approve_*` tool (or web UI action) commits
- AC-7: cross-tenant test: write tools as tenant A's user cannot create / update tenant B's rows
- AC-8: write-quota integration test: 10 concurrent writes against cap → quota exceeded surfaces as tool error
- AC-9: schema-snapshot test for write tool I/O
- AC-10: integration with slice 172's stdio transport (no transport-layer changes; same server binary)
- AC-11..AC-15: per-tool happy + sad paths
- AC-16: decisions log captures HITL approval design + write-quota choices

## Constitutional invariants honored

- **AI-assist boundary (hard)**: the LOAD-BEARING invariant for this slice. No audit-binding artifact ships without one-click human approval. Schema-level enforcement (`ai_assisted=true → human_approved` requires `human_approver`).
- **#6 Tenant isolation via RLS**: writes hit the existing platform write endpoints; RLS gates them.
- **#9 Manual evidence is first-class**: `push_evidence` MCP tool produces evidence rows identical in shape to manual-form and connector-pushed evidence.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.6 — AI-assist boundary discussion
- `Plans/canvas/04-evidence-engine.md` §4.6.5 — schema-level enforcement (`ai_assisted=true` ↔ `human_approver` invariant)
- `CLAUDE.md` "AI-assist boundary (hard)" — constitutional ledger

## Dependencies

- **#172** (MCP server foundation + read tools) — gated on this merging.
- **#003** (push SDK + evidence write path) — `merged`; `push_evidence` MCP tool wraps the same surface.
- **#034** (auth) — `merged`.

## Anti-criteria (P0 — placeholder, expanded at pickup)

- **P0-A1.** Does NOT bypass the AI-assist boundary. Every audit-binding write MUST require human approval before commit.
- **P0-A2.** Does NOT expose admin-tier writes (no tenant / user / credential mutations).
- **P0-A3.** Does NOT relax slice 172's anti-criteria (User-Agent header still required; column allowlist still enforced).
- **P0-A4.** Schema-level invariant test: `ai_assisted=true AND human_approved=true AND human_approver IS NULL` MUST be impossible at the DB level (check constraint OR trigger).
- **P0-A5.** Write quota: stricter than read; per-(tenant, user) cap ≤ 1 in-flight write at a time (default; configurable).

## Skill mix (3-5)

1. **Engineer** — primary; MCP write tool handlers + HITL approval flow + DB-level constraint
2. **Architect** — consulted on HITL approval flow design (it's a new UX surface in the web UI)
3. **Security** — fresh STRIDE pass at impl time; new threat surface
4. **Designer** — IF the HITL approval surface needs a new UI page or modal (consulted as needed)

## Notes for the implementing agent

**Provenance.** Surfaced during slice 172 design via `/idea-to-slice`, captured as follow-up per continuous-batch policy. The maintainer asked for read AND write MCP tools; slice 172 ships read, this slice ships write.

**The HITL approval flow is the load-bearing design decision.** Three viable patterns to evaluate at pickup:

- **Pattern A (recommended starting point)**: AI write creates a draft row with `state='ai_proposed'`; a separate operator action (web UI button + a `confirm_*` MCP tool) flips state to `state='active'` and records the approver. Simple; matches the existing exception-approval flow shape.
- **Pattern B**: AI write commits directly to a `_proposals` shadow table; operator's review flow merges proposals into the canonical table. More plumbing; cleaner audit trail.
- **Pattern C**: AI write is rejected at the platform layer when the request carries `ai_assisted=true` AND `human_approved=false`; operator must explicitly "co-sign" the tool call in their MCP client before it commits. Highest UX friction; tightest safety.

Engineer evaluates + the maintainer picks at design-grill. This slice doc is a STUB — the full slice gets fleshed out at pickup time (when slice 172 is merged + the operator's HITL approval-flow preference is known).

**Re-run STRIDE at impl time.** This slice's preliminary threat model is HOLD-pending-review until a fresh pass + maintainer sign-off.
