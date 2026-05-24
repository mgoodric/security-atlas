// Slice 099 — unit tests for the /evidence cell formatters.

import { describe, expect, test } from "vitest";

import {
  HASH_PREFIX_LEN,
  hashPrefix,
  ledgerSubtitleSuffix,
  observedAtLabel,
  prettyJSON,
  recordCountMeta,
  scopeLabel,
  sourceSummary,
} from "./format";

describe("hashPrefix", () => {
  test("renders exactly HASH_PREFIX_LEN characters", () => {
    const full =
      "7a4f0123456789abcdef0123456789abcdef0123456789abcdef0123456789b2c1";
    const prefix = hashPrefix(full);
    expect(prefix.length).toBe(HASH_PREFIX_LEN);
    expect(prefix).toBe("7a4f0123");
  });

  test("HASH_PREFIX_LEN is 8 (P0-A2)", () => {
    // Anti-criterion P0-A2 of slice 099 pins the prefix length to 8.
    // If the constant ever drifts, this assertion catches it.
    expect(HASH_PREFIX_LEN).toBe(8);
  });

  test("renders empty string for empty input", () => {
    expect(hashPrefix("")).toBe("");
  });
});

describe("scopeLabel", () => {
  test("renders a shortened UUID with ellipsis when present", () => {
    const out = scopeLabel("22222222-3333-4444-5555-666666666666");
    expect(out).toBe("22222222…");
  });

  test("renders an em-dash when scope_cell is null", () => {
    expect(scopeLabel(null)).toBe("—");
  });
});

describe("sourceSummary", () => {
  test("renders actor_type and actor_id when both present", () => {
    const out = sourceSummary({
      actor_type: "connector",
      actor_id: "aws-connector",
    });
    expect(out).toBe("connector · aws-connector");
  });

  test("falls back to actor_type alone when actor_id missing", () => {
    expect(sourceSummary({ actor_type: "connector" })).toBe("connector");
  });

  test("falls back to actor_id alone when actor_type missing", () => {
    expect(sourceSummary({ actor_id: "manual:sam-r" })).toBe("manual:sam-r");
  });

  test("renders an em-dash for null source", () => {
    expect(sourceSummary(null)).toBe("—");
  });

  test("renders an em-dash for shape without actor fields", () => {
    expect(sourceSummary({ unrelated: 42 })).toBe("—");
  });

  test("ignores non-string actor values (defensive)", () => {
    expect(
      sourceSummary({
        actor_type: 12 as unknown as string,
        actor_id: { nested: true } as unknown as string,
      }),
    ).toBe("—");
  });
});

describe("prettyJSON", () => {
  test("renders an indented JSON string", () => {
    const out = prettyJSON({ a: 1, b: [2, 3] });
    expect(out).toContain("\n");
    expect(out).toContain('"a": 1');
    expect(out).toContain('"b": [');
  });
});

describe("observedAtLabel", () => {
  test("passes the timestamp through verbatim today", () => {
    expect(observedAtLabel("2026-05-16T09:42:00Z")).toBe(
      "2026-05-16T09:42:00Z",
    );
  });
});

// ---- Slice 236 — record-count meta + ledger subtitle suffix ----
//
// The /evidence page surfaces a tenant-wide ledger total alongside the
// filtered-window row count so the operator can distinguish "filters
// narrowed the view" from "the ledger is empty". The three branches
// the formatter has to cover are the same three operator-confusion
// modes documented in `docs/issues/236-…`.

describe("recordCountMeta", () => {
  test("renders empty-ledger sentinel when total is zero", () => {
    // Bug class the slice closes: a fresh tenant lands on /evidence,
    // sees "Showing 0 records", and cannot tell whether the ledger is
    // empty tenant-wide or their filters narrowed to zero.
    expect(recordCountMeta(0, 0)).toBe("No records in ledger yet");
  });

  test("renders empty-ledger sentinel even if shown is non-zero (defensive)", () => {
    // The wire shape can never report shown > 0 when total === 0
    // (ledger total >= filtered count by construction), but the
    // formatter guards against the contradictory input by falling
    // through to the empty-ledger branch.
    expect(recordCountMeta(3, 0)).toBe("No records in ledger yet");
  });

  test("renders 'Showing N of M records' when filters narrow the view", () => {
    expect(recordCountMeta(12, 14712)).toBe("Showing 12 of 14712 records");
  });

  test("renders 'Showing 0 of M records' when filters narrow to zero on a non-empty ledger", () => {
    // Distinct from the empty-ledger case: the operator can read this
    // as "your filters excluded every record — try widening them".
    expect(recordCountMeta(0, 47)).toBe("Showing 0 of 47 records");
  });

  test("renders 'Showing N of N records' when the filter matches the entire ledger", () => {
    // No-filter / wide-window case on a small ledger: N === M.
    expect(recordCountMeta(5, 5)).toBe("Showing 5 of 5 records");
  });

  test("clamps negative inputs to zero defensively", () => {
    // The wire shape never returns negative counts, but a downstream
    // bug should not surface a misleading "Showing -1 of -2 records".
    expect(recordCountMeta(-1, -2)).toBe("No records in ledger yet");
  });
});

describe("ledgerSubtitleSuffix", () => {
  test("renders 'append-only · M records' when the ledger has rows", () => {
    expect(ledgerSubtitleSuffix(14712)).toBe("append-only · 14712 records");
  });

  test("renders empty string when the ledger is empty", () => {
    // The empty-ledger signal is carried by the meta row's
    // "No records in ledger yet"; the subtitle stays clean.
    expect(ledgerSubtitleSuffix(0)).toBe("");
  });

  test("clamps negative inputs to zero (renders empty string)", () => {
    expect(ledgerSubtitleSuffix(-3)).toBe("");
  });

  test("renders 'append-only · 1 records' for a single-row ledger", () => {
    // Pluralisation is intentionally NOT done here — "1 records" is
    // mildly awkward but the slice 236 spec defaults to the mockup
    // string shape, and pluralisation would diverge from the design
    // doc. A future slice can add it consistently across the page.
    expect(ledgerSubtitleSuffix(1)).toBe("append-only · 1 records");
  });
});
