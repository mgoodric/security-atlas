import { describe, expect, it } from "vitest";

import { treatmentBadge } from "./treatment-badge";

describe("treatmentBadge", () => {
  it.each([
    [{ treatment: "mitigate" }, "Mitigate", "progress"],
    [
      { treatment: "accept", acceptance_status: "approved" as const },
      "Accept (approved)",
      "secondary",
    ],
    [
      { treatment: "accept", acceptance_status: "proposed" as const },
      "Accept (proposed)",
      "warning",
    ],
    // An accept row with no status is not presented as signed off.
    [{ treatment: "accept" }, "Accept", "warning"],
    [{ treatment: "avoid" }, "Avoid", "outline"],
    [{ treatment: "untreated" }, "Untreated", "outline"],
  ])("%o -> %s / %s", (risk, label, variant) => {
    expect(treatmentBadge(risk)).toEqual({ label, variant });
  });

  it("distinguishes a proposed acceptance from an approved one", () => {
    const proposed = treatmentBadge({
      treatment: "accept",
      acceptance_status: "proposed",
    });
    const approved = treatmentBadge({
      treatment: "accept",
      acceptance_status: "approved",
    });
    expect(proposed.label).not.toBe(approved.label);
    expect(proposed.variant).not.toBe(approved.variant);
  });
});
