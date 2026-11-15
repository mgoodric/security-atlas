// #1785 — the control import review page's pure helpers.

import { describe, expect, it } from "vitest";

import {
  type ApproveClearResult,
  type BatchRow,
  type Candidate,
  type ImportBatch,
  approveClearMessage,
  batchProgress,
  effectiveImplementationType,
  fitLabel,
  initialApplicability,
  parseApplicability,
  rowPath,
  rowsQuery,
  sourceLabel,
  topCandidates,
} from "./control-imports";

function cand(over: Partial<Candidate>): Candidate {
  return {
    rank: 1,
    scf_anchor_id: "a",
    scf_id: "IAC-06",
    title: "MFA",
    source: "crosswalk",
    reaches: [],
    tested_count: 0,
    ai_assisted: false,
    ...over,
  };
}

function row(over: Partial<BatchRow>): BatchRow {
  return {
    id: "r",
    row_number: 2,
    external_id: "ORG-1",
    title: "t",
    description: "",
    tested_under: [],
    status: "pending",
    candidates: [],
    no_candidate_reaches: false,
    llm_rerank: "off",
    ...over,
  };
}

describe("fitLabel", () => {
  it("names the criteria the candidate reaches", () => {
    expect(
      fitLabel(
        cand({
          tested_count: 3,
          reaches: [
            { code: "CC6.1", requirement_id: "1" },
            { code: "CC6.2", requirement_id: "2" },
          ],
        }),
      ),
    ).toBe("reaches 2 of 3: CC6.1, CC6.2");
  });
  it("reads 0 of M when none is reached", () => {
    expect(fitLabel(cand({ tested_count: 2, reaches: null }))).toBe(
      "reaches 0 of 2",
    );
  });
  it("is empty for a row with no tested criteria", () => {
    expect(fitLabel(cand({ tested_count: 0 }))).toBe("");
  });
});

describe("sourceLabel", () => {
  it.each([
    [cand({ source: "crosswalk" }), "Crosswalk"],
    [cand({ source: "keyword" }), "Keyword"],
    [cand({ source: "llm" }), "LLM-reranked"],
    [cand({ source: "crosswalk", ai_assisted: true }), "LLM-reranked"],
  ])("%#", (c, want) => {
    expect(sourceLabel(c)).toBe(want);
  });
});

describe("topCandidates", () => {
  it("orders by rank and caps the list", () => {
    const r = row({
      candidates: [
        cand({ rank: 3, scf_id: "C" }),
        cand({ rank: 1, scf_id: "A" }),
        cand({ rank: 4, scf_id: "D" }),
        cand({ rank: 2, scf_id: "B" }),
      ],
    });
    expect(topCandidates(r).map((c) => c.scf_id)).toEqual(["A", "B", "C"]);
  });
  it("handles a null candidate list", () => {
    expect(topCandidates(row({ candidates: null }))).toEqual([]);
  });
});

describe("batchProgress", () => {
  it("tallies staged rows by status and keeps upload refusals apart", () => {
    const batch = {
      staged_count: 5,
      rejected_count: 2,
      staged: [
        row({ status: "approved" }),
        row({ status: "approved" }),
        row({ status: "rejected" }),
        row({ status: "duplicate" }),
        row({ status: "pending" }),
      ],
    } as ImportBatch;
    expect(batchProgress(batch)).toEqual({
      staged: 5,
      approved: 2,
      rejected: 1,
      duplicate: 1,
      pending: 1,
      notStaged: 2,
    });
  });
});

describe("rowsQuery", () => {
  it("is empty with no filter", () => {
    expect(rowsQuery({ status: "", noCandidateReaches: false })).toBe("");
  });
  it("sets both filters", () => {
    expect(rowsQuery({ status: "pending", noCandidateReaches: true })).toBe(
      "?status=pending&no_candidate_reaches=true",
    );
  });
});

describe("approveClearMessage", () => {
  const base: ApproveClearResult = {
    batch_id: "b",
    qualifying: 3,
    approved_count: 2,
    approved: [],
    duplicates: [{}],
    skipped: null,
  };
  it("reports approved, duplicates and skips", () => {
    expect(approveClearMessage(base)).toBe(
      "Approved 2 of 3 clear rows; 1 marked duplicate",
    );
    expect(
      approveClearMessage({
        ...base,
        qualifying: 1,
        approved_count: 0,
        duplicates: null,
        skipped: [{ row_number: 2, external_id: "X", reason: "decided" }],
      }),
    ).toBe("Approved 0 of 1 clear row; 1 skipped");
  });
});

describe("effectiveImplementationType", () => {
  it("prefers the sheet, else manual attestation", () => {
    expect(
      effectiveImplementationType(row({ implementation_type: "automated" })),
    ).toBe("automated");
    expect(effectiveImplementationType(row({}))).toBe("manual_attested");
  });
});

describe("parseApplicability", () => {
  it("parses stored JSON and ignores blanks and junk", () => {
    expect(parseApplicability('{"op":"true"}')).toEqual({ op: "true" });
    expect(parseApplicability("")).toBeUndefined();
    expect(parseApplicability("prod only")).toBeUndefined();
  });
});

describe("rowPath", () => {
  it("encodes both ids", () => {
    expect(rowPath("b 1", "r/2", "approve")).toBe(
      "/api/controls/imports/b%201/rows/r%2F2/approve",
    );
  });
});

describe("initialApplicability", () => {
  it("is Everything for a missing or unparseable sheet value", () => {
    expect(initialApplicability(undefined)).toEqual({ op: "true" });
    expect(initialApplicability("")).toEqual({ op: "true" });
    expect(initialApplicability("prod only")).toEqual({ op: "true" });
  });
  it("is the sheet's predicate when the picker can show it", () => {
    expect(initialApplicability('{"op":"true"}')).toEqual({ op: "true" });
    expect(
      initialApplicability('{"op":"eq","dim":"environment","value":"prod"}'),
    ).toEqual({ op: "eq", dim: "environment", value: "prod" });
  });
  it("is undefined for a predicate the picker shows as advanced", () => {
    expect(
      initialApplicability(
        '{"op":"or","args":[{"op":"eq","dim":"environment","value":"prod"},{"op":"eq","dim":"geography","value":"us"}]}',
      ),
    ).toBeUndefined();
  });
});
