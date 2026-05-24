// Slice 251 -- vitest unit coverage for notif-bearer-mode.ts.
//
// Pure function tests. The settings page wires the helper into its
// NotificationsSection branch logic; this file exercises every
// transition + every edge of the synthetic-credential detection.

import { describe, expect, it } from "vitest";

import type { MeProfile } from "@/lib/api";

import {
  CREDENTIAL_BEARER_BANNER_BODY,
  CREDENTIAL_BEARER_BANNER_TITLE,
  isSyntheticCredentialProfile,
  notificationsRenderMode,
} from "./notif-bearer-mode";

// A fully-populated real-user profile for happy-path baselines.
function realUserProfile(): Pick<
  MeProfile,
  "idp_subject" | "email" | "display_name"
> {
  return {
    idp_subject: "okta|00u1abc",
    email: "sam@example.com",
    display_name: "Sam Rivera",
  };
}

// The exact synthetic shape returned by
// `internal/api/me/profile.go:269-282` for credential bearers.
function syntheticCredentialProfile(): Pick<
  MeProfile,
  "idp_subject" | "email" | "display_name"
> {
  return {
    idp_subject: "",
    email: "",
    display_name: "API key 1f3a",
  };
}

describe("isSyntheticCredentialProfile", () => {
  it("returns true for the canonical synthetic shape (empty idp_subject + empty email + API-key display_name)", () => {
    expect(isSyntheticCredentialProfile(syntheticCredentialProfile())).toBe(
      true,
    );
  });

  it("returns false for a real OIDC user profile", () => {
    expect(isSyntheticCredentialProfile(realUserProfile())).toBe(false);
  });

  it("returns false when idp_subject is non-empty (real OIDC user)", () => {
    expect(
      isSyntheticCredentialProfile({
        idp_subject: "google|123",
        email: "",
        display_name: "",
      }),
    ).toBe(false);
  });

  it("returns false when email is present but idp_subject is empty (defensive fail-open to full render)", () => {
    // This is an unexpected shape per the backend contract; the helper
    // declines to classify as credential to avoid hiding a real user's
    // Notifications section because of an upstream wire-shape drift.
    expect(
      isSyntheticCredentialProfile({
        idp_subject: "",
        email: "leftover@example.com",
        display_name: "(unset)",
      }),
    ).toBe(false);
  });

  it("treats whitespace-only idp_subject as empty (defensive)", () => {
    expect(
      isSyntheticCredentialProfile({
        idp_subject: "   ",
        email: "",
        display_name: "API key 1f3a",
      }),
    ).toBe(true);
  });

  it("treats whitespace-only email as empty (defensive)", () => {
    expect(
      isSyntheticCredentialProfile({
        idp_subject: "",
        email: "   ",
        display_name: "API key 1f3a",
      }),
    ).toBe(true);
  });
});

describe("notificationsRenderMode", () => {
  it("returns 'loading' while the profile query has not resolved", () => {
    expect(
      notificationsRenderMode({
        profileLoading: true,
        profileError: false,
        profile: undefined,
      }),
    ).toBe("loading");
  });

  it("returns 'error' when the profile query errored", () => {
    expect(
      notificationsRenderMode({
        profileLoading: false,
        profileError: true,
        profile: undefined,
      }),
    ).toBe("error");
  });

  it("returns 'credential' for the synthetic credential profile", () => {
    expect(
      notificationsRenderMode({
        profileLoading: false,
        profileError: false,
        profile: syntheticCredentialProfile(),
      }),
    ).toBe("credential");
  });

  it("returns 'full' for a real OIDC user profile (no regression)", () => {
    expect(
      notificationsRenderMode({
        profileLoading: false,
        profileError: false,
        profile: realUserProfile(),
      }),
    ).toBe("full");
  });

  it("returns 'credential' when the prefs query reports the documented credential error (corroborating signal)", () => {
    // Profile happens to look like a real user (wire-shape upstream
    // drift) but the prefs endpoint returns the credential error.
    // Trust the prefs signal -- it is the load-bearing surface
    // for this section.
    expect(
      notificationsRenderMode({
        profileLoading: false,
        profileError: false,
        profile: realUserProfile(),
        preferencesErrorMessage: "no preferences for this credential",
      }),
    ).toBe("credential");
  });

  it("matches the credential prefs error case-insensitively (BFF may upper-case)", () => {
    expect(
      notificationsRenderMode({
        profileLoading: false,
        profileError: false,
        profile: realUserProfile(),
        preferencesErrorMessage:
          "404 Not Found: No Preferences For This Credential",
      }),
    ).toBe("credential");
  });

  it("matches the credential prefs error as a substring (BFF status-code wrap)", () => {
    expect(
      notificationsRenderMode({
        profileLoading: false,
        profileError: false,
        profile: realUserProfile(),
        // BFF may wrap as "404 no preferences for this credential"
        preferencesErrorMessage: "404 no preferences for this credential",
      }),
    ).toBe("credential");
  });

  it("does NOT trigger credential mode on unrelated prefs errors (5xx network etc.)", () => {
    expect(
      notificationsRenderMode({
        profileLoading: false,
        profileError: false,
        profile: realUserProfile(),
        preferencesErrorMessage: "500 internal server error",
      }),
    ).toBe("full");
  });

  it("ignores an empty preferencesErrorMessage", () => {
    expect(
      notificationsRenderMode({
        profileLoading: false,
        profileError: false,
        profile: realUserProfile(),
        preferencesErrorMessage: "",
      }),
    ).toBe("full");
  });

  it("loading state takes precedence over a present synthetic profile shape", () => {
    // Defensive: a hypothetical wire shape that resolves before
    // loading flips false should not pre-empt the loading banner.
    expect(
      notificationsRenderMode({
        profileLoading: true,
        profileError: false,
        profile: syntheticCredentialProfile(),
      }),
    ).toBe("loading");
  });

  it("error state takes precedence over the credential signals", () => {
    expect(
      notificationsRenderMode({
        profileLoading: false,
        profileError: true,
        profile: syntheticCredentialProfile(),
        preferencesErrorMessage: "no preferences for this credential",
      }),
    ).toBe("error");
  });
});

describe("banner copy", () => {
  it("title is the slice's exact title string (consumed by the Playwright AC-4 spec)", () => {
    expect(CREDENTIAL_BEARER_BANNER_TITLE).toBe("Notifications are per-user");
  });

  it("body explains the inert state without superlatives or marketing tone", () => {
    // Tone discipline: no banned filler. The body is a single sentence
    // pair: WHAT the bearer is + WHY the section is inert + HOW to
    // remediate. Word count is bounded so future drift is visible in
    // the diff.
    expect(CREDENTIAL_BEARER_BANNER_BODY).toContain("credential");
    expect(CREDENTIAL_BEARER_BANNER_BODY).toContain("per user");
    expect(CREDENTIAL_BEARER_BANNER_BODY).toContain("identity provider");
    // Banned phrases (CLAUDE.md tone-anti-pattern list).
    const lower = CREDENTIAL_BEARER_BANNER_BODY.toLowerCase();
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

  it("body never echoes the raw platform error string (P0-251-2: API stays honest; UI translates)", () => {
    // The platform's "no preferences for this credential" wording is
    // an API-shape contract; the operator should not see that exact
    // string in the UI.
    expect(CREDENTIAL_BEARER_BANNER_BODY).not.toContain(
      "no preferences for this credential",
    );
  });
});
