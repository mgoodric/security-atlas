// Slice 152 — pure error-classifier for the /controls list + detail
// pages.
//
// Co-located with the /controls page logic so the vitest scope already
// configured for `app/(authed)/**/*.test.ts` (see web/vitest.config.ts)
// picks it up without config changes. No JSX, no React, no fetch — just
// the discriminator the page consumers branch on.
//
// Why a helper rather than `if (err.status === 404)` inline at the call
// site: the empty-set robustness audit in slice 150 (D3) pinned the
// invariant that bare-`{id}` GETs return 404 on missing — that 404 is a
// load-bearing platform contract, not an error. The detail page renders
// it as an EMPTY-STATE (the operator clicked an SCF anchor that has no
// instantiated control in their tenant), not as a destructive error
// banner. Centralising the discriminator means a regression that
// mis-classifies 404 as 5xx (or vice-versa) gets caught by vitest, not
// by a Playwright run.
//
// The classifier is intentionally narrow: three cases — `notfound`,
// `unauthorized`, `other`. The detail page maps:
//
//   * `notfound`     -> friendly empty-state (slice 152 D1-c)
//   * `unauthorized` -> router.push("/login?from=…") (existing behaviour
//                       preserved; was previously gated by a separate
//                       useEffect; classifier preserves the contract so
//                       the test surface is one helper, not two paths)
//   * `other`        -> destructive Alert (existing behaviour)
//
// No vendor-prefixed tokens in tests (slice 098 / slice 141 P0-SCOPE-5
// pattern).

import { APIError } from "@/lib/api/base";

export type ControlDetailErrorClass = "notfound" | "unauthorized" | "other";

/**
 * Classify an error from any of the control-detail-page bound queries
 * into one of three buckets that the page UI branches on. Non-APIError
 * inputs (network failures, unexpected throws) fall through to "other"
 * so they render as a destructive Alert with the raw message — the
 * worst-case path stays visible, never silently empty-stated.
 *
 * @param err the error caught from a TanStack Query result; pass the
 *   error verbatim (the helper does the `instanceof APIError` check)
 * @returns one of the three classes; never throws.
 */
export function classifyControlDetailError(
  err: unknown,
): ControlDetailErrorClass {
  if (!(err instanceof APIError)) {
    return "other";
  }
  if (err.status === 404) {
    return "notfound";
  }
  if (err.status === 401) {
    return "unauthorized";
  }
  return "other";
}
