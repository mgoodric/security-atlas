# Slice 667 — Dashboard recent-activity chips decisions log

**Type:** JUDGMENT
**Slice:** `docs/issues/667-dashboard-activity-chips-inert-dup-note.md`
**Surface:** the dashboard "Recent activity" card (`web/components/dashboard/activity-feed-panel.tsx`, rendered by `web/app/(authed)/dashboard/page.tsx`).

- detection_tier_actual: none
- detection_tier_target: none

(No bug surfaced during the slice. This is a dead-control + internal-text
cleanup; the only work was the hide-vs-wire judgment call below. The
e2e-audit harness — `web/e2e-audit/` — is precisely the tier that would
have flagged the inert-chip-with-"coming soon"-tooltip pattern as a
HONESTY-GAP; this slice removes the gap at the source.)

---

## The JUDGMENT call: HIDE, not WIRE

### D1 — Hide the filter chips; do not wire them. **(confidence: high)**

The All / Evidence / Controls / Approvals chips on the dashboard
"Recent activity" card were inert `<span>` elements (no handler) and
carried a developer-facing placeholder note — _"Filter chips activate
once the activity endpoint widens beyond the evidence branch"_ —
duplicated 4× in `title` attributes.

- **Options considered:**

  - (a) **Wire** the chips to real per-kind filtering of the feed.
  - (b) **Hide** the chips until the endpoint supports filtering.

- **Endpoint investigation (the deciding fact):** the dashboard activity
  feed binds to `GET /v1/activity` via the dashboard BFF. The handler
  (`internal/api/dashboard/handler.go::Activity`) reads only `cursor` and
  `limit` from the query — there is **no `kind`/`source`/`category`
  filter param**. The store method
  (`internal/api/dashboard/store.go::ActivityFeed(ctx, cursor, pageRows)`)
  reads **only** the evidence branch (`ListEvidenceActivity` over
  slice-062's `admin_audit_log_v` filtered to `evidence_audit_log`). So
  every row the dashboard endpoint returns is already an evidence event —
  the "Controls" and "Approvals" chips would have nothing to bind to and
  would always render an empty feed.

- **Does slice 669 change this?** No. 669 (merged `4e26fd21`) defaulted
  the **standalone** `/activity` view to business events and added a
  read-telemetry deny-list to `internal/api/adminauditlog/activity.go` +
  `web/app/(authed)/activity/page-client.tsx`. It did **not** touch the
  dashboard `/v1/activity` endpoint, its store method, or its source
  table. Wiring the dashboard chips would still require new backend work.

- **Chosen:** (b) Hide. Per the slice's default lean ("don't ship inert
  controls") and the anti-criterion ("does NOT leave inert chips with an
  internal explanatory tooltip"). Wiring is a backend slice — widen the
  dashboard endpoint's source beyond the evidence branch and add a
  `?kind=` filter param — which exceeds this frontend-only XS slice's
  scope. The wire-it path is captured as a follow-up slice (see below).

- **Rationale:** an inert control with a developer-facing tooltip is the
  exact HONESTY-GAP anti-pattern the `web/e2e-audit/` harness exists to
  catch ("disabled buttons whose tooltip reads 'coming soon'"). Removing
  the chips is honest about what the dashboard surface actually does
  today; the empty-feed alternative (wiring chips to a single-branch
  endpoint) would be a worse user experience and still dishonest.

### D2 — Removal, not feature-flag gating. **(confidence: high)**

- **Options considered:** (a) delete the chip markup; (b) keep it behind a
  feature flag off-by-default.
- **Chosen:** (a). There is no partial-filter state to preserve and no
  in-flight backend; a flag would re-introduce the dead-code smell the
  slice removes. When the endpoint gains filtering, the follow-up slice
  re-introduces the chips wired to the real param — a clean re-add reads
  better than a dormant flag.

---

## Revisit once in use

- **Re-add the chips wired to real filtering** once the dashboard
  `/v1/activity` endpoint widens beyond the evidence branch and accepts a
  `?kind=` (or `?source=`) filter param. Keep the chip filter model
  **consistent with slice 669's** kind taxonomy (the `decision`/`read`
  deny-list + business-event kinds) so the dashboard card and the
  standalone `/activity` view speak the same vocabulary. Tracked as the
  spillover slice filed by this slice.
- **PanelCard description copy** still reads "Evidence ingest, control
  state changes, approvals." That is honest product copy describing what
  the feed will surface as the endpoint widens — left as-is. Revisit only
  if the endpoint's branch coverage changes the accuracy of that line.

---

## Confidence summary

| Decision             | Confidence |
| -------------------- | ---------- |
| D1 — hide not wire   | high       |
| D2 — remove not flag | high       |
