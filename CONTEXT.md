# security-atlas — Domain Glossary

> Canonical domain terms. When code or documentation uses one of these terms, this is the meaning. When in doubt, this file wins.

This file is created lazily as terms are resolved during design work. Most of the canon lives under `Plans/canvas/` — this is the short index plus the precise definitions that don't have a single-paragraph home there.

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
