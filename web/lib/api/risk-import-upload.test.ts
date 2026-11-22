// #2848 — the risk import upload page's pure helpers.

import { describe, expect, test } from "vitest";

import {
  createChoice,
  needsValueStep,
  riskMappingFields,
  slugFor,
  uploadForm,
  type RiskImportPreview,
} from "./risk-import-upload";
import type { RiskImportBatch } from "./risk-imports";

const preview = (fields: RiskImportPreview["fields"]): RiskImportPreview => ({
  methodology: "nist_800_30",
  header_row: 1,
  header_detected: true,
  columns: [],
  fields,
  rows: [],
  data_rows: 0,
});

describe("risk import upload helpers", () => {
  test("riskMappingFields labels and orders the platform's fields", () => {
    const got = riskMappingFields(
      preview([
        { name: "review_due", required: false },
        { name: "title", required: true },
        { name: "brand_new_field", required: false },
        { name: "category", required: true },
      ]),
    );
    expect(got).toEqual([
      { name: "title", label: "Title", required: true },
      { name: "category", label: "Category", required: true },
      { name: "review_due", label: "Review due", required: false },
      { name: "brand_new_field", label: "brand_new_field", required: false },
    ]);
  });

  test("uploadForm carries the file, methodology and mapping", () => {
    const f = uploadForm(new File(["x"], "r.csv"), "fair", {
      columnMap: ["title=What"],
      ignoreColumn: ["Notes"],
    });
    expect((f.get("file") as File).name).toBe("r.csv");
    expect(f.get("methodology")).toBe("fair");
    expect(f.getAll("column_map")).toEqual(["title=What"]);
    expect(f.getAll("ignore_column")).toEqual(["Notes"]);
    expect(uploadForm(new File(["x"], "r.csv"), "fair").has("column_map")).toBe(
      false,
    );
  });

  test("slugFor fits the platform's slug pattern", () => {
    const re = /^[a-z0-9][a-z0-9_-]{0,62}$/;
    for (const [v, want] of [
      ["Operations", "operations"],
      ["  Third-party / Vendor  ", "third-party-vendor"],
      ["--IT & Ops!", "it-ops"],
    ]) {
      expect(slugFor(v)).toBe(want);
      expect(slugFor(v)).toMatch(re);
    }
    expect(slugFor("a".repeat(80))).toHaveLength(63);
  });

  test("createChoice: a category takes a slug and its name, a theme a slug-shaped name", () => {
    expect(createChoice("category", " Ops Risk ")).toEqual({
      create: { slug: "ops-risk", name: "Ops Risk", description: "" },
    });
    expect(createChoice("theme", "Cloud Sprawl")).toEqual({
      create: { name: "cloud-sprawl", description: "" },
    });
  });

  test("needsValueStep while any value maps to nothing", () => {
    const b = (targets: (string | undefined)[]) =>
      ({
        category_values: targets.map((t, i) => ({
          kind: "category",
          value: `v${i}`,
          row_count: 1,
          target: t,
        })),
        theme_values: null,
      }) as unknown as RiskImportBatch;
    expect(needsValueStep(b(["operational", undefined]))).toBe(true);
    expect(needsValueStep(b(["operational"]))).toBe(false);
    expect(needsValueStep(b([]))).toBe(false);
  });
});
