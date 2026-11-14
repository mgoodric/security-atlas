// Issue #1741 — pins the /controls view toggle, the per-view row
// mapping, the control-id links, and the header count wording.

import { describe, expect, test } from "vitest";

import type { AnchorWithState } from "@/lib/api/anchors";
import type { ControlWithState } from "@/lib/api/controls-list";

import {
  controlHref,
  controlsViewSummary,
  parseControlsView,
  parseHasControl,
  rowsFromAnchors,
  rowsFromControls,
} from "./view";

const CONTROL_ID = "c0000000-0000-4000-8000-000000000001";
const ANCHOR_ID = "a0000000-0000-4000-8000-000000000001";

function control(over: Partial<ControlWithState> = {}): ControlWithState {
  return {
    id: CONTROL_ID,
    title: "Quarterly access review",
    control_family: "Identity & Access Management",
    scf_id: "IAC-37",
    lifecycle_state: "active",
    bundle_id: "ctrl-iac-06",
    scf_anchor_id: ANCHOR_ID,
    state: null,
    frameworks: ["SOC2"],
    ...over,
  };
}

function anchor(over: Partial<AnchorWithState> = {}): AnchorWithState {
  return {
    id: ANCHOR_ID,
    scf_id: "IAC-37",
    family: "Identification & Authentication",
    name: "Multi-factor authentication",
    description: "",
    state: null,
    frameworks: [],
    ...over,
  };
}

describe("parseControlsView", () => {
  test("defaults to the tenant's controls", () => {
    expect(parseControlsView(null)).toBe("mine");
    expect(parseControlsView("")).toBe("mine");
    expect(parseControlsView("bogus")).toBe("mine");
  });
  test("catalog is opt-in", () => {
    expect(parseControlsView("catalog")).toBe("catalog");
  });
});

describe("parseHasControl", () => {
  test("maps the URL value to the forwarded boolean", () => {
    expect(parseHasControl("true")).toBe(true);
    expect(parseHasControl("false")).toBe(false);
    expect(parseHasControl(null)).toBeUndefined();
    expect(parseHasControl("all")).toBeUndefined();
    expect(parseHasControl("yes")).toBeUndefined();
  });
});

describe("rowsFromControls", () => {
  test("keys and links each row by its control id, not its anchor id", () => {
    const [row] = rowsFromControls([control()]);
    expect(row.key).toBe(CONTROL_ID);
    expect(row.controlId).toBe(CONTROL_ID);
    expect(controlHref(row)).toBe(`/controls/${CONTROL_ID}`);
    expect(row.anchor.name).toBe("Quarterly access review");
    expect(row.anchor.family).toBe("Identity & Access Management");
    expect(row.frameworks).toEqual(["SOC2"]);
  });
  test("carries the control's own state cell", () => {
    const [row] = rowsFromControls([
      control({
        state: {
          result: "fail",
          freshness_status: "stale",
          last_observed_at: null,
          evaluated_at: "2026-11-12T00:00:00Z",
        },
      }),
    ]);
    expect(row.state).toEqual({
      result: "fail",
      freshness_status: "stale",
      last_observed_at: null,
    });
  });
});

describe("rowsFromAnchors", () => {
  test("an anchor with a tenant control links to that control", () => {
    const [row] = rowsFromAnchors([anchor({ control_id: CONTROL_ID })]);
    expect(row.key).toBe(ANCHOR_ID);
    expect(controlHref(row)).toBe(`/controls/${CONTROL_ID}`);
  });
  test("an anchor without a control has no link", () => {
    const [row] = rowsFromAnchors([anchor()]);
    expect(row.controlId).toBeNull();
    expect(controlHref(row)).toBeNull();
  });
});

describe("controlsViewSummary", () => {
  test("my-controls view counts controls against the catalog", () => {
    expect(
      controlsViewSummary("mine", { controls: 39, catalogTotal: 1451 }),
    ).toBe("39 controls in this tenant · 1,451 SCF anchors in the catalog");
    expect(controlsViewSummary("mine", { controls: 1 })).toBe(
      "1 control in this tenant",
    );
  });
  test("catalog view counts anchors with a control", () => {
    expect(
      controlsViewSummary("catalog", {
        catalogTotal: 1451,
        withControlTotal: 37,
      }),
    ).toBe("37 of 1,451 SCF anchors have a control in this tenant");
    expect(controlsViewSummary("catalog", {})).toBe("");
  });
});
