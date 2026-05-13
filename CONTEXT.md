# security-atlas — Domain Glossary

> Canonical domain terms. When code or documentation uses one of these terms, this is the meaning. When in doubt, this file wins.

This file is created lazily as terms are resolved during design work. Most of the canon lives under `Plans/canvas/` — this is the short index plus the precise definitions that don't have a single-paragraph home there.

## Coverage (slice 008)

The graph-traversal result that answers "what is the relationship between a framework requirement and a tenant's controls?" — produced by joining `framework_requirements → fw_to_scf_edges → scf_anchors → controls.scf_anchor_id`. Always:

- **Two-hop, not recursive** — canvas §3 fan-out is bounded (req maps to 1–6 anchors; anchor maps to 1–8 reqs; controls anchored 1:1). No recursive CTE is needed; index-backed JOINs suffice.
- **Strength-aware** — each `(requirement, anchor)` row carries the STRM `relationship_type` + `strength` from `fw_to_scf_edges` (canvas §3.2). The handler returns these verbatim; weighted-sum coverage is computed upstream (slice 012's dashboard/eval territory).
- **Effectiveness-free in v1** — the `effectiveness` field that canvas §3.3 mentions for the dashboard is **deferred to slice 012**. Slice 008 returns anchors + controls, not effectiveness numbers. The wire format omits the field rather than emitting null, so slice 012 can add it without a breaking change.
- **`no_relationship` edges filtered out** — STRM stores "confirmed no overlap" as data (canvas §3.2). Coverage responses exclude these; they're surfaced only in the mapping-inspector UI (canvas §10), not the coverage view.

Coverage queries hit three routes:

- `GET /v1/requirements/{id}/coverage` — given a framework requirement (UUID, `slug:version:code`, or `slug::code`), list anchors + controls + edges.
- `GET /v1/anchors/{id}/requirements` — given an SCF anchor (UUID or scf_id), list satisfied framework requirements (DB-backed replacement of the slice-006 in-memory placeholder).
- `GET /v1/controls/{id}/coverage` — given a tenant control (UUID), list the framework requirements its anchor satisfies.

All three accept optional `?framework_version=slug:version` to pin historical mappings. `?as-of=<timestamp>` and `?scf_release=<version>` are accepted-and-no-op in v1; slice 012 / future SCF-release-import work will activate them.

**RLS interaction:** the catalog tables (`framework_requirements`, `fw_to_scf_edges`, `scf_anchors`) have no `tenant_id` and no RLS — they're platform-bundled and global. Only `controls` is tenant-scoped. A traversal across tenant boundaries returns the (global) requirement + anchors but an empty controls list — this is the correct shape (canvas §3.5) and is enforced by Postgres RLS, not by app code. The handler MUST NOT add `WHERE tenant_id = ?` to any query (constitutional invariant 6).

## Exception (slice 021)

A time-bounded, scope-bounded waiver of a control's normal evaluation. Always:

- **Scoped** — applies only to scope cells matching `scope_cell_predicate` (slice-017 JSON-AST shape; reuses `scope.Evaluate`).
- **Time-bounded** — `expires_at` is required, max **365 days** from creation. **Auto-renewal is forbidden** (P0 anti-criterion).
- **Logged** — every state transition writes one row to `exception_audit_log` (append-only). Auto-expiry is not silent.

States (canvas §6.3):

- `requested` — initial state. Set by `POST /v1/exceptions`.
- `approved` — governance approval recorded. `approved_by` populated. **Approval is not the same as activation** — the effect doesn't take hold until `active`.
- `denied` — terminal. To revisit, file a new exception.
- `active` — the effect is in force. A control × scope cell with an active exception evaluates as `excepted` (not `fail`) in downstream dashboards (slice 020 consumer).
- `expired` — terminal. Set by daily auto-expiry job when `expires_at < now()` for a row in `active`. Reverts control evaluation to normal.

Allowed transitions:

```
requested → approved   (approver-role required; segregation of duties: approver != requester)
requested → denied     (approver-role required; segregation of duties: approver != requester)
approved  → active     (operator action; sets effective_from)
active    → expired    (system; daily cron tick)
```

No other transitions. `denied` and `expired` are terminal.

**`compensating_controls`** is a `TEXT[]` — free-form descriptions of what's being done instead. NOT an FK to `controls` (because compensating mitigations are often informal: "weekly manual review by SRE on-call until IAM federation lands"). A future slice can add `compensating_control_ids UUID[]` if a structured link becomes useful.

**Segregation of duties** — `approved_by` MUST differ from `requested_by`. The same credential cannot both file and approve an exception.

**Calendar surface** — `GET /v1/exceptions/expiring?within=30d` powers the "Upcoming items" dashboard panel (canvas §6.3, dashboard mockup).

## License posture (slice 050)

The project is licensed **Apache 2.0** — the canonical instance of the "permissive license" the canvas §1.2 thesis requires. Permissive matters because the platform is designed to be embedded in commercial deployments (the disqualification of OpenGRC at canvas §1.2 turns specifically on its CC BY-NC-SA license being incompatible with that goal). Copyleft alternatives (AGPL) were considered and rejected because they would block the same embedded-in-commercial-deployments use case the platform targets. Open-question #3 (`Plans/canvas/11-open-questions.md`) is resolved by slice 050.

Bundling posture for third-party catalogs (CLAUDE.md "Licensing constraints"):

- **SCF** — free standard license, but slice 050 does NOT bundle pre-built SCF data in release artifacts (open-question #1 resolution, consistent with slice 006's "users import their own" model).
- **CCM / CAIQ / SIG** — never bundled; opt-in import only. The platform ships the machinery, the operator provides the file.
- **HECVAT** — free; bundleable when a slice has a reason to.
- **OpenGRC code** — never copied; CC BY-NC-SA is incompatible with our license. Concepts and patterns may inform our own implementation.
