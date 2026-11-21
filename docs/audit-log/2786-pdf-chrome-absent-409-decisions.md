# PDF export without Chrome answers 409 — decisions log (#2786)

Follow-up from #2730 (PR #2769). A deployment with no Chrome installed
answered PDF export with `503`, which clients read as retryable. Retrying
cannot install Chrome, so the case is a refusal about the deployment and now
answers `409 {"error": ..., "code": "feature_not_enabled"}` through
`httpresp.WriteFeatureNotEnabled`.

detection_tier_actual: none
detection_tier_target: none

No bug surfaced. The board helper's unit test was run with the chrome-absent
branch put back on 503 and failed (`status = 503, want 409`) before it passed.

## Decisions

**D1: Slice 475's grouping is split.** Slice 475
(`475-pdf-render-degradation-decisions.md`) put all three degradation modes
on 503: chrome absent, render deadline exceeded, render queue saturated. Its
point was that none of them may produce a 500 or a hang, and that still
holds. What changes is the status of one mode. A deadline or a full queue
clears once the renders ahead finish, so those two stay 503 and now carry
`Retry-After`. A missing Chrome does not clear, so it moves to 409. The
WARN log and its `reason` values (`chrome_absent`,
`render_deadline_exceeded`, `render_queue_saturated`) are unchanged.

**D2: The classifier writes the response.** `pdfDegradation` returned
`(status, msg, ok)` and each call site wrote the body with
`httpresp.WriteError`. The two outcomes now differ in more than the status
(a `code` field on one, a `Retry-After` header on the other), so each
package's helper became `writePDFDegradation(w, r, ..., err) bool`, which
logs and writes and reports whether it handled the error. A `false` still
falls through to `httperr.WriteInternal`. The three packages (board,
questionnaires, walkthroughs) keep their own copies, as they did before.
Merging them into one shared package would need a new package and its
coverage floor, which this ticket does not call for.

**D3: `Retry-After` is 15 seconds.** `pdfrender.RetryAfterSeconds` is derived
from `DefaultQueueWait` (15s). After that long a render ahead in the queue
has usually finished. It is a constant, not the env-tuned queue wait, because
it is a hint and not a promise.

**D4: The policy PDF moves too.** `GET /v1/policies/{id}/pdf` does not go
through the slice-475 limiter, but it had the same chrome-absent 503. It now
answers the same 409 through a small `writePDFRenderErr` helper, so all four
PDF endpoints say the same thing when Chrome is missing.

**D5: One message for all four endpoints.** "PDF export is not enabled on
this deployment: chrome is not installed" follows the "... is not enabled
on this deployment" wording #2730 used for the other switched-off features.
