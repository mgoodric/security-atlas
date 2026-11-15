# Issue #2091 — Sweep the remaining destructive-variant uses

The destructive variant stays only on failing states (error alerts, a
failed or partly rejected import, a red metric threshold, an unmet CSF
target), overdue states, and the confirm step itself. Every other use moves
to a neutral or semantic variant. Style only: no button changes what it does
and no confirm is added or removed.

- `detection_tier_actual`: none
- `detection_tier_target`: none

## D1 — A trigger that opens a confirm goes neutral; the confirm stays red

**Decision.** The demo teardown button and the vendor delete trigger become
`outline`. Their dialogs' confirm buttons stay `destructive`. The vendor
edit page's delete box loses its red border and tint for the same reason.

**Why.** The confirm step is where the irreversible choice is made, so it is
the one place red carries information. A red trigger next to a red confirm
says the same thing twice.

## D2 — Buttons that fire without a confirm go neutral, not into RowActions

**Decision.** Settings › Sessions "Revoke", the risk detail "Detach" and the
OSCAL claim "Reject" become `outline`; the questionnaire review queue's
"Reject draft" gets neutral border classes. None of them gains a confirm.

**Why.** Moving them into `RowActions` would add a confirm step, which is a
behaviour change the dispatcher ruled out for this ticket. Each is
recoverable (a session can sign in again, a link can be re-attached, a claim
or draft re-disposed), so a neutral button is the honest weight.

## D3 — Status badges that are not failures use the semantic family

**Decision.**

| Badge                                    | Was           | Now        |
| ---------------------------------------- | ------------- | ---------- |
| Vendor register criticality `high`       | `destructive` | `critical` |
| Policy status `retired`                  | `destructive` | `outline`  |
| OSCAL claim status `rejected`            | `destructive` | `outline`  |
| SoA `contradiction`                      | `destructive` | `warning`  |
| Dashboard upcoming: expiring `exception` | `destructive` | `warning`  |

Overdue vendors and overdue upcoming items keep `destructive`.

**Why.** Criticality is a severity, which the `critical` token exists for.
Retired and rejected are terminal decisions, not failures. A contradiction
and an expiring exception ask for attention but have not failed.

## D4 — Notice callouts move to `info`

**Decision.** The cloud LLM routing banners (`cloud-routing-banner`, the
checklist's `cloud-banner`) and the one-time-secret callouts
(`fresh-secret-callout`, `settings-fresh-token-callout`) become `info`.

**Why.** They are notices, not errors. The routing banner stays visible on
every cloud-routed surface, which is what the AI-assist boundary requires;
only its colour changes.

## D5 — Left for open work

The vendor detail page (`vendors/[id]/page.tsx`: the overdue status badge and
its `CriticalityBadge`) belongs to #2102 (PR #2250). The risk form and risk
filters belong to #1817. Neither is touched here. Required-field asterisks
and inline field-error text use `text-destructive` as a class, not the
variant, and are out of scope.
