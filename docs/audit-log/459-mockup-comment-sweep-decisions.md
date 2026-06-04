# 459 — decisions log: sweep `Plans/mockups/` provenance citations

- detection_tier_actual: none
- detection_tier_target: none

(No bug surfaced during the slice. This is a mechanical comment/doc sweep with no
runtime surface; the only verification surfaces are the compile/lint/test gates,
which a comment-only diff cannot regress.)

Parent: slice 437 (`git mv Plans/mockups/ → Plans/_archive/mockups/`). Sibling
collisions avoided: slice 438 (`internal/api/soc2import`), slice 448 (`web/app`).

## Decisions made

### D1 — Update-path vs rephrase: chose mechanical update-path

**Options:** (a) rewrite each `Plans/mockups/<page>.html` citation to the literal
new path `Plans/_archive/mockups/<page>.html`; (b) rephrase each to a path-free
phrasing ("the archived iteration-1 mockup").

**Chosen:** (a) — mechanical `s#Plans/mockups/#Plans/_archive/mockups/#g` on the
in-scope source comments.

**Rationale:** The citations carry **line numbers** (`board-pack.html lines
27-30`, `control.html lines 139-152`) that are still load-bearing for a developer
wanting to chase the exact design reference. A path-free rephrase would discard
that navigability — the whole point of the slice (AC-1: "so the provenance links
resolve"). Update-path preserves the line-anchored citation and makes it resolve
again. Confidence: **high**.

### D2 — `web/app/**` citations: FLAGGED, not edited (sibling-448 collision avoidance)

23 files under `web/app/(authed)/**` carry the same stale citation. The slice
spec AC-1 nominally scopes `web/**/*.{ts,tsx}`, but the batch directive carves
`web/app` out as sibling 448's surface and instructs flag-don't-edit to avoid a
merge collision. The directive is the more specific, more recent instruction and
wins over the spec's broad glob.

**Chosen:** left all 23 `web/app/**` files untouched; flagged them for a
follow-up sweep (see "Flagged for follow-up" below). Confidence: **high** (pure
process/collision-avoidance call; no design judgment).

### D3 — Dated historical records left verbatim

`CHANGELOG.md` prior entries, `docs/audit-log/**`, `docs/audits/**`,
`docs/issues/**`, `docs/design/**`, and the dated
`Plans/canvas/13-ui-mockup-audit-2026-05-16.md` audit all cite the pre-archive
`Plans/mockups/` path. These are **point-in-time facts** — they record where the
mockups lived when the record was written. `canvas/13` even carries an explicit
slice-437 "Path note" stating its references "are left verbatim as a historical
record." Rewriting them would falsify history.

**Chosen:** left all of them verbatim, consistent with slice 437's AC-4
conscious-leave clause and the slice-459 spec scope discipline. Confidence:
**high**.

### D4 — `manifest.test.ts:47` deliberately-invalid fixture left as-is

`web/e2e-audit/lib/manifest.test.ts:47` uses `mockupPath:
"Plans/mockups/dashboard.html"` as a **negative test case** — the test
"rejects a mockupPath that fails the regex shape" asserts the validator's regex
`/^[a-z0-9][a-z0-9./_-]*\.html$/` rejects a capital-`P`, slash-bearing path. The
canonical manifest stores bare filenames (`dashboard.html`); the directory is
resolved separately by `mockupsDir()` (already repointed to `_archive` by 437).

This string is therefore **not a provenance citation** — it is a fixture chosen
_because_ it is invalid. Rewriting it to `Plans/_archive/mockups/dashboard.html`
would still fail the regex (so the test would still pass) but for zero
navigability benefit, while changing a test fixture — which AC-2 forbids ("no
test-assertion change").

**Chosen:** left the fixture untouched. Confidence: **high**.

### D5 — CHANGELOG bullet added despite pure-comment churn

The directive allowed omitting a CHANGELOG bullet for pure-comment churn with no
user-facing effect, but recommended one if a docs file (`responsive-discipline.md`)
was edited. That docs file turned out to already be correct (see D6), so no docs
file was edited — the diff is purely code-comment churn.

**Chosen:** added a `### Changed` bullet anyway, for traceability of a deliberate
developer-navigability sweep in the slice-437 lineage. A one-line cost; keeps the
sweep auditable. Confidence: **medium** (defensible either way; erred toward
recording the work).

### D6 — `web/docs/responsive-discipline.md` needed no edit (directive expectation was stale)

The directive expected a stale citation in `web/docs/responsive-discipline.md`.
Discovery showed slice 437 had already repointed it — line 150 already reads "the
archived mockups under `Plans/_archive/mockups/`". No edit; no CHANGELOG bullet
for it. Recorded here so the discrepancy between the directive's expectation and
the tree's actual state is not mistaken for a missed file.

## Revisit once in use

- **R1 (highest):** the 23 flagged `web/app/**` citations remain stale until a
  follow-up sweep lands (after sibling 448 merges, to avoid the collision). A
  developer chasing a comment in `web/app/(authed)/controls/[id]/page.tsx` etc.
  still hits the dead pre-archive path until then. Track as a follow-up sweep
  slice (not filed this batch — the directive said FLAG, and filing a near-twin
  slice now risks an NNN collision with the in-flight siblings).
- **R2:** if the mockups are ever moved/renamed again, both the `_archive` path
  citations _and_ the `manifest.test.ts` invalid-fixture string would need a
  re-sweep. The functional resolver (`mockupsDir()`) is the only place that must
  stay correct for runtime; the comments are best-effort navigability.
- **R3:** consider whether line-anchored citations (`lines 139-152`) are worth
  keeping at all given the mockups are frozen iteration-1 artifacts — a future
  call could strip the line numbers (they will drift if a mockup is ever edited,
  though the archive is intended to be frozen). Out of scope here; update-path
  was the conservative choice.

## Confidence summary

| Decision                                 | Confidence |
| ---------------------------------------- | ---------- |
| D1 update-path vs rephrase               | high       |
| D2 flag web/app, don't edit              | high       |
| D3 leave historical records              | high       |
| D4 leave manifest.test.ts fixture        | high       |
| D5 add CHANGELOG bullet                  | medium     |
| D6 responsive-discipline already correct | high       |
