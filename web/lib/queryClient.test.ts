import { describe, expect, it } from "vitest";

import { APIError } from "@/lib/api/base";
import { FEATURE_DISABLED_MESSAGE } from "@/lib/feature-nav";
import { shouldRetryQuery } from "@/lib/queryClient";

// #2266: a gated module's 404 "feature disabled" is the platform's final
// answer. Retrying it held the page on its loading-state form for a
// second before it swapped to the disabled panel, which unmounted
// whatever had focus.
describe("shouldRetryQuery", () => {
  it("never retries a feature-disabled 404", () => {
    const err = new APIError(404, FEATURE_DISABLED_MESSAGE);
    expect(shouldRetryQuery(0, err)).toBe(false);
  });

  it("retries a 404 that is not the feature-disabled answer", () => {
    // Only the gate's exact body is final; any other 404 keeps the retry.
    expect(shouldRetryQuery(0, new APIError(404, "not found"))).toBe(true);
  });

  it("retries any other error once", () => {
    const err = new APIError(500, "boom");
    expect(shouldRetryQuery(0, err)).toBe(true);
    expect(shouldRetryQuery(1, err)).toBe(false);
    expect(shouldRetryQuery(0, new Error("network"))).toBe(true);
  });
});
