// Slice 263 — vitest coverage for the pure validation helpers in
// components/questionnaire/upload-zone.tsx.
//
// The UploadZone component itself is React; the load-bearing rules
// live in `validateFile` + `formatValidationError`, which are pure
// helpers exported for unit coverage (node env, no DOM).

import { describe, expect, test } from "vitest";

import { formatValidationError, validateFile } from "./upload-zone";

function makeFile(name: string, size: number): File {
  const blob = new Blob([new Uint8Array(size)], {
    type: "application/octet-stream",
  });
  return new File([blob], name, { type: blob.type });
}

describe("validateFile", () => {
  test("accepts a 1KB .xlsx file", () => {
    const f = makeFile("caiq.xlsx", 1024);
    expect(validateFile(f)).toBeNull();
  });

  test("accepts uppercase .XLSX extension", () => {
    const f = makeFile("CAIQ.XLSX", 1024);
    expect(validateFile(f)).toBeNull();
  });

  test("rejects file exceeding 5MB", () => {
    const f = makeFile("big.xlsx", 6 * 1024 * 1024);
    const err = validateFile(f);
    expect(err).not.toBeNull();
    expect(err?.kind).toBe("too_large");
  });

  test("rejects exactly 5MB+1byte", () => {
    const f = makeFile("edge.xlsx", 5 * 1024 * 1024 + 1);
    expect(validateFile(f)?.kind).toBe("too_large");
  });

  test("accepts exactly 5MB", () => {
    const f = makeFile("edge.xlsx", 5 * 1024 * 1024);
    expect(validateFile(f)).toBeNull();
  });

  test("rejects non-.xlsx extension (.csv)", () => {
    const f = makeFile("data.csv", 1024);
    expect(validateFile(f)?.kind).toBe("wrong_extension");
  });

  test("rejects non-.xlsx extension (.docx)", () => {
    const f = makeFile("policy.docx", 1024);
    expect(validateFile(f)?.kind).toBe("wrong_extension");
  });
});

describe("formatValidationError", () => {
  test("too_large copy", () => {
    expect(
      formatValidationError({ kind: "too_large", sizeBytes: 6_000_000 }),
    ).toContain("5MB");
  });

  test("wrong_extension copy", () => {
    expect(
      formatValidationError({ kind: "wrong_extension", filename: "x.csv" }),
    ).toContain(".xlsx");
  });
});
