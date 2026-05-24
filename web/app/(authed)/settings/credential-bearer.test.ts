// Slice 250 -- vitest coverage for credential-bearer.ts.
//
// Pure-function tests for the shared synthetic-credential detection
// helper consumed by both the Profile section (slice 250) and the
// Notifications section (slice 251 -- via re-export from
// notif-bearer-mode.ts).
//
// AC-4 of the slice spec requires at minimum: OIDC-human-yes -> false,
// credential-bearer-yes -> true, empty-state -> false, partial-empty ->
// false. The cases below cover those four plus the defensive whitespace
// + undefined + display-name-helper edges.

import { describe, expect, it } from "vitest";

import type { MeProfile } from "@/lib/api";

import {
  credentialDisplayLast4,
  isCredentialBearer,
} from "./credential-bearer";

// Helpers -- the two canonical shapes the predicate must distinguish.
// These mirror the analogous helpers in `notif-bearer-mode.test.ts` so
// any drift between the two test files is obvious in code review.

function realOidcUser(): Pick<
  MeProfile,
  "idp_subject" | "email" | "display_name"
> {
  return {
    idp_subject: "okta|00u4f2",
    email: "sam.rivera@sentinellabs.example",
    display_name: "Sam Rivera",
  };
}

function syntheticCredential(): Pick<
  MeProfile,
  "idp_subject" | "email" | "display_name"
> {
  return {
    idp_subject: "",
    email: "",
    display_name: "API key 1f3a",
  };
}

describe("isCredentialBearer", () => {
  // ---- AC-4 required cases ----

  it("returns true for the canonical synthetic credential shape (AC-4: credential-bearer-yes)", () => {
    expect(isCredentialBearer(syntheticCredential())).toBe(true);
  });

  it("returns false for a real OIDC-human user (AC-4: OIDC-human-yes -> false)", () => {
    expect(isCredentialBearer(realOidcUser())).toBe(false);
  });

  it("returns false for undefined profile (AC-4: empty-state -> false; caller handles loading)", () => {
    expect(isCredentialBearer(undefined)).toBe(false);
  });

  it("returns false when only idp_subject is empty (AC-4: partial-empty -> false; fail-open)", () => {
    // The synthetic shape requires BOTH idp_subject and email to be
    // empty. A profile that has email but no subject is shaped
    // unexpectedly -- treat as real-user (fail-open to existing
    // rendering) rather than silently hiding an honest user's
    // affordances. Matches slice 251 D2 rationale.
    expect(
      isCredentialBearer({
        idp_subject: "",
        email: "leftover@example.com",
        display_name: "(unset)",
      }),
    ).toBe(false);
  });

  // ---- Defensive whitespace cases ----

  it("treats whitespace-only idp_subject as empty", () => {
    expect(
      isCredentialBearer({
        idp_subject: "   ",
        email: "",
        display_name: "API key 1f3a",
      }),
    ).toBe(true);
  });

  it("treats whitespace-only email as empty", () => {
    expect(
      isCredentialBearer({
        idp_subject: "",
        email: "   ",
        display_name: "API key 1f3a",
      }),
    ).toBe(true);
  });

  it("returns false when only email is empty (partial-empty fail-open)", () => {
    expect(
      isCredentialBearer({
        idp_subject: "okta|00u4f2",
        email: "",
        display_name: "Sam Rivera",
      }),
    ).toBe(false);
  });

  // ---- Live-sample regression ----

  it("matches the live /v1/me sample from the slice 250 spec (display_name = 'API key ' with trailing space)", () => {
    // The spec's verified observation block captured this exact shape.
    // Whitespace in the display_name is irrelevant to the predicate --
    // the load-bearing signal is the two empty fields.
    expect(
      isCredentialBearer({
        idp_subject: "",
        email: "",
        display_name: "API key ",
      }),
    ).toBe(true);
  });
});

describe("credentialDisplayLast4", () => {
  it("extracts last4 from the canonical 'API key <last4>' display_name", () => {
    expect(credentialDisplayLast4("API key 1f3a")).toBe("1f3a");
  });

  it("trims whitespace inside the display_name", () => {
    expect(credentialDisplayLast4("  API key 1f3a  ")).toBe("1f3a");
  });

  it("returns empty string for the degenerate live sample ('API key ' with trailing space, no last4)", () => {
    expect(credentialDisplayLast4("API key ")).toBe("");
  });

  it("returns empty string for a non-API-key display_name", () => {
    expect(credentialDisplayLast4("Sam Rivera")).toBe("");
  });

  it("returns empty string for undefined display_name", () => {
    expect(credentialDisplayLast4(undefined)).toBe("");
  });

  it("returns empty string when the last4 payload is too long (defensive)", () => {
    // A future 'API key bootstrap-1f3a' should not silently surface
    // 'bootstrap-1f3a' as the last4. The cap is 8 alphanumerics.
    expect(credentialDisplayLast4("API key bootstrap-1f3a")).toBe("");
  });

  it("returns empty string when the last4 payload contains symbols (defensive)", () => {
    expect(credentialDisplayLast4("API key 1f-3a")).toBe("");
  });

  it("matches case-insensitively on the 'API key' prefix", () => {
    expect(credentialDisplayLast4("api key abcd")).toBe("abcd");
  });
});
