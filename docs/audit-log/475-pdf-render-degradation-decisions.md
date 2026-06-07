# Slice 475 — PDF render must degrade to 503 (not 500/hang) under load — decisions log

- detection_tier_actual: integration
- detection_tier_target: integration

The recurring failure was caught at the Go integration tier (`TestPDF_ReturnsPDFOrServiceUnavailable` / `TestExportPDF_SmokeTest` flaking at ~30.1s). That is also where it _should_ have been caught — the integration tier exercises the real chromedp handler path under real load, which is the only place the deadline-vs-degradation race is observable. The new render-deadline→503 + stress tests harden the same tier so the defect cannot recur silently. No production-tier escape.

---

## Decisions made

### D1 — One shared render limiter across board + questionnaires (NOT per-package). Confidence: high

**Options considered:**

- (a) A per-package limiter (one in `internal/board`, one in `internal/questionnaire`).
- (b) A single process-wide limiter in a shared `internal/pdfrender` package that both renderers route through.

**Chosen: (b).** Headless chrome is a single OS-level resource: each render spawns a chrome process (or hits the one shared `CHROME_DEBUG_URL` endpoint). A per-package cap of N would let board AND questionnaires each spawn N simultaneously — 2N total — which defeats the point of the cap (the burst the slice is about can hit both endpoints). A single shared limiter makes the concurrency cap govern _total_ chrome usage. This is the correct invariant for "a burst of PDF requests can't exhaust headless chrome" (AC-3). The cost is a new shared package, which is also the natural home for the error taxonomy both handlers map.

### D2 — Render deadline default 90s, env `ATLAS_PDF_RENDER_TIMEOUT`. Confidence: medium

**Options considered:** keep 30s (flakes); 60s; 90s; 120s.

**Chosen: 90s**, overridable via `ATLAS_PDF_RENDER_TIMEOUT` (Go duration string). The old 30s flaked at ~30.1s on a loaded CI runner — the render was _almost_ finishing, so a modest bump clears the flake with headroom. 90s is 3× the old budget: a healthy one-page brief renders in <2s and a several-page questionnaire in <5s, so 90s only ever bites a genuinely stuck/contended chrome, while still bounding a hang to a bounded, sub-2-minute wait. Configurable so a constrained-host operator can lower it (fail faster) or a heavy-pack operator can raise it. Revisit once real loaded-runner render-time percentiles exist.

### D3 — Concurrency cap default 2, env `ATLAS_PDF_RENDER_MAX_CONCURRENCY`. Confidence: medium

**Options considered:** 1 (serialize all renders); 2; 4; NumCPU-derived.

**Chosen: 2.** The v1 deployment target is a single VM (CLAUDE.md). A cap of 1 would serialize every PDF download across the whole platform — too aggressive for two operators previewing reports concurrently. A high cap (4+, or NumCPU) risks N chrome processes each holding ~100-200MB on a small self-host box. 2 is a conservative middle that allows light concurrency without letting a burst spawn unbounded chrome. Configurable for operators who size up. Revisit if real deployments show the cap throttling legitimate concurrent use (it would surface as `render_queue_saturated` WARN logs).

### D4 — Bounded queue wait default 15s, env `ATLAS_PDF_RENDER_QUEUE_WAIT`; 0/negative = fail-fast. Confidence: medium

**Options considered:** fail-fast (no wait, immediate 503 when full); fixed bounded wait; wait == render deadline.

**Chosen: a bounded 15s wait.** A pure fail-fast cap would 503 a second concurrent download even though the first is about to finish — poor UX for the normal two-operators case. Waiting for the _whole_ render deadline (90s) before saturating would let a queue back up unboundedly. A 15s bounded wait lets a queued render usually get a slot (a normal render frees its slot in <5s) while bounding the worst-case wait. A 0/negative override is honored as explicit fail-fast for operators who prefer it. The deterministic saturation test uses 0 (fail-fast) so it doesn't depend on timing.

### D5 — Classify the deadline by the RENDER CONTEXT, not by sniffing chromedp's error string. Confidence: high

chromedp wraps `context.DeadlineExceeded` inconsistently (sometimes as a navigation error, sometimes as a raw ctx error). Sniffing the error text would be brittle. Instead `Limiter.Do` checks: parent ctx cancelled → surface that (client disconnect, not a server fault); else render ctx past deadline → `ErrRenderDeadline` regardless of how chromedp wrapped it. This is what makes the deadline→503 mapping deterministic (AC-1). The `TestDo_RenderDeadline_OpaqueError` unit test pins this: an opaque non-`DeadlineExceeded` error on an expired render ctx still classifies as `ErrRenderDeadline`.

### D6 — `PDFTimeout` constants kept as deprecated aliases of `pdfrender.DefaultRenderTimeout`. Confidence: high

`board.PDFTimeout` and `questionnaire.PDFTimeout` were referenced by the handlers (now removed) and possibly elsewhere. Rather than delete them (a wider blast radius), they are retained as deprecated aliases pointing at the shared default so any stray reference compiles and reads the same value. New code should use `pdfrender.Default().RenderTimeout()`.

### D7 — Handlers no longer wrap the render in their own `context.WithTimeout`. Confidence: high

The limiter owns the bounded deadline (it applies `renderTimeout` to the render context). A second handler-level `WithTimeout` would be redundant and could fight the limiter's classification (whichever fired first would win, muddying the deadline-vs-cancel distinction). The handlers now pass `r.Context()` straight through; the limiter bounds it.

---

## Revisit once in use

1. **D2 deadline value** — once a loaded-runner render-time distribution exists, re-check whether 90s is too generous (a stuck chrome holds a slot for 90s) or whether p99 healthy renders ever approach it. Lower it if renders are reliably fast.
2. **D3 concurrency cap** — watch for `render_queue_saturated` WARN logs in real self-host deployments. If legitimate concurrent use trips the cap, raise the default (or document the env override more prominently in the operator docs).
3. **D4 queue wait** — if operators report PDF downloads "hanging for ~15s then failing", the bounded wait may be too long for their taste; a shorter wait or fail-fast default may read better. Conversely if 503s are common under light load, lengthen it.
4. **200-path coverage locally** — the 200 (`%PDF-`) branch is asserted but was only exercised in CI (no working chrome on the dev box this slice was built on; the local proof covered the 503 + deadline→503 + saturation + stress paths). Re-confirm the 200 path on a box with real chrome when convenient.
5. **Shared limiter scope** — if a third PDF renderer is added (e.g. an audit-export bundle), route it through `pdfrender.Default()` too so the cap stays a true total. Note this in any new renderer's review.

---

## Confidence summary

| Decision                  | Confidence |
| ------------------------- | ---------- |
| D1 shared limiter         | high       |
| D2 90s deadline           | medium     |
| D3 cap=2                  | medium     |
| D4 15s queue wait         | medium     |
| D5 classify by render ctx | high       |
| D6 deprecated aliases     | high       |
| D7 no double timeout      | high       |

The three `medium`-confidence tuning values (D2/D3/D4) top the revisit list — they are best-reasoned defaults for the single-VM target, all env-overridable, and the right ones to re-tune against real load.
