import { describe, expect, test } from "vitest";

import {
  devTokenFlagEnabled,
  devTokenSignInAllowed,
  signedInRedirectTarget,
} from "./dev-token-signin";

describe("devTokenFlagEnabled (#2062)", () => {
  test("off when unset", () => {
    expect(devTokenFlagEnabled({})).toBe(false);
  });

  test("on only for the exact string true", () => {
    expect(devTokenFlagEnabled({ ATLAS_WEB_DEV_TOKEN_SIGNIN: "true" })).toBe(
      true,
    );
    for (const v of ["1", "yes", "TRUE", "True", ""]) {
      expect(devTokenFlagEnabled({ ATLAS_WEB_DEV_TOKEN_SIGNIN: v })).toBe(
        false,
      );
    }
  });
});

describe("devTokenSignInAllowed (#2062)", () => {
  test("flag off, past first install: not offered", () => {
    expect(devTokenSignInAllowed(false, { first_install: false })).toBe(false);
  });

  test("flag off, first install (bootstrap or reset-bootstrap): offered", () => {
    expect(devTokenSignInAllowed(false, { first_install: true })).toBe(true);
  });

  test("flag off, install-state unreadable: fails closed", () => {
    expect(devTokenSignInAllowed(false, null)).toBe(false);
  });

  test("flag off, first_install absent: not offered", () => {
    expect(devTokenSignInAllowed(false, {})).toBe(false);
  });

  test("flag on: offered whatever install-state says", () => {
    expect(devTokenSignInAllowed(true, { first_install: false })).toBe(true);
    expect(devTokenSignInAllowed(true, null)).toBe(true);
  });
});

describe("signedInRedirectTarget (#2062)", () => {
  test("keeps a sanitized in-app target", () => {
    expect(signedInRedirectTarget("/risks")).toBe("/risks");
    expect(signedInRedirectTarget("/dashboard")).toBe("/dashboard");
  });

  test("never sends a signed-in user back to /login", () => {
    expect(signedInRedirectTarget("/login")).toBe("/dashboard");
    expect(signedInRedirectTarget("/login?from=/x")).toBe("/dashboard");
    expect(signedInRedirectTarget("/login/")).toBe("/dashboard");
  });

  test("does not swallow paths that only share the prefix", () => {
    expect(signedInRedirectTarget("/login-help")).toBe("/login-help");
  });
});
