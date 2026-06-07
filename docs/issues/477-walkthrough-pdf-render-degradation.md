# Slice 477 — Walkthrough PDF render must degrade to 503 (not 500/hang) under load

**Cluster:** Quality/Backend · **Type:** JUDGMENT · **Estimate:** S · **Status:** filed
**Parent:** slice 475 (board/questionnaire PDF render degradation) · **Surfaced by:** slice 475 implementation

## Problem

There is a THIRD headless-chrome (chromedp) PDF renderer the slice-475 fix did
NOT touch (slice 475 was scoped explicitly to board + questionnaires):

- `internal/audit/walkthrough/export.go` — `RenderPDF` + its own `PDFTimeout = 45s`.
- `internal/api/walkthroughs/handlers.go:381` — the HTTP handler.

The walkthrough PDF handler carries the **identical latent defect** slice 475
fixed in board/questionnaires:

```go
ctx2, cancel := context.WithTimeout(r.Context(), walkthrough.PDFTimeout)
defer cancel()
buf, err := walkthrough.RenderPDF(ctx2, wt)
if err != nil {
    if errors.Is(err, walkthrough.ErrChromeUnavailable) {
        httpresp.WriteError(w, http.StatusServiceUnavailable, ...)
        return
    }
    httperr.WriteInternal(w, r, "render PDF", err)   // <-- deadline-exceeded → 500, not 503
    return
}
```

A render that exceeds the 45s deadline (slow / contended chrome) returns a
wrapped `context.DeadlineExceeded` that is NOT `ErrChromeUnavailable`, so it
falls through to `httperr.WriteInternal` = **500**, not the graceful **503**.
Same non-deterministic degradation; same flake risk under load. It was not
observed flaking (the walkthrough export endpoint is exercised less under CI
load than the board/questionnaire ones), but the defect is the same class.

## Deliverable

Route `walkthrough.RenderPDF` through the existing shared
`internal/pdfrender.Limiter` (built by slice 475 — `pdfrender.Default().Do`),
exactly as `board.RenderPDF` / `questionnaire.RenderPDF` now do, and map
`pdfrender.ErrRenderDeadline` / `pdfrender.ErrQueueSaturated` /
`walkthrough.ErrChromeUnavailable` ALL to 503 in the handler with the WARN that
distinguishes the three modes. Add the deterministic render-deadline→503 +
stress integration coverage for the walkthrough export endpoint. Routing it
through the SAME shared limiter is important: it keeps the concurrency cap a
true _total_ across all three chrome renderers (slice 475 decision D1), rather
than letting walkthroughs spawn chrome outside the cap.

No change to walkthrough PDF content/templates; the `chromedp` dependency and
the slice 340/341 `WSURLReadTimeout` fix stay untouched.

## Constitutional invariants honored

- No change to the AI-assist boundary, RLS/tenancy, or evidence ledger — this
  is a pure render-degradation hardening of an existing read endpoint.
- Reuses the slice-475 `internal/pdfrender` governor; adds no new dependency.

## Canvas references

- canvas §8 (audit-workflow — walkthrough export is an auditor-facing artifact).

## Not fixed in slice 475 because

Slice 475's spec scoped it explicitly to two packages ("This touches board +
questionnaires (two packages)"). Expanding to a third renderer mid-slice would
have widened the blast radius beyond the named scope. The shared
`internal/pdfrender` package slice 475 builds makes this follow-on small: route,
map, and test.
