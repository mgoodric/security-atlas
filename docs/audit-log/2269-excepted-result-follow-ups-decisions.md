# 2269 — Excepted result follow-ups — decisions log

Build-time calls made for issue #2269, the follow-ups from the review of
#2228 (#2112, the `excepted` evaluation result).

- detection_tier_actual: integration
- detection_tier_target: integration

(The stale-cell defect named in the ticket was confirmed by
`TestFrameworkPosture_ExceptedCellThatStopsApplyingDoesNotCount`, which fails
on the #2112 query and passes on this one.)

## D1 — "Currently applies" means "in the control's latest evaluation run"

Applicability is computed in Go (`scope.Store.ControlApplicability`
evaluates `applicability_expr` over the cell universe), so SQL cannot
re-derive it. Each evaluation run writes one row per cell the expression
resolves to at that moment, sharing one `eval_run_id`. `control_excepted_now`
therefore counts only rows of the control's latest run: a cell that stopped
applying has no row in it. The bound tracks applicability as of the last
run; the hourly scheduler and the #2269 exception-change trigger keep that
recent. The same rule is applied 90 days back (`control_latest_run_then`).

The other worst-state rollups that take the latest row per (control, cell)
are left as they are; the ticket names `control_excepted_now` only.

## D2 — OSCAL: observations plus back-matter resources, reconstructed exceptions

An excepted cell becomes an AR observation with `risk-accepted=true`,
`observation-source=exception` and the control, cell and evaluation ids. It
links (`rel=related`, `href=#<exception id>`) to one back-matter resource per
exception, whose uuid is the exception id and whose props carry expiry,
approver, approval time and status. This is OSCAL's own in-document link
target; each exception appears once however many cells it covers. The
observation's `expires` is the earliest linked exception's `expires_at`.
Nothing is emitted as a finding or a POA&M item, and failing evaluations are
untouched.

The evaluation row does not record which exception excepted it (#2112 D3),
so the export reconstructs it: an exception of the same control that was
activated by the evaluation's instant, not revoked by it, inside
`[effective_from, expires_at)`, and whose predicate covers the cell by the
engine's own rule (`eval.ExceptionCovers`, extracted from `exceptedIn`). A
cell whose exception cannot be resolved (cell deleted, exception gone) still
gets an observation, which says so, rather than vanishing from the AR.
Adding an `exception_id` column to the append-only ledger was the
alternative; it would leave every pre-#2269 row unresolvable anyway and is
not needed while the reconstruction is exact.

Schema validity is proven by the bridge's trestle round-trip
(`round_trip_validate("assessment-results", …)`) in
`test_serialize_assessment_emits_excepted_cells_as_risk_accepted_observations`.

## D3 — Board sentence: both ends, stated together

The posture query gains `covered_by_exception_then_pct`: requirements whose
every covering control that counted at the cutoff rested on an exception
then. The pack averages it over the scoped frameworks the same way the
baseline is averaged. The coverage-trend sentence discloses both shares
whenever either is non-zero ("averages 70% (10% … covered only by
exception) … against a baseline of 65% (5% by exception)"), a zero included,
so the two ends can be compared. With no exception anywhere the sentence is
unchanged. An operator-entered baseline carries no exception share, so
`baseline_coverage_pct` clears it and the baseline clause is dropped. The PDF
coverage card states the same figures. Packs stored before #2269 decode
unchanged (all new fields are `omitempty`).

There is no golden file for the pack narrative (the only board golden is the
monthly brief's HTML, which this change does not touch); the sentence is
pinned by exact-string unit and integration tests instead.

## D4 — Re-evaluation: after commit, outside the request, no actor

`exception.Store.WithReevaluate` and `Expirer.WithReevaluate` take a
`func(ctx, tenantID, controlID)` built by `eval.ExceptionChangeReevaluator`,
so the exception package does not import the engine. Activate and Revoke
call it after `transition` returns (the transaction has committed), in a
goroutine on a context detached from the request; the expiry sweep calls it
synchronously after each tenant's commit, once per distinct control. A
failure is logged, not returned: the state change has already committed and
the scheduler re-evaluates the control within the hour.

The runs record trigger `exception_change` (migration `20261243000000`
widens `control_evaluations_trigger_chk`) and no `triggered_by`, which #2479
keeps to manual runs. Approval and denial do not re-evaluate: neither changes
what the engine reads. An activation with a future `effective_from` records
the control's current state; the exception applies from the first run after
it comes into force.

## D5 — Tooltip

The dashboard tile's `title` states D11's rule: requirements whose every
covering control rests on an exception, where one excepted scope cell is
enough for a control to count.
