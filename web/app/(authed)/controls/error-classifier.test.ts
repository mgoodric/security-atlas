// Slice 152 — vitest coverage for the /controls error classifier.
//
// Pure-data tests. No React, no DOM, no fetch. Matches the
// filters.test.ts precedent in this same directory (slice 098).
//
// All fixtures are neutral identifiers — no vendor token prefixes
// (slice 098 P0-A5 + slice 141 P0-SCOPE-5).

import { describe, expect, test } from "vitest";

import { APIError } from "@/lib/api/base";

import { classifyControlDetailError } from "./error-classifier";

describe("classifyControlDetailError", () => {
  test("404 APIError -> notfound (the empty-state branch)", () => {
    const err = new APIError(404, "control not found");
    expect(classifyControlDetailError(err)).toBe("notfound");
  });

  test("401 APIError -> unauthorized (the /login redirect branch)", () => {
    const err = new APIError(401, "unauthenticated");
    expect(classifyControlDetailError(err)).toBe("unauthorized");
  });

  test("500 APIError -> other (the destructive Alert branch)", () => {
    const err = new APIError(500, "upstream error");
    expect(classifyControlDetailError(err)).toBe("other");
  });

  test("403 APIError -> other (forbidden is not an empty-state)", () => {
    // A 403 from the control-coverage handler means RLS denied the
    // caller — that is NOT an empty-state (the control may exist, the
    // caller just cannot see it). Treat as a destructive error so the
    // operator sees the underlying message rather than a misleading
    // "no control instantiated yet" empty-state.
    const err = new APIError(403, "forbidden");
    expect(classifyControlDetailError(err)).toBe("other");
  });

  test("400 APIError -> other (bad request is not an empty-state)", () => {
    // The backend returns 400 when the path segment is not a UUID
    // (internal/api/ucfcoverage/handlers.go ControlCoverage). Show the
    // raw message so the operator sees what was wrong, rather than
    // empty-stating a malformed URL.
    const err = new APIError(400, "control id must be a UUID");
    expect(classifyControlDetailError(err)).toBe("other");
  });

  test("non-APIError thrown value -> other (network failure path)", () => {
    // TanStack Query surfaces non-APIError throws (e.g. TypeError from
    // a network failure inside fetch). The classifier must NOT empty-
    // state those — the operator needs to see the network error so
    // they can act on it.
    expect(classifyControlDetailError(new Error("network down"))).toBe("other");
    expect(classifyControlDetailError(new TypeError("Failed to fetch"))).toBe(
      "other",
    );
  });

  test("null and undefined -> other (defensive path)", () => {
    // TanStack Query's `error` field is `Error | null`; an unset error
    // should never reach this helper, but if it does the helper must
    // not classify a missing error as a notfound (which would render
    // an empty-state for a successful query).
    expect(classifyControlDetailError(null)).toBe("other");
    expect(classifyControlDetailError(undefined)).toBe("other");
  });

  test("APIError with status 0 -> other (network-level APIError)", () => {
    // bffControlFetch in web/lib/api.ts constructs APIError(status,
    // msg) from `res.status` which is always >= 200 for an HTTP
    // response. A status of 0 would indicate a non-HTTP construction
    // path; the classifier must not treat that as a notfound.
    const err = new APIError(0, "no response");
    expect(classifyControlDetailError(err)).toBe("other");
  });
});
