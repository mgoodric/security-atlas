import { describe, expect, test } from "vitest";

import {
  attestationFields,
  isManualAttestation,
  resultBadgeVariant,
  safeHttpUrl,
} from "./view";

describe("safeHttpUrl", () => {
  test("allows http and https", () => {
    expect(safeHttpUrl("https://example.test/a")).toBe(
      "https://example.test/a",
    );
    expect(safeHttpUrl("http://example.test/")).toBe("http://example.test/");
  });
  test.each([
    "javascript:alert(1)",
    "data:text/html,x",
    "ftp://x/",
    "/relative",
    "",
    42,
    null,
  ])("rejects %s", (raw) => {
    expect(safeHttpUrl(raw)).toBeNull();
  });
});

describe("attestationFields", () => {
  test("reads the attestation fields", () => {
    expect(
      attestationFields({
        attestor: "demo-operator@example.invalid",
        statement: "Reviewed",
        supporting_uri: "https://example.invalid/r",
      }),
    ).toEqual({
      attestor: "demo-operator@example.invalid",
      statement: "Reviewed",
      supportingUri: "https://example.invalid/r",
    });
  });
  test("missing or non-string fields are null", () => {
    expect(attestationFields({ statement: 3 })).toEqual({
      attestor: null,
      statement: null,
      supportingUri: null,
    });
    expect(attestationFields(null).statement).toBeNull();
  });
});

describe("isManualAttestation", () => {
  test("matches manual.attestation kinds only", () => {
    expect(isManualAttestation("manual.attestation.v1")).toBe(true);
    expect(isManualAttestation("aws.iam.mfa")).toBe(false);
    expect(isManualAttestation(null)).toBe(false);
  });
});

describe("resultBadgeVariant", () => {
  test("maps every evidence result", () => {
    expect(resultBadgeVariant("pass")).toBe("pass");
    expect(resultBadgeVariant("fail")).toBe("critical");
    expect(resultBadgeVariant("inconclusive")).toBe("warning");
    expect(resultBadgeVariant("na")).toBe("secondary");
    expect(resultBadgeVariant("unknown")).toBe("secondary");
  });
});
