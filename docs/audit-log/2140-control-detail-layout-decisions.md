# #2140 — Control detail layout

Ticket: [#2140](https://github.com/mgoodric/security-atlas/issues/2140)
(parent [#2072](https://github.com/mgoodric/security-atlas/issues/2072)).
Mirrors #2138 (risk detail: shared header, a strip, named sections,
per-section errors, `<Person>`, `enumLabel`, the raw-value scan). Builds
on #2137 (evidence detail), #1800 (org mappings), #1788 (SoA exclusions)
and #2174 (module-aware link sections).

detection_tier_actual: none
detection_tier_target: playwright

No bug surfaced during the work. Two raw values the new raw-value scan
targets were fixed while building it rather than caught by it: the
evidence row printed the result key (`pass`) and an attesting user's id
(`user/<uuid>`), and the header printed the rolled-up result key.

## D1. Three tabs: Overview, Framework map, History

The ticket makes the overview the page: the state strip, then Evidence,
Coverage and Linked. Those sections hold what the Evidence, Mappings,
Effective scope, Policies and Risks tabs held, so those five tabs are
retired, not kept as duplicates. The graph moves to `?tab=map`; History
keeps the evaluation history and takes the freshness clock, which reads
the same evaluated state. A retired `?tab=` key is not a key, so an old
link lands on the overview, which now carries that content. The count
chips went with the tabs they counted.

## D2. The strip owns the state readout

Result and "last evaluated" moved from the header actions into the strip
(`ControlStateStrip`), with the attestation wait (#2111) they depend on,
so the page reads the state once. Testids are kept
(`control-state-result-value`, `control-last-evaluated-value`); the
result now reads as a label ("Fail", not `fail`) with the key in
`data-result`. Freshness is the stalest cell, ranked as the controls
list ranks it. The KPI strip (effectiveness, frameworks, evidence count,
in-scope frameworks) is retired: the strip states the control's
position, and Coverage lists the frameworks themselves.

## D3. The exception marker rests on state or an active exception

The strip shows "Exception" when any scope cell evaluated `excepted`, or
an active exception is filed against the control. A caller the platform
refuses the exceptions read still sees the marker from the state they
may read, without the exception's details. Coverage shows "Covered by
exception" on the same state signal; the evaluated state, not the
exception list, is what changed the credit.

## D4. Exceptions are read through the existing list, filtered on the server

`GET /v1/exceptions?control_id=` (the BFF already forwards `control_id`).
OPA admits exceptions reads to admin, grc_engineer and auditor; viewer
and control_owner are refused. The page does not guess from roles: it
asks, and a 403 removes the part with no error. Any other failure shows
the part's own error. The part is also left out, and never fetched, when
the Exceptions module is off for the caller (`useSeesPage`, #2174).

## D5. SoA exclusions come from the control coverage read

`GET /v1/controls/{id}/coverage` now sets `exclusion` on each requirement
the tenant's activated framework-scope revision excludes, with the same
shape `GET /v1/requirements/{id}/coverage` returns (#1788). One lookup
per distinct requirement inside one tenant transaction: an anchor
reaches tens of requirements. The field is omitted when there is none,
so the anchor-requirements route that shares the wire type is unchanged.
A batched query was not worth a new sqlc query at that size.

## D6. Write affordances follow the live `/v1/me`

Upload new version (controls write) and Request exception (exceptions
write) show on `is_approver`: OPA admits both to admin and grc_engineer.
Evaluate now and Attest also show for a live `control_owner` role, whom
OPA admits on exactly those two paths; the handler then checks the
assignment. Nothing shows while `/v1/me` loads or when it fails.
Download bundle is a read and shows to everyone. The #2581 caveat
applies: `is_approver` also counts the credential approver flag, which
can be wider than OPA's role set. The platform still refuses the write.

## D7. The AI explanation renders only when there is one

The gap explanation ("Why this state") shows once, under the strip, and
only when the platform returned an explanation. Loading, a failed read
and a suppressed generation render nothing. The evidence summary
follows the same rule inside Evidence. The degraded notes the cards used
to show are gone from the page; the strip carries the deterministic
state and freshness the gap card's rollup line repeated.

## D8. `DetailHeader` gains an `actions` slot

The control's actions (Attest, Evaluate now, Download bundle, Upload new
version, Request exception, and the inline upload form) are not an Edit
link, one lifecycle step or overflow items. `DetailHeader` takes them in
a new `actions` slot after the back link; every other adopter is
unchanged.
