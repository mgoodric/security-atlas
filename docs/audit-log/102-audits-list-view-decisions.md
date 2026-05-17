# Slice 102 — `/audits` list view: build-time decisions

> AFK-type slice. Engineer made these subjective calls during build per
> the `JUDGMENT` slice-development pattern (CLAUDE.md "AI-assist
> boundary": this is about how we build, NOT how the shipped product
> behaves). The product still never publishes audit-binding artifacts
> without one-click human approval — the list view is read-only render.

---

## D1 — Row source: `periodWire` from `GET /v1/audit-periods` (canonical)

**Decision:** Consume `GET /v1/audit-periods` via a new BFF at
`web/app/api/audits/route.ts`. Row shape = `periodWire` from
`internal/api/auditperiods/handlers.go`.

**Why:** Design doc §7 mandates `periodWire` as authoritative. The
slice text + dependency on 020 + 028 confirm. The endpoint already
returns the tenant-scoped period list (via RLS).

**Alternative rejected:** Reuse `/api/audit/periods` (slice 042 BFF).
That endpoint forwards `/v1/me/audit-periods` which scopes to the
CALLER's auditor assignments only — wrong scope for the security
leader's period index. Different endpoint, different consumer.

---

## D2 — `framework_version_id` rendered as mono-prefix + tooltip (no label endpoint yet)

**Decision:** Render `framework_version_id` as the first 8 chars of the
UUID + ellipsis, with the full UUID in a `title=` tooltip.

**Why:** No label endpoint exists on `main`. `internal/api/dashboard/`
exposes `/v1/frameworks/posture` (per-framework posture summary), and
`internal/api/frameworkscopes/` exposes `GET /v1/framework-scopes` with
filter-by-framework_version, but neither returns a labeled list of
{id → "SOC 2 · 2017 TSC rev. 2022"}. The slice's "Notes" section
flagged this risk and suggested filing a spillover.

**Why mono-prefix vs the raw UUID:** Full UUIDs are 36 chars and break
the column layout. Truncation + tooltip is the standard pattern for
opaque identifiers in this codebase (slice 098 controls table uses the
same approach for `anchor.id`).

**Spillover filed:** No — deferred to a future "audits enhancements"
slice rather than filed today. The UI is functional without the label;
the slice ACs do not require a friendly framework name.

---

## D3 — Status pill enumerates the forward-looking vocabulary (not just the v1 DB enum)

**Decision:** The status filter pill exposes
`{open, in_progress, frozen, closed, planned}` even though the v1
`audit_periods.status` CHECK constraint allows only `{open, frozen}`.

**Why:** The slice text mentions "planned/in-progress/frozen/closed" as
the status set, and the design mockup (`audits.html`) shows all four.
The platform's CHECK constraint will lift in a future migration. By
enumerating the forward set now, the page works the day that
migration lands — no UI rework. Until then, filtering on (e.g.)
`closed` just returns zero rows, which is correct.

**Render strategy:** Whatever string the backend returns goes through
the pill-color switch. Unknown statuses fall through to a neutral slate
pill — never crashes.

---

## D4 — In-progress urgent cue: 30 days, inclusive of zero, non-frozen only

**Decision:** A period gets the amber pulsing dot iff
`status !== 'frozen' && 0 <= daysUntilEnd(p, now) <= 30`.

**Why:**

- Slice text: "in-progress periods within 30 days of period_end".
- Frozen periods are locked — they get a lock icon, not an urgency
  cue. The two markers are semantically disjoint.
- Past-end periods (`daysUntilEnd < 0`) get a different signal (should-
  freeze prompt) which is out of scope for this slice — the amber cue
  is "fieldwork ahead", not "you missed the deadline".
- Inclusive bound at 30: matches "within 30 days" literally.
- Inclusive bound at 0: a period that ends today is the most urgent
  case; excluding it would be wrong.

**Spillover candidate:** The "should freeze" prompt for past-end open
periods is a separate UX concern — file as a follow-up slice if a
maintainer wants it.

---

## D5 — Row-click target: `/audits/[id]` placeholder

**Decision:** Row click navigates to `/audits/{id}` which currently
404s with the Next.js standard not-found UI.

**Why:** The slice text explicitly allows "placeholder OR drawer".
404 is the cleanest placeholder — when the per-period detail slice
lands, no code change is needed here. A drawer would require building
plus then tearing down a one-off UI; an inline expand-row would
require extending the shared `ListTable` shell with row-detail
mechanics (out of scope per the shell's domain-agnostic contract).

---

## D6 — `Create audit period` CTA routes to `/admin` (placeholder)

**Decision:** The empty-state CTA + the disabled-button CTA both point
at the admin route placeholder, not at an in-list create form.

**Why:** P0-A3: "Does NOT bundle period-create UI". The mockup shows
a primary button labeled "New audit period" in the page actions; we
preserve the chrome but disable the button (and the empty-state CTA
opens `/admin` where future credential / connector / period-create
admin flows live). When a real period-create UI lands, the routing
target updates in one place.

---

## D7 — Frozen-meta tooltip omits `frozen_hash`

**Decision:** The lock icon's `title=` tooltip renders
`frozen at YYYY-MM-DD by <actor>` only — NOT the `frozen_hash`.

**Why:** `frozen_hash` is a 64-character hex digest that's irrelevant
to the security leader scanning the list. It belongs on the period
detail page where the user can copy + verify it. Putting it in a
tooltip clutters the hover affordance without serving any inline
workflow.

---

## D8 — Sample-size column omitted (P0-A4 enforcement)

**Decision:** The mockup shows a "Sample size" column ("1,847
records"). This slice does NOT render it.

**Why:** P0-A4 forbids invented columns. `periodWire` does NOT carry a
`sample_size` or `record_count` field. Computing one would require
joining `evidence_records` per period, which is a per-row fan-out
anti-pattern (same shape as the 098 → 104 spillover for per-row state).
If a maintainer wants the column, they file a backend slice analogous
to 104 that extends `periodWire` with an aggregate count.

---

## Trade-off summary

| Decision | Optimized for         | Trade-off accepted                                                     |
| -------- | --------------------- | ---------------------------------------------------------------------- |
| D1       | Tenant-scoped reads   | One new BFF route instead of reusing `/api/audit/periods`              |
| D2       | Functional v1         | Users see UUIDs until a label endpoint lands                           |
| D3       | Forward-compat        | Some filter options return zero rows today                             |
| D4       | Clear UX signal       | Past-end open periods need a future separate cue                       |
| D5       | Minimal code in slice | Row click 404s until the detail slice lands                            |
| D6       | P0-A3 compliance      | The "New audit period" button is non-functional until create UI lands  |
| D7       | Tooltip simplicity    | Hash verification requires the detail page                             |
| D8       | P0-A4 compliance      | The list loses the mockup's "Sample size" affordance until backend ext |
