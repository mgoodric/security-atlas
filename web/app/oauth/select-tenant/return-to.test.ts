// return-to.test.ts — #2211: the select-tenant page routes `return_to`
// through safeRedirectTarget (wired in #2197, untested until now).

import { describe, expect, it } from "vitest";

import { selectTenantReturnTo } from "./return-to";

function params(returnTo: string | null): URLSearchParams {
  const p = new URLSearchParams();
  if (returnTo !== null) p.set("return_to", returnTo);
  return p;
}

describe("selectTenantReturnTo", () => {
  it.each([
    ["absolute URL", "https://evil.example"],
    ["protocol-relative", "//evil.example"],
    ["backslash", "/\\evil.example"],
    ["javascript: scheme", "javascript:alert(1)"],
    ["encoded slashes", "%2F%2Fevil.example"],
    ["tab prefix", "\t//evil.example"],
    ["newline prefix", "\n//evil.example"],
    ["tab inside the slashes", "/\t/evil.example"],
  ])("rejects %s to /dashboard", (_name, target) => {
    expect(selectTenantReturnTo(params(target))).toBe("/dashboard");
  });

  it("keeps a same-origin path", () => {
    expect(selectTenantReturnTo(params("/risks/123?tab=open"))).toBe(
      "/risks/123?tab=open",
    );
  });

  it("defaults to /dashboard when return_to is absent or params are null", () => {
    expect(selectTenantReturnTo(params(null))).toBe("/dashboard");
    expect(selectTenantReturnTo(null)).toBe("/dashboard");
  });
});
