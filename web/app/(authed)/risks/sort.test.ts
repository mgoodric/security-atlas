// Slice 681 / ATLAS-039 — unit tests for the risks-list column sort.
//
// The register grew past 20 rows with no way to order by the columns an
// operator triages on (residual, inherent severity, review-due). This
// module is the pure, vitest-covered seam: the page owns the click
// handler + URL state, but the comparator + the asc/desc toggle math
// live here so the ordering is unit-tested without a React tree (the
// filters.ts / count-label.ts per-page convention).
//
// Design (decisions log D1): CLIENT-SIDE sort, mirroring the existing
// filter + paginate-over-the-full-list pattern. `GET /v1/risks` ships
// the whole list; the page sorts the in-memory array. No wire change.
//
// Three sortable keys, each with a deterministic tie-break and a
// well-defined position for "pending evaluation" rows (a brand-new risk
// has no residual / review-due yet — slice 680). Pending rows sort to
// the END regardless of direction, so an operator triaging "worst first"
// (desc) or "soonest review" (asc) never has un-scored rows jump the
// queue.

import { describe, expect, it } from "vitest";

import type { Risk } from "@/lib/api/risks";
import {
  DEFAULT_SORT,
  nextSortState,
  parseSortState,
  serializeSortState,
  sortRisks,
  type SortDir,
  type SortKey,
  type SortState,
} from "./sort";

// Minimal Risk factory — only the fields the comparator reads. Neutral
// strings only (P0-A4 / GitGuardian).
function mkRisk(over: Partial<Risk>): Risk {
  return {
    id: "00000000-0000-0000-0000-000000000000",
    title: "sample risk",
    description: "",
    category: "operational",
    methodology: "qualitative_5x5",
    inherent_score: {},
    treatment: "mitigate",
    treatment_owner: "owner-a",
    residual_score: {},
    review_due_at: undefined,
    accepted_until: null,
    accepter: "",
    instrument_reference: "",
    linked_control_ids: [],
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    themes: [],
    severity: 0,
    ...over,
  };
}

// A residual JSONB blob with the canonical {likelihood, impact} shape.
function residual(
  l: number,
  i: number,
): { likelihood: number; impact: number } {
  return { likelihood: l, impact: i };
}

describe("parseSortState", () => {
  it("returns the default when the param is absent", () => {
    expect(parseSortState(null)).toEqual(DEFAULT_SORT);
  });

  it("returns the default for an unknown key", () => {
    expect(parseSortState("title:asc")).toEqual(DEFAULT_SORT);
    expect(parseSortState("garbage")).toEqual(DEFAULT_SORT);
  });

  it("parses a valid key:dir pair", () => {
    expect(parseSortState("severity:asc")).toEqual({
      key: "severity",
      dir: "asc",
    });
    expect(parseSortState("residual:desc")).toEqual({
      key: "residual",
      dir: "desc",
    });
    expect(parseSortState("review_due:asc")).toEqual({
      key: "review_due",
      dir: "asc",
    });
  });

  it("defaults an unknown direction to desc", () => {
    expect(parseSortState("severity:sideways")).toEqual({
      key: "severity",
      dir: "desc",
    });
  });
});

describe("serializeSortState round-trips through parseSortState", () => {
  it("survives a round-trip for every key+dir", () => {
    const keys: SortKey[] = ["residual", "severity", "review_due"];
    const dirs: SortDir[] = ["asc", "desc"];
    for (const key of keys) {
      for (const dir of dirs) {
        const s: SortState = { key, dir };
        expect(parseSortState(serializeSortState(s))).toEqual(s);
      }
    }
  });
});

describe("nextSortState — header-click toggle", () => {
  it("clicking a NEW column sorts it descending first (worst-first triage)", () => {
    const next = nextSortState({ key: "severity", dir: "asc" }, "residual");
    expect(next).toEqual({ key: "residual", dir: "desc" });
  });

  it("clicking the ACTIVE column toggles desc -> asc", () => {
    const next = nextSortState({ key: "severity", dir: "desc" }, "severity");
    expect(next).toEqual({ key: "severity", dir: "asc" });
  });

  it("clicking the ACTIVE column toggles asc -> desc", () => {
    const next = nextSortState({ key: "severity", dir: "asc" }, "severity");
    expect(next).toEqual({ key: "severity", dir: "desc" });
  });
});

describe("sortRisks — severity", () => {
  it("orders by severity descending (high first)", () => {
    const rows = [
      mkRisk({ id: "a", severity: 4 }),
      mkRisk({ id: "b", severity: 20 }),
      mkRisk({ id: "c", severity: 12 }),
    ];
    const out = sortRisks(rows, { key: "severity", dir: "desc" });
    expect(out.map((r) => r.id)).toEqual(["b", "c", "a"]);
  });

  it("orders by severity ascending (low first)", () => {
    const rows = [
      mkRisk({ id: "a", severity: 4 }),
      mkRisk({ id: "b", severity: 20 }),
      mkRisk({ id: "c", severity: 12 }),
    ];
    const out = sortRisks(rows, { key: "severity", dir: "asc" });
    expect(out.map((r) => r.id)).toEqual(["a", "c", "b"]);
  });

  it("does not mutate the input array", () => {
    const rows = [
      mkRisk({ id: "a", severity: 4 }),
      mkRisk({ id: "b", severity: 20 }),
    ];
    const snapshot = rows.map((r) => r.id);
    sortRisks(rows, { key: "severity", dir: "desc" });
    expect(rows.map((r) => r.id)).toEqual(snapshot);
  });
});

describe("sortRisks — residual", () => {
  it("orders scored rows by residual magnitude descending", () => {
    const rows = [
      mkRisk({ id: "lo", residual_score: residual(1, 2) }), // 2/25
      mkRisk({ id: "hi", residual_score: residual(5, 5) }), // 25/25
      mkRisk({ id: "mid", residual_score: residual(3, 3) }), // 9/25
    ];
    const out = sortRisks(rows, { key: "residual", dir: "desc" });
    expect(out.map((r) => r.id)).toEqual(["hi", "mid", "lo"]);
  });

  it("orders scored rows by residual magnitude ascending", () => {
    const rows = [
      mkRisk({ id: "lo", residual_score: residual(1, 2) }),
      mkRisk({ id: "hi", residual_score: residual(5, 5) }),
      mkRisk({ id: "mid", residual_score: residual(3, 3) }),
    ];
    const out = sortRisks(rows, { key: "residual", dir: "asc" });
    expect(out.map((r) => r.id)).toEqual(["lo", "mid", "hi"]);
  });

  it("sorts PENDING residual rows to the end (desc) — un-scored never jumps the queue", () => {
    const rows = [
      mkRisk({ id: "pending", residual_score: {} }),
      mkRisk({ id: "hi", residual_score: residual(5, 5) }),
      mkRisk({ id: "lo", residual_score: residual(1, 1) }),
    ];
    const out = sortRisks(rows, { key: "residual", dir: "desc" });
    expect(out.map((r) => r.id)).toEqual(["hi", "lo", "pending"]);
  });

  it("sorts PENDING residual rows to the end (asc) too", () => {
    const rows = [
      mkRisk({ id: "pending", residual_score: {} }),
      mkRisk({ id: "hi", residual_score: residual(5, 5) }),
      mkRisk({ id: "lo", residual_score: residual(1, 1) }),
    ];
    const out = sortRisks(rows, { key: "residual", dir: "asc" });
    expect(out.map((r) => r.id)).toEqual(["lo", "hi", "pending"]);
  });
});

describe("sortRisks — review_due", () => {
  it("orders by review-due date ascending (soonest first)", () => {
    const rows = [
      mkRisk({ id: "late", review_due_at: "2026-12-01" }),
      mkRisk({ id: "soon", review_due_at: "2026-06-15" }),
      mkRisk({ id: "mid", review_due_at: "2026-09-01" }),
    ];
    const out = sortRisks(rows, { key: "review_due", dir: "asc" });
    expect(out.map((r) => r.id)).toEqual(["soon", "mid", "late"]);
  });

  it("orders by review-due date descending (latest first)", () => {
    const rows = [
      mkRisk({ id: "late", review_due_at: "2026-12-01" }),
      mkRisk({ id: "soon", review_due_at: "2026-06-15" }),
    ];
    const out = sortRisks(rows, { key: "review_due", dir: "desc" });
    expect(out.map((r) => r.id)).toEqual(["late", "soon"]);
  });

  it("sorts PENDING (no review-due) rows to the end in both directions", () => {
    const rows = [
      mkRisk({ id: "pending", review_due_at: undefined }),
      mkRisk({ id: "soon", review_due_at: "2026-06-15" }),
      mkRisk({ id: "late", review_due_at: "2026-12-01" }),
    ];
    expect(
      sortRisks(rows, { key: "review_due", dir: "asc" }).map((r) => r.id),
    ).toEqual(["soon", "late", "pending"]);
    expect(
      sortRisks(rows, { key: "review_due", dir: "desc" }).map((r) => r.id),
    ).toEqual(["late", "soon", "pending"]);
  });
});

describe("sortRisks — stable tie-break", () => {
  it("breaks ties by id so the order is deterministic", () => {
    const rows = [
      mkRisk({ id: "ccc", severity: 10 }),
      mkRisk({ id: "aaa", severity: 10 }),
      mkRisk({ id: "bbb", severity: 10 }),
    ];
    const out = sortRisks(rows, { key: "severity", dir: "desc" });
    expect(out.map((r) => r.id)).toEqual(["aaa", "bbb", "ccc"]);
  });
});
