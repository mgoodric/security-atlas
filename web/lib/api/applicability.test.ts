import { describe, expect, it } from "vitest";

import {
  applicabilityQuery,
  buildSoAExportURL,
  defaultRevision,
  describeReferenceChange,
  describeSide,
  isFrozen,
  soaTitle,
  summarizeChanges,
  type ApplicabilityChange,
} from "./applicability";
import type { FrameworkScope } from "./framework-scopes";

function scope(
  id: string,
  state: FrameworkScope["state"],
  created_at: string,
): FrameworkScope {
  return {
    id,
    framework_version_id: "fv",
    name: id,
    state,
    predicate: {},
    predicate_hash: "",
    created_at,
    updated_at: created_at,
  };
}

describe("applicabilityQuery", () => {
  it("maps each filter onto the platform's query string", () => {
    expect(applicabilityQuery("all")).toBe("");
    expect(applicabilityQuery("applicable")).toBe("?decision=applicable");
    expect(applicabilityQuery("excluded")).toBe("?decision=excluded");
    expect(applicabilityQuery("undecided")).toBe("?decision=undecided");
    expect(applicabilityQuery("contradiction")).toBe("?contradiction=true");
  });
});

describe("buildSoAExportURL", () => {
  it("carries the version, format and pinned revision", () => {
    expect(buildSoAExportURL("fv-1", "xlsx", "rev-1")).toBe(
      "/api/framework-scopes/applicability/export?framework_version=fv-1&format=xlsx&revision=rev-1",
    );
  });
  it("omits the revision when none is pinned", () => {
    expect(buildSoAExportURL("fv-1", "csv")).toBe(
      "/api/framework-scopes/applicability/export?framework_version=fv-1&format=csv",
    );
  });
});

describe("soaTitle", () => {
  it("titles ISO/IEC 27001 Statement of Applicability", () => {
    expect(soaTitle("iso27001")).toBe("Statement of Applicability");
  });
  it("titles every other framework Applicability", () => {
    expect(soaTitle("soc2")).toBe("Applicability");
    expect(soaTitle(undefined)).toBe("Applicability");
  });
});

describe("defaultRevision", () => {
  it("prefers the newest revision still being edited", () => {
    const got = defaultRevision([
      scope("active", "activated", "2026-01-01T00:00:00Z"),
      scope("old-draft", "draft", "2026-02-01T00:00:00Z"),
      scope("new-review", "review", "2026-03-01T00:00:00Z"),
    ]);
    expect(got?.id).toBe("new-review");
  });
  it("falls back to the activated revision", () => {
    const got = defaultRevision([
      scope("gone", "superseded", "2026-01-01T00:00:00Z"),
      scope("active", "activated", "2026-02-01T00:00:00Z"),
    ]);
    expect(got?.id).toBe("active");
  });
  it("is undefined with no revisions", () => {
    expect(defaultRevision([])).toBeUndefined();
  });
});

describe("isFrozen", () => {
  it("freezes activated and superseded revisions only", () => {
    expect(isFrozen({ state: "activated" })).toBe(true);
    expect(isFrozen({ state: "superseded" })).toBe(true);
    expect(isFrozen({ state: "draft" })).toBe(false);
    expect(isFrozen({ state: "approved" })).toBe(false);
  });
});

describe("summarizeChanges", () => {
  it("tallies each change kind, zero for kinds absent", () => {
    const c = (change: ApplicabilityChange["change"]): ApplicabilityChange => ({
      requirement_id: change,
      code: change,
      title: "",
      change,
      before: null,
      after: null,
    });
    expect(
      summarizeChanges([c("added"), c("added"), c("decision_changed")]),
    ).toEqual({
      added: 2,
      removed: 0,
      decision_changed: 1,
      justification_changed: 0,
    });
  });
});

describe("describeReferenceChange", () => {
  const change = (before: string, after: string) => ({
    requirement_id: "r",
    code: "A.5.1",
    title: "",
    change: "reference_changed" as const,
    before: {
      decision: "applicable" as const,
      justification: "",
      implementation_ref: before,
    },
    after: {
      decision: "applicable" as const,
      justification: "",
      implementation_ref: after,
    },
  });
  it("names the reference before and after", () => {
    expect(describeReferenceChange(change("wiki/a", "wiki/b"))).toBe(
      "reference changed: wiki/a → wiki/b",
    );
  });
  it("marks an empty side as none", () => {
    expect(describeReferenceChange(change("", "wiki/b"))).toBe(
      "reference changed: (none) → wiki/b",
    );
  });
});

describe("describeSide", () => {
  it("names the decision, its justification, or undecided", () => {
    expect(describeSide(null)).toBe("undecided");
    expect(
      describeSide({
        decision: "applicable",
        justification: "",
        implementation_ref: "x",
      }),
    ).toBe("applicable");
    expect(
      describeSide({
        decision: "excluded",
        justification: "No card data",
        implementation_ref: "",
      }),
    ).toBe("excluded — No card data");
  });
});
