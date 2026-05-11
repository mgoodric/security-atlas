**security-atlas canvas** · [← index](../ARCHITECTURE_CANVAS.md)

---

# 7. Metrics and Posture

## 7.1 KPIs

| KPI                     | Type      | Definition                                                                                                               |
| ----------------------- | --------- | ------------------------------------------------------------------------------------------------------------------------ |
| Control coverage        | Lagging   | `(active_controls_with_at_least_one_passing_evidence_record_in_freshness_window) / (active_controls_with_applicability)` |
| Evidence freshness      | Leading   | `% of (control, scope_cell) tuples with evidence inside freshness window`                                                |
| MTTR-control            | Lagging   | Median time from `control_state=fail` to `control_state=pass`                                                            |
| Drift count             | Leading   | `(controls passing yesterday) − (controls passing today)`, signed                                                        |
| Exception inventory     | Leading   | Open exceptions by aging bucket; expiring-in-30-days highlighted                                                         |
| Audit readiness index   | Composite | Weighted blend per framework — coverage × freshness × open-finding-burndown                                              |
| Policy attestation rate | Lagging   | % required acknowledgments completed in window                                                                           |
| Vendor risk burndown    | Lagging   | High-criticality vendor reviews on time                                                                                  |

## 7.2 Leading vs lagging

Leading: drift count, evidence freshness, expiring exceptions, expiring policy acknowledgments. They predict the next audit.

Lagging: coverage, MTTR-control, audit findings closed, attestation rate. They report what already happened.

We display them on separate dashboards — mixing them lets execs misread the program.

## 7.3 Aggregation across scopes

KPIs are computed per scope cell, then aggregated up scope dimensions. The same KPI can be sliced by BU, environment, geography, or cloud account. The aggregation operator is explicit per KPI (sum vs weighted average vs worst-cell).

## 7.4 Benchmarks / peer comparison

In OSS, we don't have proprietary peer data. We support **opt-in anonymized telemetry** (off by default) that contributes to community benchmarks — control coverage distribution by framework, MTTR-control distribution, evidence freshness percentiles. This is not Vanta's "you're in the 78th percentile" feature, but a credible OSS approximation.

## 7.5 Board reporting (first-class)

Practitioner research surfaced this as an underserved JTBD: **no GRC tool produces a board-ready narrative** — every CISO rebuilds the deck quarterly in Google Slides. IANS Research (March 2026) reports 34% of CISOs say boards dismiss security warnings out of hand and only 29% of board directors describe cybersecurity updates as very effective ([IANS](https://www.iansresearch.com/resources/all-blogs/post/security-blog/2026/03/24/boards-give-ciso-cybersecurity-reporting-a-mixed-grade)). This is a v1 feature, not a v3 one.

**The board pack** is generated as PDF + editable Markdown/HTML for paste into the deck of choice. It is _not_ a slide-rendering engine — that's overreach. It produces:

| Section                 | Source                                                          | Auto-drafted narrative                                                                                         |
| ----------------------- | --------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------- |
| Posture summary         | Coverage + freshness composite per framework                    | "We are in audit-ready state for SOC 2; ISO 27001 readiness at 78%, gap concentrated in A.8 asset management." |
| Top risks aging         | Risk register, sorted by residual × age-since-treatment         | "Three high-residual risks are open >90 days: ..."                                                             |
| Control coverage trend  | Last 90 / 180 / 365 days, per framework                         | "Control coverage rose from 71% to 84% in the quarter; one regression in CC8.1 driven by ..."                  |
| Open findings burndown  | Audit findings + POA&M                                          | Trend chart + median-time-to-close.                                                                            |
| Phishing / training     | Training connector (v2; manual upload v1)                       | "97% phishing pass rate, target ≥95%."                                                                         |
| Patching cadence        | Vuln-scanner integration                                        | "Median P1 patch time 4 days, target ≤7."                                                                      |
| Incident response       | IR ticketing integration                                        | "Two incidents in the period; both contained <SLA."                                                            |
| Vendor risk burndown    | Vendor module                                                   | "12 of 14 high-criticality vendor reviews on time."                                                            |
| Investment vs. coverage | Manually entered tool/headcount cost vs. control coverage delta | "Q investment: $X; coverage delta: +Y points." Critical for board narrative; no GRC tool does this today.      |
| Asks of the board       | Editable freeform                                               | Solo operator drafts; tool does not write asks.                                                                |

**Auto-drafted narrative** uses templated language (Jinja-style) over the metrics, with optional LLM polish on a per-section, human-in-the-loop basis. We never publish auto-narrative without one-click human approval. This is the explicit boundary against the "AI-generated audit response" anti-pattern.

**Business-impact-in-dollars** (ALE — Annualized Loss Expectancy) is supported for the top N risks that use the FAIR methodology. Below the top N, we present qualitative bands. Practitioners report this is the actual board-trusted format.

**Deck cadence:** monthly briefs (single page, posture + drift + top-3 risks) and quarterly full pack. Both are pinned snapshots — the board is reading what posture _was_ at the report date, even if the live state has changed.

---

[← Canvas index](../ARCHITECTURE_CANVAS.md) · [← 6. Risk Register](./06-risk.md) · **Next:** [8. Audit Workflow →](./08-audit-workflow.md)
