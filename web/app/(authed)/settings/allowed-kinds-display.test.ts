// Slice 166 — regression coverage for the production DoS bug surfaced by
// slice 165 iter 2.
//
// Pre-fix bug: the credentials table dereferenced `c.allowed_kinds.length`
// on the rendered row. When the backend returned `allowed_kinds: null`
// (the natural state of any newly-issued admin credential — pgx-go decodes
// the Postgres empty-text-array to a Go nil slice; Go's encoding/json then
// marshals nil to JSON null), the render threw `TypeError: Cannot read
// properties of null (reading 'length')`, React unmounted the page subtree,
// and Next.js showed its default error boundary.
//
// These tests lock the helper that replaced the deref. As long as the
// helper covers null + undefined + [] + non-empty, the render site is
// crash-proof.

import { describe, expect, test } from "vitest";

import {
  ALLOWED_KINDS_ANY,
  isAnyKind,
  kindsLabel,
} from "./allowed-kinds-display";

describe("kindsLabel", () => {
  test("null returns the ANY sentinel (the production crash case)", () => {
    // This is the bug-shaped artifact: pgx-go empty array -> Go nil ->
    // JSON null. The helper must NOT throw on null even though the
    // TypeScript type at the seam is `string[]`.
    expect(kindsLabel(null)).toBe(ALLOWED_KINDS_ANY);
  });

  test("undefined returns the ANY sentinel", () => {
    // Defensive: if the row arrives partial (e.g., a future endpoint that
    // doesn't include the field), we still don't throw.
    expect(kindsLabel(undefined)).toBe(ALLOWED_KINDS_ANY);
  });

  test("empty array returns the ANY sentinel", () => {
    // Semantically equivalent to null at the wire shape: both mean "any
    // kind." A backend that fixes the marshalling step will start sending
    // [] here; the UI must behave identically.
    expect(kindsLabel([])).toBe(ALLOWED_KINDS_ANY);
  });

  test("single kind joins to itself", () => {
    expect(kindsLabel(["evidence.kind.v1"])).toBe("evidence.kind.v1");
  });

  test("multiple kinds join with comma+space", () => {
    expect(kindsLabel(["evidence.kind.v1", "audit.event.v1"])).toBe(
      "evidence.kind.v1, audit.event.v1",
    );
  });

  test("preserves kind order from the input", () => {
    // The backend caller is the source of truth for ordering; the helper
    // does not sort.
    expect(kindsLabel(["b", "a", "c"])).toBe("b, a, c");
  });
});

describe("isAnyKind", () => {
  test("null is treated as any-kind (the production crash case)", () => {
    expect(isAnyKind(null)).toBe(true);
  });

  test("undefined is treated as any-kind", () => {
    expect(isAnyKind(undefined)).toBe(true);
  });

  test("empty array is treated as any-kind", () => {
    expect(isAnyKind([])).toBe(true);
  });

  test("non-empty array is NOT any-kind", () => {
    expect(isAnyKind(["evidence.kind.v1"])).toBe(false);
  });
});

describe("ALLOWED_KINDS_ANY", () => {
  test('sentinel value is the literal string "any"', () => {
    // Locks the label that the credentials table renders into a muted
    // span; changing this value is a visible UX change and must be
    // intentional.
    expect(ALLOWED_KINDS_ANY).toBe("any");
  });
});
