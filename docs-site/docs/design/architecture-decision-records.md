# Architecture Decision Records (ADRs)

security-atlas records load-bearing design decisions as Architecture Decision
Records in the repository, not in this docs site, because each ADR is a
contributor-and-reviewer artifact that lives alongside the code and migration
trail it governs. The canvas (`Plans/`) holds the _resolved invariant_ as the
daily reference; the ADR holds the _trade-off context and the rejected
alternative_ — the "why this and not that" a diligence reviewer needs.

ADRs live at
[`docs/adr/`](https://github.com/mgoodric/security-atlas/tree/main/docs/adr) in
the repository. The records below cover the four load-bearing architecture
invariants (CLAUDE.md "Architecture invariants") — the constraints that bound
every other decision.

## Pillar invariant ADRs

| Invariant                                                   | ADR                                                                                                                                  | Records                                         |
| ----------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------ | ----------------------------------------------- |
| #6 — Tenant isolation via PostgreSQL Row-Level Security     | [ADR-0011](https://github.com/mgoodric/security-atlas/blob/main/docs/adr/0011-rls-tenant-isolation.md)                               | RLS at the DB layer, deny-on-missing-context    |
| #2 — Append-only evidence ledger, ingestion/eval separated  | [ADR-0012](https://github.com/mgoodric/security-atlas/blob/main/docs/adr/0012-append-only-evidence-ledger.md)                        | immutable ledger, point-in-time replay          |
| #1 — UCF graph, one control / N framework satisfactions     | [ADR-0013](https://github.com/mgoodric/security-atlas/blob/main/docs/adr/0013-ucf-graph-one-control-n-satisfactions.md)              | STRM-typed edges through SCF anchors            |
| #4 / #5 — Multidimensional scope + FrameworkScope intersect | [ADR-0014](https://github.com/mgoodric/security-atlas/blob/main/docs/adr/0014-multidimensional-scope-frameworkscope-intersection.md) | tuple-space cells, effective-scope intersection |

## Earlier ADRs

The repository also carries tactical decision records (framework-scope
lifecycle, bearer-token storage, audit-period-freeze hash inputs, the OAuth
authorization server, the contract-test tier, OSCAL export signing, and
others). Browse the full set at
[`docs/adr/`](https://github.com/mgoodric/security-atlas/tree/main/docs/adr).
