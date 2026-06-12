// Slice 704 — contract-test-tier rollout (consumer side: GET /v1/evidence with
// NO control_id, the tenant-wide evidence-ledger window — the slice-106 +
// slice-234 filter-matrix branch the /evidence list view reads).
//
// PROVIDER: internal/api/controldetail/evidence_contract_test.go records the
// real Evidence handler's tenant-wide bodies into
// evidence-tenant-window.golden.json (via the slice-704 EvidencePaged seam
// method). This CONSUMER half asserts the BFF (web/app/api/evidence/route.ts)
// against them. That BFF is a VERBATIM passthrough — it forwards the upstream
// body bytes + status unchanged — so the assert is toEqual(golden), like the
// slice-692 per-control branch.
//
// Load-bearing field assumptions (the tenant-wide evidence envelope built by
// internal/api/controldetail/handler.go Evidence + evidenceWireFromListRow):
//   * control_id is the EMPTY STRING on this branch (no control_id was sent)
//   * evidence is ALWAYS an array (never null); empty window records []
//   * count is a number and equals evidence.length (the page length)
//   * total is a number — the TENANT-WIDE ledger count (NOT the page length);
//     it lets the frontend render "Showing N of M records"
//   * next_cursor is a string ("" when there is no next page)
//   * each row carries evidence_id / observed_at / content_hash / result
//     (strings), evidence_kind (string-or-null), scope_cell (string-or-null),
//     and source (opaque JSON — object or null, never absent)

import { readFileSync } from "node:fs";
import { join } from "node:path";

import { beforeEach, describe, expect, test, vi } from "vitest";

import { mockNextServer } from "../test-utils/next-mocks";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () => Promise.resolve({ get: mockCookieGet }),
}));

import { GET } from "../../app/api/evidence/route";

interface EvidenceRow {
  evidence_id: string;
  evidence_kind: string | null;
  observed_at: string;
  source: unknown;
  content_hash: string;
  scope_cell: string | null;
  result: string;
}

interface EvidenceEnvelope {
  control_id: string;
  evidence: EvidenceRow[];
  count: number;
  total: number;
  next_cursor: string;
}

interface Golden {
  endpoint: string;
  variants: Record<string, EvidenceEnvelope>;
}

const golden: Golden = JSON.parse(
  readFileSync(join(__dirname, "evidence-tenant-window.golden.json"), "utf8"),
) as Golden;

// Tenant-wide target: NO control_id. The `filtered` provider variant was
// recorded behind a non-empty filter matrix upstream; the BFF only whitelists
// + forwards those params, so the consumer assert drives the no-control_id URL
// and trusts the recorded provider body.
const TARGET = "http://localhost/api/evidence";
const ALLOWED_RESULTS = new Set(["pass", "fail", "na", "inconclusive"]);

describe("contract: GET /api/evidence (tenant-wide) <-> atlas GET /v1/evidence", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    mockCookieGet.mockReset();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("golden pins the documented tenant-wide endpoint", () => {
    expect(golden.endpoint).toBe("GET /v1/evidence");
    expect(Object.keys(golden.variants).length).toBeGreaterThan(0);
  });

  test("control_id is the empty string on the tenant-wide branch", () => {
    for (const [name, body] of Object.entries(golden.variants)) {
      expect(body.control_id, `${name}.control_id is empty string`).toBe("");
    }
  });

  test("every variant envelope is {control_id, evidence:[], count, total, next_cursor}", () => {
    for (const [name, body] of Object.entries(golden.variants)) {
      expect(typeof body.control_id, `${name}.control_id`).toBe("string");
      expect(Array.isArray(body.evidence), `${name}.evidence is array`).toBe(
        true,
      );
      expect(typeof body.count, `${name}.count`).toBe("number");
      expect(body.count, `${name}.count === evidence.length`).toBe(
        body.evidence.length,
      );
      expect(typeof body.total, `${name}.total`).toBe("number");
      expect(typeof body.next_cursor, `${name}.next_cursor`).toBe("string");
    }
  });

  test("total is the tenant-wide ledger count, decoupled from the page length", () => {
    // The populated variant pins total !== count so a future refactor that
    // accidentally returns the page length as `total` is caught.
    const populated = golden.variants.populated;
    expect(populated, "populated variant present").toBeDefined();
    expect(populated.total).toBeGreaterThan(populated.count);
    const empty = golden.variants.empty;
    expect(empty, "empty variant present").toBeDefined();
    // Empty window but non-empty ledger: count 0, total > 0 (slice 236).
    expect(empty.count).toBe(0);
    expect(empty.total).toBeGreaterThan(0);
  });

  test("filtered variant pins the filter-matrix request surface", () => {
    // The provider recorded this variant behind a non-empty filter predicate
    // (kind/result/scope_cell_id/window). The wire shape is identical to an
    // unfiltered window — filters narrow rows, not the envelope. Pinning it
    // catches a future change that lets a filter leak into the wire shape.
    const filtered = golden.variants.filtered;
    expect(filtered, "filtered variant present").toBeDefined();
    expect(filtered.evidence.length).toBeGreaterThan(0);
    expect(filtered.control_id).toBe("");
  });

  test("every evidence row carries the nullable-aware wire shape", () => {
    for (const [name, body] of Object.entries(golden.variants)) {
      for (const [i, row] of body.evidence.entries()) {
        for (const field of [
          "evidence_id",
          "observed_at",
          "content_hash",
          "result",
        ] as const) {
          expect(typeof row[field], `${name}[${i}].${field}`).toBe("string");
        }
        expect(
          ALLOWED_RESULTS.has(row.result),
          `${name}[${i}].result '${row.result}' is allowed`,
        ).toBe(true);
        // evidence_kind is string-or-null (never absent).
        expect("evidence_kind" in row, `${name}[${i}].evidence_kind`).toBe(
          true,
        );
        if (row.evidence_kind !== null) {
          expect(typeof row.evidence_kind, `${name}[${i}].evidence_kind`).toBe(
            "string",
          );
        }
        // scope_cell is string-or-null (never absent).
        expect("scope_cell" in row, `${name}[${i}].scope_cell`).toBe(true);
        if (row.scope_cell !== null) {
          expect(typeof row.scope_cell, `${name}[${i}].scope_cell`).toBe(
            "string",
          );
        }
        // source is opaque JSON — object or null, never absent.
        expect("source" in row, `${name}[${i}].source`).toBe(true);
      }
    }
  });

  test("populated variant exercises a fully-populated AND a fully-nulled row", () => {
    const populated = golden.variants.populated;
    expect(populated, "populated variant present").toBeDefined();
    const kinds = populated.evidence.map((e) => e.evidence_kind);
    expect(
      kinds.some((k) => k !== null),
      "at least one present evidence_kind",
    ).toBe(true);
    expect(
      kinds.some((k) => k === null),
      "at least one null evidence_kind",
    ).toBe(true);
    const scopes = populated.evidence.map((e) => e.scope_cell);
    expect(
      scopes.some((s) => s === null),
      "at least one null scope_cell",
    ).toBe(true);
    const sources = populated.evidence.map((e) => e.source);
    expect(
      sources.some((s) => s === null),
      "at least one null source",
    ).toBe(true);
  });

  for (const variantName of Object.keys(golden.variants)) {
    test(`BFF passes provider variant "${variantName}" through verbatim`, async () => {
      mockCookieGet.mockReturnValue({ value: "test-bearer-704" });
      const providerBody = golden.variants[variantName];
      vi.spyOn(globalThis, "fetch").mockImplementation(
        async () =>
          new Response(JSON.stringify(providerBody), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
      );

      const res = await GET(new Request(TARGET));
      expect(res.status).toBe(200);
      const got = (await res.json()) as EvidenceEnvelope;
      expect(got).toEqual(providerBody);
    });
  }

  test("returns 401 when the session cookie is absent (guard before upstream)", async () => {
    mockCookieGet.mockReturnValue(undefined);
    const res = await GET(new Request(TARGET));
    expect(res.status).toBe(401);
  });
});
