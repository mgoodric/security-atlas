// Slice 690 — contract-test-tier rollout (consumer side: GET /v1/audit-notes,
// the legacy slice-025 author-scoped note list within a period).
//
// PROVIDER: internal/api/auditnotes/contractrecord_test.go records the real
// List handler's bodies into audit-notes-list.golden.json.
//
// CONSUMER SHAPE (honest scoping — slice 687 D3): there is NO GET BFF for the
// legacy list today — the audit workspace reads GET /v1/audit-notes/thread, not
// this legacy author-scoped list. So this CONSUMER half is a FIELD-CONTRACT
// assert on the recorded provider golden, NOT a BFF-passthrough drive: it pins
// the load-bearing wire-shape assumptions a future legacy-list BFF (or the
// shared noteWire consumers) depend on, and (paired with the Go provider
// recorder) it fails on provider drift.
//
// Load-bearing field assumptions (the envelope built by
// internal/api/auditnotes/handlers.go List + noteWireFrom):
//   * audit_notes is ALWAYS an array (never null); empty records []
//   * count is a number and equals audit_notes.length
//   * each row carries id / audit_period_id / author_user_id / scope_type /
//     body / visibility (strings) and created_at / updated_at (millisecond
//     ISO-8601 strings, 2006-01-02T15:04:05.000Z)
//   * scope_id is omitempty — PRESENT on a scoped note, ABSENT on a
//     period-level note
//   * parent_note_id + depth are omitempty (the legacy list is a flat
//     author-scoped read; depth is 0 → absent)

import { readFileSync } from "node:fs";
import { join } from "node:path";

import { describe, expect, test } from "vitest";

interface NoteRow {
  id: string;
  audit_period_id: string;
  author_user_id: string;
  scope_type: string;
  body: string;
  visibility: string;
  created_at: string;
  updated_at: string;
  scope_id?: string;
  parent_note_id?: string;
  depth?: number;
}

interface Golden {
  endpoint: string;
  variants: Record<string, { audit_notes: NoteRow[]; count: number }>;
}

const golden: Golden = JSON.parse(
  readFileSync(join(__dirname, "audit-notes-list.golden.json"), "utf8"),
) as Golden;

// Millisecond ISO-8601 the noteWireFrom emits (2006-01-02T15:04:05.000Z).
const MILLIS_ISO = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$/;

describe("contract: atlas GET /v1/audit-notes provider shape", () => {
  test("golden pins the documented endpoint", () => {
    expect(golden.endpoint).toBe("GET /v1/audit-notes");
    expect(Object.keys(golden.variants).length).toBeGreaterThan(0);
  });

  test("every variant envelope is {audit_notes:[], count} with count === length", () => {
    for (const [name, body] of Object.entries(golden.variants)) {
      expect(
        Array.isArray(body.audit_notes),
        `${name}.audit_notes is array`,
      ).toBe(true);
      expect(typeof body.count, `${name}.count`).toBe("number");
      expect(body.count, `${name}.count === length`).toBe(
        body.audit_notes.length,
      );
    }
  });

  test("every note row carries the core string fields + millisecond timestamps", () => {
    for (const [name, body] of Object.entries(golden.variants)) {
      for (const [i, row] of body.audit_notes.entries()) {
        for (const field of [
          "id",
          "audit_period_id",
          "author_user_id",
          "scope_type",
          "body",
          "visibility",
        ] as const) {
          expect(typeof row[field], `${name}[${i}].${field}`).toBe("string");
        }
        expect(row.created_at, `${name}[${i}].created_at millis`).toMatch(
          MILLIS_ISO,
        );
        expect(row.updated_at, `${name}[${i}].updated_at millis`).toMatch(
          MILLIS_ISO,
        );
      }
    }
  });

  test("empty list records [] and count 0", () => {
    const empty = golden.variants.empty;
    expect(empty, "empty variant present").toBeDefined();
    expect(empty.audit_notes).toEqual([]);
    expect(empty.count).toBe(0);
  });

  test("scope_id is present on a scoped note, absent on a period-level note (omitempty)", () => {
    const populated = golden.variants.populated;
    expect(populated, "populated variant present").toBeDefined();
    const scoped = populated.audit_notes.find((n) => "scope_id" in n);
    const periodLevel = populated.audit_notes.find((n) => !("scope_id" in n));
    expect(scoped, "a scoped note exists").toBeDefined();
    expect(typeof scoped?.scope_id, "scoped.scope_id").toBe("string");
    expect(periodLevel, "a period-level note exists").toBeDefined();
    expect(periodLevel?.scope_type, "period-level note has scope_type").toBe(
      "period",
    );
  });

  test("legacy list rows omit parent_note_id + depth (flat author-scoped read)", () => {
    const populated = golden.variants.populated;
    for (const [i, row] of populated.audit_notes.entries()) {
      expect(
        "parent_note_id" in row,
        `populated[${i}].parent_note_id absent`,
      ).toBe(false);
      expect("depth" in row, `populated[${i}].depth absent`).toBe(false);
    }
  });
});
