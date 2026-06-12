// Slice 690 — contract-test-tier rollout (consumer side: GET
// /v1/samples/{id}/annotations, the auditor-decision list on a sample).
//
// PROVIDER: internal/api/audit/contractrecord_test.go records the real
// ListAnnotations handler's bodies into sample-annotations.golden.json.
//
// CONSUMER SHAPE (honest scoping — slice 687 D3): there is NO verbatim-
// passthrough Next.js BFF that consumes GET /v1/samples/{id}/annotations today
// — the annotation list is read by the sample-detail component, not a thin
// passthrough. So this CONSUMER half is a FIELD-CONTRACT assert on the recorded
// provider golden, NOT a BFF-passthrough drive: it pins the load-bearing
// wire-shape assumptions the sample-detail surface depends on, and (paired with
// the Go provider recorder) it fails on provider drift. When a verbatim
// passthrough BFF lands, the passthrough-drive half is added here.
//
// Load-bearing field assumptions (the envelope built by
// internal/api/audit/handlers.go ListAnnotations + annotationWireFrom):
//   * annotations is ALWAYS an array (never null); empty sample records []
//   * count is a number and equals annotations.length
//   * each row carries id / sample_id / evidence_record_id / result /
//     annotated_by (strings), annotated_at (ISO-8601 string), and notes
//     (NOT omitempty — "" when absent)
//   * result is one of passed | failed | not-applicable

import { readFileSync } from "node:fs";
import { join } from "node:path";

import { describe, expect, test } from "vitest";

interface AnnotationRow {
  id: string;
  sample_id: string;
  evidence_record_id: string;
  result: string;
  annotated_by: string;
  annotated_at: string;
  notes: string;
}

interface Golden {
  endpoint: string;
  variants: Record<string, { annotations: AnnotationRow[]; count: number }>;
}

const golden: Golden = JSON.parse(
  readFileSync(join(__dirname, "sample-annotations.golden.json"), "utf8"),
) as Golden;

const ALLOWED_RESULTS = new Set(["passed", "failed", "not-applicable"]);

describe("contract: atlas GET /v1/samples/{id}/annotations provider shape", () => {
  test("golden pins the documented endpoint", () => {
    expect(golden.endpoint).toBe("GET /v1/samples/{id}/annotations");
    expect(Object.keys(golden.variants).length).toBeGreaterThan(0);
  });

  test("every variant envelope is {annotations:[], count} with count === length", () => {
    for (const [name, body] of Object.entries(golden.variants)) {
      expect(
        Array.isArray(body.annotations),
        `${name}.annotations is array`,
      ).toBe(true);
      expect(typeof body.count, `${name}.count`).toBe("number");
      expect(body.count, `${name}.count === length`).toBe(
        body.annotations.length,
      );
    }
  });

  test("every annotation row carries the core string fields + notes (never omitted)", () => {
    for (const [name, body] of Object.entries(golden.variants)) {
      for (const [i, row] of body.annotations.entries()) {
        for (const field of [
          "id",
          "sample_id",
          "evidence_record_id",
          "result",
          "annotated_by",
          "annotated_at",
          "notes",
        ] as const) {
          expect(typeof row[field], `${name}[${i}].${field}`).toBe("string");
        }
        expect(
          ALLOWED_RESULTS.has(row.result),
          `${name}[${i}].result '${row.result}' is allowed`,
        ).toBe(true);
      }
    }
  });

  test("empty sample records [] and count 0", () => {
    const empty = golden.variants.empty;
    expect(empty, "empty variant present").toBeDefined();
    expect(empty.annotations).toEqual([]);
    expect(empty.count).toBe(0);
  });

  test("populated variant exercises a non-empty AND an empty notes string", () => {
    const populated = golden.variants.populated;
    expect(populated, "populated variant present").toBeDefined();
    const notes = populated.annotations.map((a) => a.notes);
    expect(
      notes.some((n) => n.length > 0),
      "at least one non-empty note",
    ).toBe(true);
    expect(
      notes.some((n) => n === ""),
      "at least one empty note",
    ).toBe(true);
  });
});
