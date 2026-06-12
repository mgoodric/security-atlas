# Slice 457 — OSCAL signed-export download surface · decisions log

- detection_tier_actual: playwright
- detection_tier_target: playwright

Slice type: JUDGMENT. The subjective calls below are the download
packaging, the endpoint shape, the UX placement, and the AC-4 verification
tier. Two bugs surfaced in CI during the slice, both at the Playwright
tier (`target=playwright` was correct — they are browser-download-contract
bugs that only manifest in a real browser, not unit-testable):

1. **Download filename fell back to `oscal-export.txt`** — a BARE anchor
   `download` attribute let the browser derive the name from the URL last
   segment, overriding the BFF `Content-Disposition` for the same-origin
   case. Fixed by setting the `download` attribute to the deterministic
   filename VALUE (`oscalExportDownloadFilename`, mirroring the Go-side
   builder); guarded by a module-tier vitest + an e2e DOM assertion on the
   anchor attribute (so a regression now trips at the cheaper vitest tier
   too).
2. **`download.createReadStream()` "canceled"** — reading a route-mocked
   same-origin download body via the download stream is a flaky Playwright
   harness seam (the browser consumes the mocked body before the stream is
   read). Resolved by the D6 tier split below.

## Decisions made

### D1 — Where the download surface lives: a sibling platform verb, not a BFF-only repackage

**Options considered.**

- **(a)** BFF-only: the existing `POST /v1/audit-periods/{id}/oscal-export`
  stays the single platform surface; the BFF fetches the JSON envelope,
  re-serializes it, and adds the `Content-Disposition` header.
- **(b)** A new platform verb `POST /v1/audit-periods/{id}/oscal-export:download`
  that reuses the same Exporter under the same tenant context but serves
  the envelope with attachment headers. The BFF is a thin byte
  passthrough.

**Chosen: (b).** The colon-verb sibling mirrors the in-tree
`POST /v1/walkthroughs/{id}:finalize` precedent and keeps the
attachment-vs-inline distinction an explicit, discoverable platform
contract (it surfaces in `docs/openapi.yaml` + `routes.go`, gated by the
required `openapi-drift-check`). Critically, it puts the
**tenant-isolation boundary at the Go tier** — the authoritative RLS
layer — where the headline cross-tenant test belongs, rather than
implying (via a BFF-only repackage) that the disposition is a
presentation concern. The two verbs share `runExport`, so they can never
drift in tenant scoping, body shape, or error mapping. The wire contract
of the original `:export` verb is untouched (scope discipline: "does NOT
change the platform export handler's wire contract").

**Rationale / pattern match.** `internal/api/oscalexport/handler.go`
already had a clean `Export` shell; extracting `runExport` and adding
`Download` is the minimal additive change. The board-pack PDF surface
(slice 043) is the analogous "platform serves the attachment, BFF streams
bytes" shape.

### D2 — Packaging: a single self-contained `.json` envelope, NOT a zipped multi-file bundle

**Options considered.**

- **(a)** Single `.json` envelope (the existing `exportResponse`: a
  manifest plus four OSCAL members plus the slice-413 signature, all
  inline).
- **(b)** A zip of the four members + a manifest + a detached `.sig`
  sidecar.

**Chosen: (a).** The slice-413 signing manifest is an **inline** field of
the envelope (`oscal.Signature`), not a detached `.sig` over separate
files. A single `.json` therefore keeps the signed manifest, the four
members, and the signature in **one verifiable artifact** — the same
bytes the wire `:export` endpoint returns. A zip would add a
streaming/packaging surface and a multi-file layout for **no integrity
gain** in v1 (there is no detached signature to carry as a sidecar). AC-4
is satisfied: the manifest rides inside the downloaded file. If a future
slice introduces a detached-`.sig`-over-files shape (e.g. for tools that
verify per-member), the zipped form becomes the right call — it is
recorded in the revisit list.

**Filename.** `oscal-bundle-<period-id>-<frozen-date>.json`. The period
id grounds the name to a specific period (an operator saving several
bundles does not collide them); the frozen date (the `YYYY-MM-DD` prefix
of the period's RFC-3339 `FrozenAt`) is human-meaningful. A malformed /
empty `FrozenAt` omits the date segment rather than guessing one.

### D3 — Browser-friendly GET → upstream POST at the BFF

**Options considered.**

- **(a)** BFF exposes POST, the page drives a `fetch` POST + a synthesized
  blob download.
- **(b)** BFF exposes GET (a native `<a href download>`), translating to
  the upstream POST.

**Chosen: (b).** A native browser download is a GET navigation —
`<a download href>` raises a real `download` event with no fetch
ceremony, which is exactly what `page.waitForEvent("download")` (AC-3)
needs and what the board-pack PDF link (slice 043) does. The platform
export verb stays a POST (it is a generate action, not a cacheable read).
The BFF bridges the two and posts an empty body (`{}`) — the org/system
SSP-profile fields are optional and default in the bridge. A later slice
can carry org-profile overrides as query params if an operator needs
them (revisit list).

### D4 — UX placement: a per-frozen-period row link, plus a toolbar note

**Options considered.**

- **(a)** A single list-level export button in the toolbar (where the
  slice-217 disclosure lived).
- **(b)** A per-frozen-period download link in each frozen row, with the
  toolbar carrying a note that points at the per-row action.

**Chosen: (b).** OSCAL export is a **per-period** operation (invariant
#10 — only a _specific frozen period_ is exportable), so a list-level
button has no single period to act on. The per-row link is the honest
shape: it renders only on frozen rows, and its `href` is the BFF download
route for that period. The toolbar note (`audits-oscal-export-toolbar`)
replaces the slice-217 disclosure `<span>` and tells the operator where
the now-working action lives — the honesty property **migrates** from the
disclosure to the live affordance (AC-5), it is not silently deleted. The
slice-217 module (`oscal-export-future.ts`) + its vitest are replaced by
`oscal-export.ts` + a new vitest; the `audits-list.spec.ts` slice-217
assertion is rewritten to assert the old testid is gone and the new
download link + toolbar note are present.

### D5 — Tenant isolation test tier: the `internal/oscal` integration tier

The headline threat is cross-tenant information disclosure. The download
handler reuses the Exporter, which reads under RLS via
`tenancy.ApplyTenant`. The authoritative place to prove a Tenant-B
request cannot download Tenant-A's bundle is the integration tier with a
real Postgres + RLS — `TestExport_CrossTenantPeriodIsNotExportable` seeds
a frozen period for tenant A, then resolves it under tenant B's context
and asserts `ErrPeriodNotFound` (the 404 the download serves).

**Test exercises `Exporter.Aggregate`, not the full `Export`.** RLS
denial happens in the Aggregate stage (`aggregate.go:109`,
`tenancy.ApplyTenant` + `GetAuditPeriodByID`), BEFORE any
Python-oscal-bridge call. Asserting at `Aggregate` therefore (a) tests
the exact RLS boundary the isolation property lives in, and (b) keeps the
test **bridge-free** — no nil-bridge dereference. (An earlier draft called
the full `Export` with a `nil` bridge; the tenant-A sanity pre-check
resolved the period and then reached the serialize stage, dereferencing
the nil bridge → a deterministic CI panic. `Aggregate` never touches the
bridge, so a nil bridge is safe and the test is panic-proof.) The
sanity pre-check (tenant A aggregates its OWN period without error) proves
the period genuinely exists for its owner, so the tenant-B denial is real
isolation, not a not-found that fires for everyone. The handler-level +
BFF-level unit/vitest tiers assert the attachment headers, the filename,
and the error-path "no attachment on error" property.

### D6 — AC-3 vs AC-4 e2e verification tier split

**Context.** The slice-457 download e2e originally proved AC-4 ("the
downloaded artifact carries the slice-413 signing manifest") by reading
the fired download's body via `download.createReadStream()`. In CI this
threw `download.createReadStream: canceled`: for a **route-mocked
same-origin** anchor-download, Playwright frequently cancels the download
stream (the browser already consumed/cached the mocked body before the
stream is read). This is a test-harness brittleness, not a product bug.

**Decision.** Split the two ACs across the reliable tier for each:

- **AC-3 (download fires + deterministic filename)** stays in the e2e and
  is fully deterministic: `context.waitForEvent("download")` fires and
  `download.suggestedFilename()` equals the pinned
  `oscal-bundle-<period>-<frozen-date>.json` (the anchor `download`
  attribute drives it; a sibling DOM assertion pins the attribute value).
  The e2e does **not** call `download.createReadStream()`.
- **AC-4 (signing manifest rides in the bundle)** is verified at the
  tiers where the bundle body is read reliably:
  - **BFF vitest** (`web/app/api/audits/[id]/oscal-export/route.test.ts`)
    — the AUTHORITATIVE manifest-in-bundle assertion: the BFF streams the
    platform body verbatim, so a full-shape assertion (mode + algorithm +
    digest + signature + the four members) proves the manifest survives
    the passthrough.
  - **Go `download_test.go`** — the handler serializes the same
    `exportResponse` (the signature is an inline field).
  - **slice-423 envelope e2e** (same file, line 159, passing) — already
    asserts "export produces a signed OSCAL bundle envelope".
  - As in-flow corroboration the slice-457 e2e reads the bundle from the
    BFF **response** object (`page.waitForResponse(...).json()`) — the
    reliable seam, NOT the download stream.

**Rationale.** Reading a route-mocked same-origin download body is the
flaky seam; the `response.json()` path returns the fulfilled body
verbatim. Pushing the byte-faithful manifest proof down to the
vitest/Go tier follows the project's "catch it at the cheapest reliable
tier" discipline and removes a class of CI flake.

## Revisit once in use

1. **Detached-`.sig`-over-files packaging (D2).** If real auditors /
   external OSCAL tooling want to verify each member independently, the
   single-`.json` envelope should graduate to a zipped bundle with a
   detached `.sig` sidecar. Re-evaluate when the first external verifier
   integration lands. (confidence the single-json is right for v1: high)
2. **Org-profile overrides on download (D3).** The BFF posts an empty
   body, so the SSP org/system fields fall back to bridge defaults on a
   download. If operators need to set OrganizationName/SystemName at
   download time (not just at the `:export` call site), add query-param
   passthrough. (confidence empty-body is fine for v1: medium)
3. **Per-period detail page (D4).** When the per-period detail view ships
   (the slice-184 banner's deferred work), the download link likely moves
   there as the primary home, and the `/audits` row link either points to
   it or is removed. Re-evaluate the row-vs-detail placement then.
   (confidence the row link is the right v1 home: high)
4. **Concurrency / large-bundle streaming.** The download materializes the
   envelope in BFF memory via `arrayBuffer()` (mirroring the board-pack
   PDF BFF). If bundles grow large enough that buffering is a problem,
   switch to a streamed passthrough like the slice-139 data-export BFF.
   (confidence buffering is fine at v1 bundle sizes: high)

## Confidence

| Decision                                          | Confidence |
| ------------------------------------------------- | ---------- |
| D1 — sibling `:download` verb sharing `runExport` | high       |
| D2 — single `.json` envelope packaging            | high       |
| D3 — GET-here → POST-upstream at the BFF          | high       |
| D4 — per-frozen-period row link + toolbar note    | high       |
| D5 — cross-tenant test at the integration tier    | high       |
| D6 — AC-3/AC-4 e2e verification tier split        | high       |
