// #2083 — every backend enum value has a display label.
//
// The backend's enum values are read from the backend's own source, not
// from a list copied into this test:
//
//   * internal/db/dbx/models.go — sqlc generates one Go string type per
//     Postgres enum, with one constant per value, from the migrations. A
//     migration that adds an enum value regenerates this file, and this test
//     then fails until ENUM_LABELS has a label for it.
//   * internal/featureflag/seed.go — the feature-flag catalog's `Category`
//     strings, which Admin › Features groups by.
//
// CI runs this test whenever either file changes: migrations/** and
// sqlc.yaml are in the workflow's shared tier, and so are the two files.

import { readFileSync } from "node:fs";
import { join } from "node:path";

import { describe, expect, it } from "vitest";

import { AUDIT_LOG_KINDS } from "../api/audit-log";
import {
  ENUM_LABELS,
  enumLabel,
  humanizeEnum,
  lookupLabel,
} from "./enum-labels";

const REPO_ROOT = join(__dirname, "..", "..", "..");
const MODELS_GO = join(REPO_ROOT, "internal", "db", "dbx", "models.go");
const FLAG_SEED_GO = join(REPO_ROOT, "internal", "featureflag", "seed.go");
const CHECKLIST_GO = join(REPO_ROOT, "internal", "checklist", "checklist.go");
// #2328: CHECK-constrained text columns, recorded from the migrated
// database by internal/db/check_enums_integration_test.go.
const CHECK_ENUMS_GOLDEN = join(
  __dirname,
  "..",
  "contracts",
  "check-enums.golden.json",
);

// parseSqlcEnums reads sqlc's enum declarations:
//
//   type RiskMethodology string
//   const (
//       RiskMethodologyNist80030 RiskMethodology = "nist_800_30"
//       ...
//   )
function parseSqlcEnums(src: string): Map<string, string[]> {
  const enums = new Map<string, string[]>();
  for (const m of src.matchAll(/^type (\w+) string$/gm)) {
    enums.set(m[1], []);
  }
  for (const m of src.matchAll(/^\s+\w+\s+(\w+)\s*=\s*"([^"]*)"\s*$/gm)) {
    enums.get(m[1])?.push(m[2]);
  }
  return enums;
}

function parseFlagCategories(src: string): string[] {
  return [
    ...new Set([...src.matchAll(/Category:\s*"([^"]+)"/g)].map((m) => m[1])),
  ];
}

const labels: Record<string, Record<string, string>> = ENUM_LABELS;

describe("backend enum label coverage (#2083)", () => {
  const sqlcEnums = parseSqlcEnums(readFileSync(MODELS_GO, "utf8"));

  it("reads a plausible set of enums from models.go", () => {
    // Guards the parser: if sqlc's output shape changes and the regexes
    // stop matching, this fails instead of the coverage check passing on
    // an empty set.
    expect(sqlcEnums.size).toBeGreaterThanOrEqual(20);
    expect(sqlcEnums.get("RiskMethodology")).toContain("qualitative_5x5");
    expect(sqlcEnums.get("StrmRelationshipType")).toContain("intersects_with");
    for (const [name, values] of sqlcEnums) {
      expect(values.length, `${name} has no parsed values`).toBeGreaterThan(0);
    }
  });

  it("labels every value of every Postgres enum", () => {
    const missing: string[] = [];
    for (const [name, values] of sqlcEnums) {
      for (const v of values) {
        if (!labels[name]?.[v]) missing.push(`${name}.${v}`);
      }
    }
    expect(missing, "add these to ENUM_LABELS in enum-labels.ts").toEqual([]);
  });

  it("labels no value the backend does not have", () => {
    const stale: string[] = [];
    for (const [name, values] of sqlcEnums) {
      for (const v of Object.keys(labels[name] ?? {})) {
        if (!values.includes(v)) stale.push(`${name}.${v}`);
      }
    }
    expect(stale).toEqual([]);
  });

  it("labels every feature-flag category", () => {
    const categories = parseFlagCategories(readFileSync(FLAG_SEED_GO, "utf8"));
    expect(categories.length).toBeGreaterThan(0);
    const missing = categories.filter((c) => !labels.FeatureCategory[c]);
    expect(missing, "add these to ENUM_LABELS.FeatureCategory").toEqual([]);
  });

  it("labels every Activity record kind", () => {
    const missing = AUDIT_LOG_KINDS.filter((k) => !labels.ActivityKind[k]);
    expect(missing).toEqual([]);
  });

  it("labels every value of every CHECK-constrained status (#2328)", () => {
    const golden = JSON.parse(readFileSync(CHECK_ENUMS_GOLDEN, "utf8")) as {
      enums: Record<string, string[]>;
    };
    const names = Object.keys(golden.enums);
    expect(names).toEqual(
      expect.arrayContaining(["ActionPlanStatus", "AuditPeriodStatus"]),
    );
    const missing: string[] = [];
    for (const name of names) {
      for (const v of golden.enums[name]) {
        if (!labels[name] || !Object.hasOwn(labels[name], v)) {
          missing.push(`${name}.${v}`);
        }
      }
    }
    expect(missing, "add these to ENUM_LABELS in enum-labels.ts").toEqual([]);
  });

  it("labels every checklist citation kind (#2328)", () => {
    const src = readFileSync(CHECKLIST_GO, "utf8");
    const kinds = [
      ...src.matchAll(/^\s+\w+\s+CitationKind\s*=\s*"([^"]+)"/gm),
    ].map((m) => m[1]);
    expect(kinds).toContain("scf_anchor");
    const missing = kinds.filter((k) => !labels.ChecklistItemKind[k]);
    expect(missing).toEqual([]);
  });

  it("fails for an unlabelled value", () => {
    // The check above, run against a models.go that gained a value.
    const src = `type RiskMethodology string

const (
	RiskMethodologyFair RiskMethodology = "fair"
	RiskMethodologyOctave RiskMethodology = "octave"
)
`;
    const parsed = parseSqlcEnums(src);
    const unlabelled = parsed
      .get("RiskMethodology")!
      .filter((v) => !labels.RiskMethodology[v]);
    expect(unlabelled).toEqual(["octave"]);
  });
});

describe("enumLabel", () => {
  it("returns the label", () => {
    expect(enumLabel("RiskMethodology", "qualitative_5x5")).toBe(
      "Qualitative 5×5",
    );
    expect(enumLabel("StrmRelationshipType", "intersects_with")).toBe(
      "Intersects with",
    );
    expect(enumLabel("ControlImplementationType", "manual_attested")).toBe(
      "Manually attested",
    );
    expect(enumLabel("RiskTreatment", "mitigate")).toBe("Mitigate");
  });

  it("humanizes an unknown value rather than showing it raw", () => {
    expect(enumLabel("RiskMethodology", "octave_allegro")).toBe(
      "Octave allegro",
    );
  });

  it("does not resolve a value to an Object.prototype member", () => {
    expect(enumLabel("ActivityKind", "constructor")).toBe("Constructor");
    expect(enumLabel("EvidenceResult", "toString")).toBe("ToString");
    expect(lookupLabel("ActionPlanStatus", "hasOwnProperty")).toBeUndefined();
  });

  it("renders empty as an em dash", () => {
    expect(enumLabel("RiskTreatment", "")).toBe("—");
    expect(enumLabel("RiskTreatment", null)).toBe("—");
  });

  it("humanizeEnum keeps an empty-after-trim value as given", () => {
    expect(humanizeEnum("__")).toBe("__");
  });
});
