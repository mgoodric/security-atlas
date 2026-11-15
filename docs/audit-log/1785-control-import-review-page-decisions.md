# Control import web review page — decisions log (#1785)

The web surface of the bring-your-own-controls import (#1734), over the API
that #1779-#1784 shipped: upload a sheet, review each staged row's
candidates, approve by candidate or by searched anchor, reject with a reason,
approve-clear, filter, and export the approved controls.

detection_tier_actual: playwright
detection_tier_target: playwright

One bug surfaced while writing the spec: after approving rows, "Back to
controls" showed the controls list cached from before the import (React
Query's 60 s `staleTime`), so the new controls were missing. The review page
now invalidates the `["controls"]` queries on every decision. The Playwright
flow caught it; that is the tier that should, because it is a cross-page
cache interaction.

## Decisions

**D1: Two routes, `/controls/imports` (upload) and `/controls/imports/[id]`
(review).** The platform has no endpoint that lists a tenant's batches, so
the Controls page links to the upload page, and an upload opens its batch's
review page. A batch is reopened by its URL. A batch list needs a
`GET /v1/controls/imports` endpoint first, so it is out of scope for this
web-only ticket.

**D2: No `PAGE_TITLES` entry.** A key there adds a sidebar item
(`lib/nav.test.ts`). The import is reached from the Controls page, so the
pages set their document title in `controls/imports/layout.tsx`, as
`controls/checklist/layout.tsx` does.

**D3: The SCF anchor search is the vendor-claims picker, extracted.**
`ScfAnchorPicker` and `filterAnchors` moved from the OSCAL
component-definition page to `components/control/scf-anchor-picker.tsx`
with their testids unchanged. Both pages use it, and the vendor-claims spec
still passes. It filters `/api/anchors` (the current SCF release) on the
client. The platform validates the pick against the batch's own release and
returns its error inline if they differ.

**D4: Applicability at approval reuses `ScopePicker` (#2103), behind "Set
applicability".** Left closed, the approval sends no `applicability_expr`,
so the platform applies the sheet's value or every cell. Opened, the picker
starts from the sheet's predicate. While the picker is incomplete
(`null`), approval stays disabled. Implementation type is a select whose
blank option names the effective value ("from sheet" or "default").

**D5: Approve-clear and reject use `ConfirmInline`.** Approve-clear creates
many controls in one action, so it confirms first. A 409 (another
approve-clear of the batch is running) is a toast, not an inline error, and
the batch refreshes afterwards. Reject's confirm stays disabled until a
reason is typed. The platform requires one too.

**D6: The Playwright spec is hermetic (slice 594), with stateful mocks.**
One in-memory batch backs the batch, rows, approve, reject, approve-clear
and controls-list mocks. A decision made on the review page is therefore
what the Controls page then lists. This pins the page's request bodies and
its rendering. Staging, approval and RLS are pinned by
`internal/api/controlimports/*_integration_test.go`. Playwright's
intercepted multipart body omits the file bytes, so the spec asserts the
filename and the framework field, not the sheet's content.
