- **fix(policy/pdf):** slice 340 — chromedp `TestRender_ProducesRealPDF`
  integration test re-enabled after diagnosing the websocket-url-timeout
  flake (5 consecutive CI failures across slices 312/315/320, all
  clustering within ~70ms of the 20.0-second wall-clock boundary). Root
  cause: chromedp's hardcoded `wsURLReadTimeout` watchdog at
  `github.com/chromedp/chromedp@v0.15.1/allocate.go:249` fires after 20
  seconds if Chrome doesn't print its `DevTools listening on ws://...`
  line to stderr in time. On the GitHub Actions `ubuntu-latest` runner
  with the slice 117 StepSecurity Harden-Runner audit-mode hook
  instrumenting every outbound call, Chrome's startup network calls
  (component-updater, GPU blocklist refresh, safebrowsing list update)
  stretch past the threshold. Fix: `chromedp.WSURLReadTimeout(60s)`
  added to the ExecAllocator options in `internal/policy/pdf/render.go`,
  plus a `DefaultTimeout` raise from 30s to 90s for outer-context
  headroom. `t.Skip` removed from `render_integration_test.go`.
  Production HTTP handler timeout (`pdfRenderTimeout = 30s` in
  `internal/api/policies/handlers.go`) is unchanged — the runtime SLA
  is preserved; only the CI-tolerance ceiling moves. Full diagnosis
  trail with failed-run links + the 22.13s tail-pass evidence at
  `docs/audit-log/340-chromedp-flake-decisions.md`. Spillover slice 341
  filed for fan-out to the four sibling PDF renderers (`internal/board/pdf.go`,
  `internal/board/pack_pdf.go`, `internal/questionnaire/pdf.go`,
  `internal/audit/walkthrough/export.go`) which share the same chromedp
  configuration and the same exposure to the watchdog.
