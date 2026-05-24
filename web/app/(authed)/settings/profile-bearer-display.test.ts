// Slice 250 -- vitest coverage for profile-bearer-display.ts.
//
// Banner-copy invariants + the credentialBearerLabel helper. The
// Playwright spec asserts the exported constants by-reference so the
// banner text is bound to one source-of-truth string; this file binds
// the tone-discipline ban list + the label-formatting edges.

import { describe, expect, it } from "vitest";

import {
  PROFILE_CREDENTIAL_BANNER_BODY,
  PROFILE_CREDENTIAL_BANNER_TITLE,
  credentialBearerLabel,
} from "./profile-bearer-display";

describe("Profile-section credential-bearer banner copy", () => {
  it("title identifies the bearer as a credential (not a person)", () => {
    expect(PROFILE_CREDENTIAL_BANNER_TITLE).toBe(
      "You are signed in as a credential",
    );
  });

  it("body explains WHAT + WHY + HOW without marketing tone", () => {
    // Required content phrases (load-bearing for operator comprehension).
    expect(PROFILE_CREDENTIAL_BANNER_BODY).toContain("credential");
    expect(PROFILE_CREDENTIAL_BANNER_BODY).toContain("not a person");
    expect(PROFILE_CREDENTIAL_BANNER_BODY).toContain("identity provider");

    // Banned phrases (CLAUDE.md "Board-narrative AI-assist" tone list).
    const lower = PROFILE_CREDENTIAL_BANNER_BODY.toLowerCase();
    for (const banned of [
      "proud",
      "industry-leading",
      "best-in-class",
      "world-class",
      "robust",
      "leverage",
      "exceeded expectations",
    ]) {
      expect(lower).not.toContain(banned);
    }
  });

  it("body does NOT echo the raw 'API key ' display_name with trailing space (P0-250-1: no fabrication, no platform-string echo)", () => {
    // The live /v1/me sample literally returns `display_name: "API key "`.
    // The banner copy must explain the situation in its own words rather
    // than parroting the degenerate platform string.
    expect(PROFILE_CREDENTIAL_BANNER_BODY).not.toContain('"API key "');
  });
});

describe("credentialBearerLabel", () => {
  it("formats a well-shaped 'API key <last4>' as 'API key …<last4>'", () => {
    expect(credentialBearerLabel("API key 1f3a")).toBe("API key …1f3a");
  });

  it("returns plain 'API key' for the degenerate live sample (display_name = 'API key ')", () => {
    // The slice 250 spec's verified observation block shows this
    // exact value. We MUST NOT render "API key …" (trailing ellipsis
    // with nothing after) or fabricate a fake last4.
    expect(credentialBearerLabel("API key ")).toBe("API key");
  });

  it("returns plain 'API key' for an undefined display_name", () => {
    expect(credentialBearerLabel(undefined)).toBe("API key");
  });

  it("returns plain 'API key' for an empty-string display_name", () => {
    expect(credentialBearerLabel("")).toBe("API key");
  });

  it("returns plain 'API key' for a non-API-key display_name (defensive fallback)", () => {
    // If the platform ever changes the credential's display_name shape,
    // the label still falls back to a generic identifier instead of
    // surfacing the unexpected string.
    expect(credentialBearerLabel("bootstrap-admin-2025")).toBe("API key");
  });
});
