# Issue #1736 — control anchor fit

Build-time calls made for
[#1736](https://github.com/mgoodric/security-atlas/issues/1736). Some
SOC 2 CC1 controls (audit committee independence and cadence, recruitment
and retention, performance reviews) have no real SCF equivalent. Adopters
anchored them to catch-alls such as GOV-09 and HRS-02, and nothing recorded
that the anchor was approximate. A control can now state its fit:
`anchor_fit: exact | partial | none`, with `anchor_fit_note` required for a
non-exact fit.

`detection_tier_actual = none` (no bug surfaced; the ticket is a gap found
by a first-run adopter) · `detection_tier_target = none`.

## D1 — `none` is the only fit that may omit `scf_anchor_id`

**Decision.** `ValidateStructural` still rejects a missing `scf_anchor_id`,
except when `anchor_fit` is `none`. `Store.Upload` then skips anchor
resolution and stores `scf_anchor_id`, `scf_id` NULL, and `control_family`
`UNKNOWN` unless the manifest names one.

**Why.** ADR 0013 asks for "an anchor (or an honest 'unmapped')". `none` is
that honest unmapped, and the ticket's criteria allow it "with or without an
anchor". `partial` asserts that some anchor covers part of the control, so a
`partial` fit without an anchor is contradictory and stays an error. The
`controls.scf_anchor_id` column was already nullable and the coverage
endpoint already returns `anchor: null` for an unanchored control, so no
read path needed a new branch.

## D2 — The fit is a column with CHECKs, not a manifest-only field

**Decision.** Migration `20260904000000` adds `controls.anchor_fit TEXT NOT
NULL DEFAULT 'exact'` and `anchor_fit_note TEXT NULL`, with two CHECKs: the
value is one of the three, and a non-exact fit has a non-blank note.

**Why.** The list filter, the detail page and the export all read the fit;
parsing it back out of `bundle_manifest_yaml` on every read would be slower
and easy to get wrong. The default backfills every existing row as `exact`,
which is what those controls were implicitly claiming. A TEXT + CHECK
matches `tenants.bundle_gate_mode` rather than a new enum type, which would
need the `_enums.sql` sqlc prelude as well.

## D3 — Export columns are appended, the history export is unchanged

**Decision.** `anchor_fit` and `anchor_fit_note` are the last two columns of
`GET /v1/controls/export`. The history export
(`GET /v1/controls/history/export`) keeps its 17 columns.

**Why.** The export header is documented as position-stable for downstream
consumers; appending keeps every existing column at its index. The ticket
names the controls export only, and the history export restates the
15-column baseline as its own list, so its lock test now compares against
the first 15 columns of the controls header.

## D4 — The list filter runs client-side; the API filter exists too

**Decision.** The `/controls` page filters by fit client-side, like family,
state and freshness, because it already walks every control. The pill shows
in "My controls" only and is dropped on a switch to the SCF catalog, whose
anchor rows have no fit. `GET /v1/controls?include=state` also accepts
`anchor_fit=exact|partial|none` (applied in SQL to the page and the total;
any other value is a 400) for API callers. `anchor_fit` joins the saved-view
filter allow-list on both sides.

## D5 — Marker copy

A non-exact row carries an outline badge after the control name: "Partial
fit" or "No SCF fit", with the note as its tooltip. The detail page shows
"Anchor fit Exact | Partial | No SCF fit" in the metadata line and the note
beneath it. "No SCF fit" is used instead of "None" so the label reads on its
own.
