# Slice 031 — Monthly board brief — decisions log

Slice type: **AFK**. Per the slice-development workflow, the subjective
build-time calls below were made by Claude and recorded here rather than
blocking the merge on a human sign-off. The maintainer iterates
post-deployment. None of these touch the constitutional AI-assist boundary —
that is about how the _shipped product behaves at runtime_; this is about
_how the slice was built_.

---

## Decisions made

### D1 — `board_briefs` is append-only (two-policy RLS), not mutable

The board brief is a PINNED, IMMUTABLE snapshot (AC-5; P0 anti-criterion
"Does NOT permit edit of a pinned snapshot — new brief = new snapshot").
Modelled append-only by construction: `tenant_read` (SELECT) + `tenant_write`
(INSERT) policies only, no UPDATE/DELETE policy under FORCE ROW LEVEL
SECURITY. The explicit absence of mutation policies makes immutability
_structural_ — `atlas_app` has no SQL path to mutate a brief row — rather
than application-enforced. Mirrors slice 013 `evidence_audit_log`, slice 026
`aggregation_rule_evaluations`, slice 030 `decisions_audit`, slice 036
`artifact_access_log`.

**Confidence: high.** This is the established codebase pattern for
append-only tables and the issue's anti-criterion makes immutability a hard
requirement.

### D2 — Posture is computed live at generation time, not time-travelled

The platform has no historical posture store — there is no per-day
materialized posture-by-framework table. The "pin" is the _immutability of
the stored row_: the structured metrics are frozen into `content` (JSONB) at
generation time and read back verbatim on every fetch. `period_end` _labels_
the brief; it does not drive a point-in-time query.

This matches the issue's own phrasing — it calls invariant 10 an "analog"
("brief is a snapshot, immutable after generation"), not a literal
audit-period freeze. A literal time-travel query would require either a
posture history table (out of scope for this slice) or replaying the
evidence ledger as-of `period_end` (heavyweight, and slice 028's audit-period
freezing already owns the as-of-replay concern for _audit_ artifacts, not
board reporting).

**Confidence: medium-high.** Revisit if a future slice adds a posture-history
read model — the Generator could then accept an as-of horizon.

### D3 — Per-framework posture is the program's tenant-wide numbers, labeled per framework

Canvas §7.5 describes posture as a "coverage + freshness composite per
framework". True per-framework control attribution requires walking the SCF
anchor graph (framework requirement → SCF anchor → tenant control) and
intersecting with each framework's `framework_scopes` predicate — the
slice-008 + slice-018 machinery. That is heavyweight to assemble correctly
inside a once-monthly brief generator.

v1 reports the _program's_ tenant-wide coverage / freshness / drift numbers
once, listed against each registered framework (one `FrameworkPosture` row
per `frameworks` catalog entry). The brief is honest about this: it states
the program posture and names every framework the program runs against. The
narrative reads "We are in audit-ready state for SOC 2 (94% coverage ...)"
where 94% is the program number.

**Confidence: medium.** This is a deliberate v1 simplification. Slice 032
(quarterly board pack) is the natural place to do true per-framework
attribution — it already has "investment-vs-coverage per framework" in
scope. When that lands, the `FrameworkPosture` shape is already
per-framework, so the change is internal to the Generator, not a wire-format
break.

### D4 — Risk "age" uses `updated_at` as the age-since-treatment proxy

Canvas §7.5 says top risks are "sorted by residual × age-since-treatment".
The `risks` table (slices 002 / 019 / 020 / 053) has no treatment-applied
timestamp column — `updated_at` is the closest available signal. A risk's
`updated_at` advances on every PATCH, so it is a reasonable proxy for "how
long since this risk was last touched", which correlates with
age-since-treatment for the common case (a risk is touched when its
treatment changes).

**Confidence: medium.** Revisit when/if a `treatment_applied_at` (or
per-treatment-transition timestamp) column lands on `risks`. The Generator's
`rankTopRisks` is the single place that computes age — a one-line change.

### D5 — PDF is rendered on demand, not stored

The brief renders deterministically from the frozen `content` +
`narrative_md`. The `GET /v1/board-briefs/{id}/pdf` endpoint re-renders on
demand via the existing chromedp path (`internal/policy/pdf` precedent — no
new go.mod dependency). Storing the PDF bytes in the row would bloat
`board_briefs` for no correctness gain: the frozen JSONB _is_ the snapshot;
the PDF is just one presentation of it.

**Confidence: high.** Storing derived presentation bytes alongside the
canonical structured data is a known anti-pattern.

### D6 — Top-risks query is board-package-owned, not a shared `risks.sql` change

Batch-24 context flagged that slice 066 (built in parallel) extends the
shared `ListRisks` query. To keep the two slices conflict-free, slice 031
adds its own date-bounded `ListRisksForBoardBrief` query in
`internal/db/queries/board_briefs.sql` rather than touching `risks.sql`. The
residual-severity extraction is done in Go (`extractSeverity`) rather than a
JSONB-path SQL expression — the `residual_score` JSONB shape is
methodology-dependent (slice 020 `score` field vs slice 053 `severity` field
vs slice 019 `likelihood`/`impact`), so a single SQL path would be brittle.

**Confidence: high.** Documented parallel-batch hygiene.

### D7 — `{id}.md` route shape

The Markdown download endpoint is `GET /v1/board-briefs/{id}.md` — chi treats
`{id}.md` as a param with a literal `.md` suffix. It is declared _before_ the
bare `/v1/board-briefs/{id}` so chi's declaration-order match within the same
method keeps it ahead. The alternative — `GET /v1/board-briefs/{id}/markdown`
— was rejected because the issue's AC-4 explicitly says ".md" and a
file-extension suffix is the more natural download affordance.

**Confidence: high.** Matches the issue's stated route shape.

---

## Revisit once in use

- **D2 / D3** — if a posture-history read model lands, give the Generator an
  as-of horizon and do true per-framework attribution. Slice 032 is the
  likely trigger.
- **D4** — swap the `updated_at` age proxy for a real treatment-applied
  timestamp if one is added to `risks`.
- **Narrative template** — the v1 `text/template` narrative is intentionally
  terse. If maintainers want richer prose, the template is a single
  compile-time constant in `internal/board/narrative.go`; it stays templated
  (no LLM) per AC-6.
- **PDF styling** — `buildBriefHTML` is a minimal print-friendly layout. If
  the board wants the mockup's exact visual (`Plans/mockups/board-pack.html`),
  the HTML builder is the single place to evolve.
