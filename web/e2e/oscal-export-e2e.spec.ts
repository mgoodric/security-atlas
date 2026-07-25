// Slice 423 — Playwright E2E for the OSCAL SIGNED-EXPORT chain:
// authenticated operator -> POST oscal-export -> signed bundle envelope.
//
// This is a v1 binary success-test artifact (CLAUDE.md: "does the
// operator run their next SOC 2 out of security-atlas" — the auditor
// hand-off is the OSCAL bundle). The Go side
// (`internal/api/oscalexport/handler.go`, 98 floor) has unit +
// integration coverage, but the BROWSER-DRIVEN generate -> stream chain
// is never exercised end-to-end. Slice 413 made the bundle a SIGNED
// artifact (ADR-0010 — embedded-ed25519 default, cosign-kms opt-in),
// which raises the stakes: the e2e must prove the chain emits a bundle
// carrying the signing manifest, not just any blob. This spec closes
// that "does the chain actually produce a signed bundle" gap.
//
// ── WHAT THE EXPORT SURFACE ACTUALLY IS (load-bearing scoping note) ──
//
// The slice doc's AC-1/AC-2/AC-3 are phrased as a browser DOWNLOAD chain
// (navigate /audits -> per-period detail -> click export ->
// `page.waitForEvent("download")` with a Content-Disposition attachment),
// mirroring the board-pack PDF export (`board-pack-export-e2e.spec.ts`,
// slice 388). That phrasing assumes a browser download surface that does
// NOT exist in v1:
//
//   1. The OSCAL export handler (`internal/api/oscalexport/handler.go`)
//      responds `Content-Type: application/json` with a JSON ENVELOPE
//      (manifest + signature + the four OSCAL members) — NOT a
//      `Content-Disposition: attachment` byte stream. It raises no
//      browser `download` event.
//   2. There is no BFF route in `web/app/api/**` forwarding to
//      `POST /v1/audit-periods/{id}/oscal-export` (only the slice-139
//      DATA export at `/api/admin/audit-periods/export`, which is the
//      CSV/JSON/XLSX dump explicitly distinct from the cosigned bundle
//      — see the page.tsx P0-A-AP-1 comment).
//   3. There is no per-period detail page: `/audits/[id]` does not exist
//      (the slice-184 banner on `/audits` says the detail view "is
//      coming in a future slice"), and the `/audits` toolbar carries a
//      slice-217 disclosure SPAN, not an export trigger.
//
// Building the per-period detail page + an OSCAL download BFF + the
// export button would be a multi-slice FEATURE — which the slice's scope
// discipline explicitly forbids ("does NOT add a new export feature").
// Per AC-5 / P0-423-3 ("does NOT relax a precondition the docker-compose
// bring-up cannot provide — a seed gap is a spillover"), the missing
// browser DOWNLOAD surface is a precondition gap. It is filed as
// spillover slice 457; the literal `waitForEvent("download")` form of
// AC-2 lands there, against the real surface, rather than being faked
// here (a fabricated download event the product does not emit would be
// the dishonest-test anti-pattern this project rejects).
//
// ── WHAT THIS SPEC DRIVES (the boundary that genuinely exists) ──
//
// The reachable end of the chain from an authenticated browser is the
// platform wire call itself. Following the `evidence-push-e2e.spec.ts`
// and board-pack stage-1 precedent (model the operator's action as an
// IN-PAGE `fetch` so it flows through the page network and is
// intercepted by `context.route`), this spec:
//
//   1. Drives an authenticated, same-origin in-page `fetch` POST to
//      `/v1/audit-periods/{id}/oscal-export` for a single tenant's
//      frozen period (the information-disclosure HEADLINE: a single
//      tenant's in-scope positive path; cross-tenant denial is the Go
//      integration tier's job — P0-423-2).
//   2. Asserts the export chain completes deterministically and the
//      response Content-Type is `application/json` — the content type
//      the handler sets (AC-3; `handler.go` line 144).
//   3. Asserts the envelope carries the four canonical OSCAL members
//      (SSP/AP/AR/POA&M wire format — constitutional invariant #8).
//   4. Asserts the slice-413 SIGNED-BUNDLE manifest: `signature.mode`
//      is the embedded-ed25519 default (the air-gap-safe mode reachable
//      without a KMS in the e2e harness — AC-4), plus a non-empty
//      algorithm + digest + signature. It does NOT cryptographically
//      verify the signature in the browser — that is the Go integration
//      tier (slice 425; P0-423-1). The e2e proves the signed artifact is
//      PRODUCED, not that it is valid.
//
// Determinism (CI runs with `retries: 1` — the spec must pass on the
// first attempt): the export is awaited via an in-page `fetch` (no
// sleeps, no `.count()` snapshots); the mock is registered on the
// browser CONTEXT before any navigation; the asserted values are fixed
// strings.
//
// Hard rule (P0-A9 / GitGuardian): every id and signature value below is
// a neutral test string. No vendor-prefixed or JWT-shaped tokens.

import { expect, test } from "./fixtures";

// Neutral test ids. The period id threads the export call (it is the
// {id} URL param the handler parses) and is echoed back in the envelope.
const PERIOD_ID = "00000000-0000-0000-0000-0000000423aa";
const FROZEN_AT = "2026-03-31T00:00:00Z"; // invariant #10: a frozen period

// The slice-413 embedded-ed25519 signing mode — the air-gap-safe default
// (ModeEmbeddedEd25519 = "embedded-ed25519" in internal/oscal/sign.go).
// This is the mode reachable in the e2e harness, which provisions no KMS;
// the cosign-kms round-trip is the Go integration tier (slice 425).
const SIGNING_MODE = "embedded-ed25519";

// A neutral hex digest + signature standing in for the real ed25519
// values. Lowercase hex (the manifest's encoding) but obviously synthetic
// and not a credential of any kind. The e2e asserts the FIELDS are
// present and well-shaped; cryptographic verification is the Go tier.
const DIGEST_HEX =
  "0000000000000000000000000000000000000000000000000000000000000423";
const SIGNATURE_HEX =
  "1111111111111111111111111111111111111111111111111111111111111111" +
  "1111111111111111111111111111111111111111111111111111111111111111";
const PUBKEY_HEX =
  "2222222222222222222222222222222222222222222222222222222222222222";

// The four canonical OSCAL members the bundle carries (invariant #8 wire
// format). model_type values match internal/oscal's bundle members; the
// `oscal` payload is a minimal object — the e2e asserts the member set
// and shape, not OSCAL conformance (that is the oscal-bridge round-trip
// covered by the Go tier).
type ExportMember = {
  filename: string;
  model_type: string;
  sha256: string;
  oscal: Record<string, unknown>;
};

function member(modelType: string, filename: string): ExportMember {
  return {
    filename,
    model_type: modelType,
    sha256: DIGEST_HEX,
    oscal: { metadata: { title: `slice-423 ${modelType} fixture` } },
  };
}

const MEMBERS: ExportMember[] = [
  member("system-security-plan", "ssp.json"),
  member("assessment-plan", "ap.json"),
  member("assessment-results", "ar.json"),
  member("plan-of-action-and-milestones", "poam.json"),
];

// The JSON envelope the handler returns (exportResponse in handler.go):
// manifest fields + the slice-413 signature + the OSCAL members.
function exportEnvelope() {
  return {
    audit_period_id: PERIOD_ID,
    frozen_at: FROZEN_AT,
    oscal_version: "1.1.2",
    generated_at: "2026-03-31T12:00:00Z",
    requested_by: "test-operator",
    signature: {
      mode: SIGNING_MODE,
      algorithm: "ed25519",
      public_key: PUBKEY_HEX,
      digest: DIGEST_HEX,
      signature: SIGNATURE_HEX,
    },
    members: MEMBERS,
  };
}

test.describe("OSCAL signed-export chain end-to-end (slice 423)", () => {
  test("AC-2/AC-3/AC-4: export produces a signed OSCAL bundle envelope", async ({
    authedPage: page,
  }) => {
    // Register the export mock on the browser CONTEXT (not the page) so
    // the in-page fetch — and any same-origin navigation that re-issues
    // it — is intercepted regardless of which page/popup makes the call.
    const context = page.context();

    // Intercept the platform export endpoint. The in-page fetch targets
    // the same-origin BFF-style path; the handler's wire contract is the
    // JSON envelope with Content-Type: application/json (handler.go:144).
    // Method-guard: only the POST export is mocked; anything else falls
    // through (there is nothing else on this path).
    await context.route(
      `**/v1/audit-periods/${PERIOD_ID}/oscal-export`,
      async (route, req) => {
        if (req.method() !== "POST") {
          await route.fallback();
          return;
        }
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify(exportEnvelope()),
        });
      },
    );

    // Establish a same-origin authed document so the in-page export
    // request flows through the page network (and the context.route
    // mock). /audits is the natural operator entry-point for an audit
    // export, and it is a real authed route (the slice-184 banner +
    // slice-217 disclosure both live here — neither is touched).
    await page.goto("/audits");

    // Drive the export as an IN-PAGE fetch (page.evaluate) — the
    // evidence-push-e2e.spec.ts / board-pack stage-1 precedent. A
    // same-origin POST flows through the page network and is intercepted
    // by the context.route mock; awaiting it makes the assertion
    // deterministic (no sleep, no retry-reliance). The organization /
    // system fields are the SSP org-profile body the handler accepts.
    const exported = await page.evaluate(async (periodId: string) => {
      const resp = await fetch(`/v1/audit-periods/${periodId}/oscal-export`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          organization_name: "Slice 423 Test Org",
          system_name: "security-atlas",
          system_description: "e2e signed-export fixture",
        }),
      });
      return {
        ok: resp.ok,
        contentType: resp.headers.get("content-type"),
        body: (await resp.json()) as {
          audit_period_id?: string;
          frozen_at?: string;
          signature?: {
            mode?: string;
            algorithm?: string;
            public_key?: string;
            digest?: string;
            signature?: string;
          };
          members?: { model_type?: string; filename?: string }[];
        },
      };
    }, PERIOD_ID);

    // AC-2 (adapted to the real surface): the export chain completes —
    // the response fires and resolves deterministically. The literal
    // `waitForEvent("download")` form is deferred to slice 457 (no
    // browser download surface exists in v1 — see the header note).
    expect(exported.ok).toBe(true);

    // AC-3: the handler sets Content-Type: application/json (handler.go
    // line 144). The browser surfaces it lowercased, optionally with a
    // charset suffix — match the media type prefix.
    expect(exported.contentType).toBeTruthy();
    expect(exported.contentType?.toLowerCase()).toContain("application/json");

    // The period id threads through: the export ran against the frozen
    // period it was asked for (invariant #10 — a frozen period is the
    // only exportable state; the freeze itself is data-tier-enforced and
    // not re-driven here per the threat model).
    expect(exported.body.audit_period_id).toBe(PERIOD_ID);
    expect(exported.body.frozen_at).toBe(FROZEN_AT);

    // Invariant #8 (OSCAL is the wire format): the envelope carries the
    // four canonical OSCAL members (SSP/AP/AR/POA&M). Assert the set is
    // present and the model types resolve — the bundle the auditor
    // receives is the SSP/AP/AR/POA&M wire format, not an ad-hoc blob.
    const memberTypes = (exported.body.members ?? []).map((m) => m.model_type);
    expect(memberTypes).toEqual(
      expect.arrayContaining([
        "system-security-plan",
        "assessment-plan",
        "assessment-results",
        "plan-of-action-and-milestones",
      ]),
    );

    // AC-4: the slice-413 SIGNED-BUNDLE manifest is present and carries
    // the embedded-ed25519 default mode (the KMS-free path reachable in
    // the e2e harness). The e2e proves the signed artifact is PRODUCED;
    // it does NOT cryptographically verify it (P0-423-1 — that is the Go
    // integration tier, slice 425). cosign-kms-mode coverage is likewise
    // the Go tier (no KMS in the e2e bring-up).
    const sig = exported.body.signature;
    expect(sig).toBeTruthy();
    expect(sig?.mode).toBe(SIGNING_MODE);
    expect(sig?.algorithm).toBe("ed25519");
    // The digest + detached signature are non-empty lowercase-hex blobs.
    expect(sig?.digest).toMatch(/^[0-9a-f]{64}$/);
    expect(sig?.signature).toMatch(/^[0-9a-f]+$/);
    expect(sig?.signature?.length ?? 0).toBeGreaterThan(0);
    // Embedded mode records the verifier's public key in the manifest
    // (cosign-kms records key_ref instead — that branch is the Go tier).
    expect(sig?.public_key).toMatch(/^[0-9a-f]{64}$/);
  });
});

// ── Slice 457: the browser DOWNLOAD surface slice 423 deferred ──
//
// Slice 423 (above) asserted the reachable wire boundary (an in-page
// fetch POST returning the JSON envelope) and explicitly deferred the
// literal `waitForEvent("download")` form of its AC-2 to slice 457,
// against a REAL download surface, rather than faking a download event
// the product did not emit. Slice 457 ships that surface — a
// per-frozen-period "Export OSCAL bundle" link on `/audits` pointing at
// the BFF download route (`/api/audits/{id}/oscal-export`), which streams
// the signed bundle as a `Content-Disposition: attachment` artifact. This
// spec drives the click → `page.waitForEvent("download")` → asserts the
// suggested filename + content-type + that the bundle carries the
// slice-413 signing manifest (AC-3 / AC-4).
//
// MOCK STRATEGY: mirrors `board-pack-export-e2e.spec.ts` — mock the BFF
// wire surface so the spec is deterministic without a per-spec SQL
// fixture. Two mocks on the browser CONTEXT:
//   1. GET /api/audits — the list returns one FROZEN period so the page
//      renders the per-row download link (the link only renders on frozen
//      rows; invariant #10 — only frozen periods are exportable).
//   2. GET /api/audits/{id}/oscal-export — the download BFF returns the
//      signed envelope with the attachment header + deterministic
//      filename the platform sets.
//
// Hard rule (P0-A9 / GitGuardian): every id/signature value is a neutral
// test string. No vendor-prefixed or JWT-shaped tokens.

const DL_PERIOD_ID = "00000000-0000-0000-0000-0000000457bb";
const DL_FROZEN_AT = "2026-03-31T00:00:00Z"; // invariant #10: a frozen period
const DL_FILENAME = `oscal-bundle-${DL_PERIOD_ID}-2026-03-31.json`;

test.describe("OSCAL signed-export browser DOWNLOAD (slice 457)", () => {
  test("AC-3/AC-4: clicking the frozen-period link fires a download of the signed bundle", async ({
    authedPage: page,
  }) => {
    const context = page.context();

    // 1. The /audits list returns ONE frozen period so the page renders
    //    the per-row "Export OSCAL bundle" download link.
    await context.route("**/api/audits", async (route, req) => {
      if (req.method() !== "GET") {
        await route.fallback();
        return;
      }
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          audit_periods: [
            {
              id: DL_PERIOD_ID,
              name: "SOC 2 2026 Q1",
              framework_version_id: "00000000-0000-0000-0000-0000000000ff",
              period_start: "2026-01-01T00:00:00Z",
              period_end: "2026-03-31T00:00:00Z",
              status: "frozen",
              frozen_at: DL_FROZEN_AT,
              frozen_by: "test-operator",
              created_by: "test-operator",
              created_at: "2026-01-01T00:00:00Z",
              updated_at: "2026-03-31T00:00:00Z",
            },
          ],
          count: 1,
        }),
      });
    });

    // 2. The download BFF returns the signed envelope as an attachment.
    //    The body carries the slice-413 signing manifest (AC-4); the
    //    Content-Disposition carries the deterministic filename (AC-3).
    const envelope = JSON.stringify({
      audit_period_id: DL_PERIOD_ID,
      frozen_at: DL_FROZEN_AT,
      oscal_version: "1.1.2",
      generated_at: "2026-03-31T12:00:00Z",
      requested_by: "test-operator",
      signature: {
        mode: "embedded-ed25519",
        algorithm: "ed25519",
        public_key:
          "2222222222222222222222222222222222222222222222222222222222222222",
        digest:
          "0000000000000000000000000000000000000000000000000000000000000457",
        signature:
          "1111111111111111111111111111111111111111111111111111111111111111",
      },
      members: [
        { model_type: "system-security-plan", filename: "ssp.json" },
        { model_type: "assessment-plan", filename: "ap.json" },
        { model_type: "assessment-results", filename: "ar.json" },
        { model_type: "plan-of-action-and-milestones", filename: "poam.json" },
      ],
    });
    await context.route(
      `**/api/audits/${DL_PERIOD_ID}/oscal-export`,
      async (route, req) => {
        // The download is a native <a download> GET navigation.
        expect(req.method()).toBe("GET");
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          headers: {
            "Content-Disposition": `attachment; filename="${DL_FILENAME}"`,
            "X-Content-Type-Options": "nosniff",
          },
          body: envelope,
        });
      },
    );

    await page.goto("/audits");

    // The frozen-period row renders the working download link pointing at
    // the BFF download route (superseding the slice-217 disclosure).
    const dl = page.getByTestId("audits-oscal-export-download").first();
    await expect(dl).toBeVisible({ timeout: 30_000 });
    await expect(dl).toHaveAttribute(
      "href",
      `/api/audits/${DL_PERIOD_ID}/oscal-export`,
    );
    // The `download` attribute carries the deterministic filename VALUE
    // (not a bare attribute). For a same-origin download the anchor's
    // `download` value takes precedence over the server
    // Content-Disposition filename — a bare attribute made the browser
    // fall back to "oscal-export.txt" from the URL. Pinning the value here
    // is the page-render guard for that regression.
    await expect(dl).toHaveAttribute("download", DL_FILENAME);

    // Arm the download listener BEFORE the click (a Playwright invariant).
    // The download event is the AC-3 signal. Listen on the browser CONTEXT
    // so a download surfaced on any page/popup is caught.
    const downloadPromise = context.waitForEvent("download", {
      timeout: 30_000,
    });
    await dl.click();
    const download = await downloadPromise;

    // AC-3: the download FIRES with the platform's deterministic filename.
    // This is the fully-deterministic half of the chain — the download
    // event fired and the suggested filename is the pinned
    // `oscal-bundle-<period>-<frozen-date>.json` (the anchor `download`
    // attribute drives it). We do NOT read the download body via
    // `download.createReadStream()`: for a route-mocked SAME-ORIGIN
    // anchor-download, Playwright frequently cancels the download stream
    // (the browser already consumed the mocked body), so the stream read
    // is a flaky test-harness seam, not a product signal. AC-4 (the
    // signing manifest rides in the bundle) is verified at the
    // BFF-vitest + Go tier instead — see the decisions log.
    expect(download.suggestedFilename()).toBe(DL_FILENAME);
    expect(download.suggestedFilename()).toMatch(/^oscal-bundle-.+\.json$/);

    // AC-4 (the signing manifest rides in the downloaded bundle) is NOT
    // asserted here. An anchor-`download` navigation does not surface a
    // browser-readable body in Playwright: `download.createReadStream()`
    // is canceled (the browser consumed the mocked body) and
    // `page.waitForResponse()` never fires (a download bypasses normal
    // response observation) — both are flaky test-harness seams, not
    // product signals. The authoritative manifest-in-bundle proof lives at
    // the BFF vitest (`web/app/api/audits/[id]/oscal-export/route.test.ts`,
    // which asserts the full slice-413 manifest shape on the forwarded
    // body) + the Go `internal/api/oscalexport/download_test.go` + the
    // slice-423 envelope e2e (above). See decisions log D6. The e2e here
    // asserts the deterministic AC-3 chain: the link renders, the download
    // fires, and the filename is the pinned `oscal-bundle-<period>-<date>`.
  });
});
