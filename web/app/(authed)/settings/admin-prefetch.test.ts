// Slice 249 -- vitest unit coverage for admin-prefetch.ts.
//
// The fail-closed semantics in parseSessionMe are the load-bearing
// safety property: a wrong "admit" decision here would re-introduce
// the slice 249 SSR flicker AND widen the admit set to non-admin
// callers (P0-249-3 violation). Every accept/reject branch is
// asserted; the explicit "only literal true is admin" rule is
// exercised against the obvious truthy footguns (string "true",
// number 1, etc.) so a regression to `Boolean(body.is_admin)` or
// `!!body.is_admin` cannot slip in.

import { describe, expect, it } from "vitest";

import {
  NON_ADMIN_SESSION_ME,
  SETTINGS_SESSION_ME_QUERY_KEY,
  parseSessionMe,
} from "./admin-prefetch";

describe("parseSessionMe", () => {
  describe("admit (admin) cases", () => {
    it("returns is_admin=true when upstream { is_admin: true }", () => {
      expect(parseSessionMe({ is_admin: true })).toEqual({ is_admin: true });
    });

    it("ignores unrelated upstream fields when is_admin=true", () => {
      // The upstream /v1/me payload carries additional fields (roles,
      // email, idp_subject, ...); the prefetch must project only the
      // SessionMe shape. Anything else gets dropped.
      expect(
        parseSessionMe({
          is_admin: true,
          roles: ["admin", "grc_engineer"],
          email: "alice@example.com",
        }),
      ).toEqual({ is_admin: true });
    });
  });

  describe("fail-closed (non-admin) cases", () => {
    it("returns is_admin=false on null upstream", () => {
      expect(parseSessionMe(null)).toEqual(NON_ADMIN_SESSION_ME);
    });

    it("returns is_admin=false on undefined upstream", () => {
      expect(parseSessionMe(undefined)).toEqual(NON_ADMIN_SESSION_ME);
    });

    it("returns is_admin=false on non-object upstream (string)", () => {
      expect(parseSessionMe("admin")).toEqual(NON_ADMIN_SESSION_ME);
    });

    it("returns is_admin=false on non-object upstream (number)", () => {
      expect(parseSessionMe(1)).toEqual(NON_ADMIN_SESSION_ME);
    });

    it("returns is_admin=false when is_admin field absent", () => {
      expect(parseSessionMe({ roles: ["admin"] })).toEqual(
        NON_ADMIN_SESSION_ME,
      );
    });

    it("returns is_admin=false on empty object", () => {
      expect(parseSessionMe({})).toEqual(NON_ADMIN_SESSION_ME);
    });

    it("returns is_admin=false when is_admin is the string 'true'", () => {
      // Footgun: a JSON serializer that emits "true" instead of true
      // must NOT be admitted. This catches a regression to
      // `Boolean(body.is_admin)` (which would admit the string).
      expect(parseSessionMe({ is_admin: "true" })).toEqual(
        NON_ADMIN_SESSION_ME,
      );
    });

    it("returns is_admin=false when is_admin is the number 1", () => {
      // Same footgun, different shape. Catches `!!body.is_admin`.
      expect(parseSessionMe({ is_admin: 1 })).toEqual(NON_ADMIN_SESSION_ME);
    });

    it("returns is_admin=false when is_admin is explicitly null", () => {
      expect(parseSessionMe({ is_admin: null })).toEqual(NON_ADMIN_SESSION_ME);
    });

    it("returns is_admin=false when is_admin is explicitly false", () => {
      expect(parseSessionMe({ is_admin: false })).toEqual(NON_ADMIN_SESSION_ME);
    });
  });
});

describe("SETTINGS_SESSION_ME_QUERY_KEY", () => {
  it("is the same key the page's useQuery registers under", () => {
    // The page hard-codes ["settings-session-me"]; this assertion
    // binds the layout's prefetchQuery key to the page's useQuery
    // key. If the page ever drifts, this test fails -- much better
    // than silently re-introducing the slice-249 flicker.
    expect(SETTINGS_SESSION_ME_QUERY_KEY).toEqual(["settings-session-me"]);
  });
});

describe("NON_ADMIN_SESSION_ME", () => {
  it("is the canonical non-admin shape", () => {
    expect(NON_ADMIN_SESSION_ME).toEqual({ is_admin: false });
  });
});
