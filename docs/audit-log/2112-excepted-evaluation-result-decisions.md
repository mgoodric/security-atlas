# 2112 — Excepted evaluation result — decisions log

Build-time calls made for issue #2112 (the evaluation engine reads active
exceptions; a covered cell records `excepted`). Parent: #2071, finishing the
record lifecycles.

- detection_tier_actual: none
- detection_tier_target: none

(No bug surfaced. The GLOSSARY's Exception entry claimed `excepted` before any
code wrote it; this issue makes the claim true.)

## Decisions made

### D1 — `excepted` joins the `evidence_result` enum; evidence cannot carry it

`control_evaluations.result` reuses the slice-002 `evidence_result` enum, so the
value is added there (`20261101000000`). Every existing value is kept. Moving
the column to TEXT + CHECK would have rewritten every sqlc row type that reads
it for no gain. Because the enum is shared with `evidence_records.result`, a new
CHECK (`evidence_records_result_not_excepted`) keeps `excepted` out of the
ledger: an exception is a property of an evaluation, never of a piece of
evidence. The push validators (REST, gRPC, CLI, MCP) already allow-list the four
evidence values, so `excepted` is rejected before it reaches the CHECK.

One file, not the two that #1746 needed: both CHECKs compare `result::text`, so
neither parses the new value as an enum literal inside the transaction that
added it. Verified with `psql --single-transaction -f` on a migrated cluster.

### D2 — The no-evidence coherence CHECK is widened

`control_evaluations_no_evidence_coherent` required `result = 'inconclusive'`
for a `no_evidence` row. An excepted cell with no evidence is `excepted` +
`no_evidence`, so the CHECK now accepts either value. It accepts every row it
accepted before.

### D3 — The overlay is applied last and holds whatever the evidence said

An active exception covering the cell replaces `pass`, `fail`, `na` and
`inconclusive` alike. The ticket says the covered cell "gets the new result
`excepted`". The underlying result is not stored on the row; recording it
would be a column of its own and is not in scope.

### D4 — "In force" is `[effective_from, expires_at)` on the evaluation clock

The engine reads rows in status `active` (`ListExceptionsByControl`, inside the
evaluation's tenant-GUC transaction, so RLS binds the read) and honours one only
while `effective_from <= now < expires_at`. It does not wait for the daily
auto-expiry job: an exception past `expires_at` stops applying at once, which
is what "after expiry, the next evaluation returns to normal" needs. Any status
other than `active` (today `expired`; a later revoked state) is not read at all,
so revocation needs no engine change.

The clock is the evaluation's `now`, not the `asOf` horizon. The exceptions
table is mutable state, not a ledger, so a replay at a past `asOf` cannot
reconstruct which exceptions were active then. Replay therefore applies the
exceptions active at replay time. This is a known limit of AC-7's
"identical state from the ledger" for excepted cells; an exception-history
read model would lift it.

### D5 — The whole-tenant row is covered only by an always-true predicate

A control that resolves to no scope cells is evaluated once with a NULL cell.
Only a predicate that matches every cell (`{}`, `null`, empty or
`{"op":"true"}`) covers that row. A narrower predicate cannot say which part of
the tenant it means, and evaluating it against an empty dimension map would let
`not` predicates match by accident.

### D6 — A malformed predicate fails the control's evaluation

The write path canonicalizes predicates, so a malformed one is a defect. It
returns an error (EvaluateAll logs it and continues with the other controls)
rather than silently treating the cell as not covered — the same fail-loud rule
the engine applies to unsupported query languages.

### D7 — Rollup rank: below `fail` and `inconclusive`, above `pass`

The worst-state rollups (`ListActiveControlsWithState`,
`ListSCFAnchorsLatestWithState`, `ListSCFAnchorsForVersionWithState`) rank
`fail` > `inconclusive` > `excepted` > `pass` > `na`. Before this change
`excepted` fell to the `ELSE 0` branch, so a control with one excepted cell and
one passing cell would have rolled up `pass`. Ranking it above `pass` keeps a
waived control from ever reading as passing; ranking it below `inconclusive`
keeps a cell with unknown state as the louder signal.

### D8 — Never a pass, anywhere it is counted

Every reader that counts a pass compares `result = 'pass'` (SQL) or
`== ResultPass` (Go), so `excepted` is excluded without a code change:
the drift snapshot (`ListPassingControlsForDay`, hence #2063's
`computeGains`/`computeFlips`), effectiveness (`PassRate`), residual-risk
coverage, `program_effectiveness`, the SoA contradiction read, and the board
pack / OSCAL open-finding reads (which select `fail`). The drift integration
test now pins it. Consequence: an exception that starts on a passing control is
reported as a flip out of passing, which is accurate — the control is no longer
demonstrating itself.

### D9 — Display

The controls list renders the raw result enum as its State pill. `excepted`
alone gets a display label, "Excepted", and the `info` badge hue, never the
pass hue. It is also a State filter option.
