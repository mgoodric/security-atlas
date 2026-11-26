# Issue #1767 — chromedp v0.15.1 → v0.23.0 PDF renderer migration

Build-time calls made while taking the chromedp major in the
[#1767](https://github.com/mgoodric/security-atlas/issues/1767) dependency
sweep. chromedp v0.23 rewrites its API around generics, and all five PDF
renderers stopped compiling against it: `internal/policy/pdf`,
`internal/board` (brief and pack), `internal/questionnaire` and
`internal/audit/walkthrough`. The upstream guide is
`docs/MIGRATION.md` in the chromedp v0.23.0 module.

`detection_tier_actual = none` (no bug surfaced; the break was a compile
error found when the minor/patch batch tried the bump) ·
`detection_tier_target = none`.

## D1 — Migrate in place, one renderer at a time, not into a shared helper

**Decision.** Each renderer keeps its own allocator and print block, rewritten
to the new API. No shared `pdfrender` helper is introduced.

**Why.** The five blocks were already near-identical, and that duplication
predates this change. Folding them into one helper is a refactor with its own
review surface. Keeping this PR to a mechanical API port means each hunk can be
read against the migration guide line by line. A consolidation is a follow-up
if anyone wants it.

## D2 — Launch Chrome over the pipe transport and drop `WSURLReadTimeout`

**Decision.** The exec allocator uses v0.23's default transport, a pipe
(`--remote-debugging-pipe`). The `chromedp.WSURLReadTimeout(60s)` option and
the `chromedpWSURLReadTimeout` constants are removed. `DefaultTimeout`
(90s, `internal/policy/pdf`) and the `pdfrender` render budgets are unchanged.

**Why.** In v0.23, `WSURLReadTimeout` only applies to the websocket mode, so
under the pipe default it is dead code. The option existed because of the
slice 340/341 flake: chromedp's 20s wait for Chrome to print
`DevTools listening on ws://...` expired on the Harden-Runner-instrumented CI
runner. The pipe transport has no websocket URL to wait for, so that failure
mode is gone, not just given a longer timer. The #1827 run hit a related
websocket dial timeout in `TestPDF_ReturnsPDFOrServiceUnavailable`
(shard B2, cleared on re-run), which is the same websocket path.

The alternative was to keep the old behavior with `remote.WebSocket` and
keep the 60s timer. That preserves a known flaky path for no gain, so it is
not taken. Keeping the render budgets unchanged means only the transport
changes, not how long a render may take.

## D3 — `CHROME_DEBUG_URL` keeps working through `remote.NewAllocator`

**Decision.** `chromedp.NewRemoteAllocator(ctx, url)` becomes
`remote.NewAllocator(ctx, url)` from `github.com/chromedp/chromedp/remote`,
which v0.21 moved into the core module. The local variable is renamed from
`remote` to `debugURL` so it no longer shadows the package.

**Why.** It is a one-to-one rename in the upstream table. Operators who point
`CHROME_DEBUG_URL` at a `chromedp/headless-shell` container see no change.

## D4 — `PrintToPDF` through `cdp.Call` with `new(expr)` for pointer params

**Decision.** `chromedp.ActionFunc` plus the `page.PrintToPDF()` builder
becomes `chromedp.Func` calling
`cdp.Call(ctx, t, page.PrintToPDF, page.PrintToPDFParams{...})`, and the
bytes come from the result's `Data` field. `chromedp.Run(ctx, a, b)` becomes
`chromedp.Do(ctx, a, b)`. The optional params are pointers, and they are
written with Go 1.26's `new(true)` / `new(0.4)`.

**Why.** This is the shape the migration guide prescribes. `go.mod` already
declares `go 1.26.0`, so `new(expr)` needs no helper. The print options are the
same as before: background on, 0.4in margins.

## D5 — The missing-Chrome heuristic is unchanged

**Decision.** `isChromeUnavailable` still matches `exec:`,
`executable file not found` and `no chrome found`.

**Why.** v0.23 still resolves the binary with `exec.LookPath` and falls back to
`google-chrome`, so a missing browser still fails with
`exec: "google-chrome": executable file not found in $PATH`. The handlers' 503
mapping keeps working.
