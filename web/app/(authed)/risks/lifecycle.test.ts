// #2105 — the risk treatment and acceptance forms' rules.

import { describe, expect, test } from "vitest";

import {
  CLOSURE_REASONS,
  acceptanceState,
  closureReasonLabel,
  dayAfter,
  isClosureReason,
  primaryAcceptanceAction,
  validateAcceptance,
  validateClose,
  validateTreatmentChange,
} from "./lifecycle";

const NOW = new Date("2026-11-15T12:00:00Z");
const draft = (
  over: Partial<{ justification: string; until: string; reason: string }>,
) => ({
  justification: "",
  until: "",
  reason: "",
  ...over,
});

describe("acceptanceState", () => {
  test("reads the acceptance from treatment and status", () => {
    expect(acceptanceState({ treatment: "mitigate" })).toBe("none");
    expect(
      acceptanceState({ treatment: "accept", acceptance_status: "proposed" }),
    ).toBe("proposed");
    expect(
      acceptanceState({ treatment: "accept", acceptance_status: "approved" }),
    ).toBe("approved");
  });

  test("leads with the next acceptance action", () => {
    expect(primaryAcceptanceAction("none")).toBe("propose");
    expect(primaryAcceptanceAction("proposed")).toBe("approve");
    expect(primaryAcceptanceAction("approved")).toBe("revoke");
  });
});

describe("validateAcceptance", () => {
  test("propose needs a justification and a future expiry", () => {
    expect(
      validateAcceptance("propose", draft({ until: "2027-01-01" }), NOW).ok,
    ).toBe(false);
    expect(
      validateAcceptance("propose", draft({ justification: "x" }), NOW).ok,
    ).toBe(false);
    expect(
      validateAcceptance(
        "propose",
        draft({ justification: "x", until: "2026-11-15" }),
        NOW,
      ).ok,
    ).toBe(false);
    expect(
      validateAcceptance(
        "propose",
        draft({ justification: " WAF ", until: dayAfter(NOW, 1) }),
        NOW,
      ),
    ).toEqual({
      ok: true,
      body: { justification: "WAF", proposed_until: "2026-11-16" },
    });
  });

  test("approve needs a future accepted_until", () => {
    expect(validateAcceptance("approve", draft({}), NOW).ok).toBe(false);
    expect(
      validateAcceptance("approve", draft({ until: "2026-11-15" }), NOW).ok,
    ).toBe(false);
    expect(
      validateAcceptance("approve", draft({ until: "2027-02-01" }), NOW),
    ).toEqual({
      ok: true,
      body: { accepted_until: "2027-02-01" },
    });
  });

  test("revoke needs a reason; reject does not", () => {
    expect(validateAcceptance("revoke", draft({ reason: "  " }), NOW).ok).toBe(
      false,
    );
    expect(
      validateAcceptance("revoke", draft({ reason: "gone" }), NOW),
    ).toEqual({
      ok: true,
      body: { reason: "gone" },
    });
    expect(validateAcceptance("reject", draft({}), NOW)).toEqual({
      ok: true,
      body: {},
    });
  });
});

describe("validateTreatmentChange", () => {
  test("refuses accept and unknown treatments", () => {
    expect(
      validateTreatmentChange({ treatment: "accept", instrumentReference: "" })
        .ok,
    ).toBe(false);
    expect(
      validateTreatmentChange({ treatment: "", instrumentReference: "" }).ok,
    ).toBe(false);
  });

  test("transfer needs an instrument reference, which only it sends", () => {
    expect(
      validateTreatmentChange({
        treatment: "transfer",
        instrumentReference: " ",
      }).ok,
    ).toBe(false);
    expect(
      validateTreatmentChange({
        treatment: "transfer",
        instrumentReference: "POL-7",
      }),
    ).toEqual({
      ok: true,
      body: { treatment: "transfer", instrument_reference: "POL-7" },
    });
    expect(
      validateTreatmentChange({
        treatment: "avoid",
        instrumentReference: "POL-7",
      }),
    ).toEqual({
      ok: true,
      body: { treatment: "avoid" },
    });
  });
});

describe("#2107 close", () => {
  test("requires a listed reason", () => {
    expect(validateClose({ reason: "", note: "" })).toEqual({
      ok: false,
      error: "Choose why the risk is closed.",
    });
    expect(validateClose({ reason: "duplicate", note: "" }).ok).toBe(false);
  });

  test("sends the note only when there is one, trimmed", () => {
    expect(validateClose({ reason: "merged", note: "  " })).toEqual({
      ok: true,
      body: { reason: "merged" },
    });
    expect(
      validateClose({ reason: "not_applicable", note: " Lease ended. " }),
    ).toEqual({
      ok: true,
      body: { reason: "not_applicable", note: "Lease ended." },
    });
  });

  test("labels every reason", () => {
    for (const r of CLOSURE_REASONS) {
      expect(isClosureReason(r)).toBe(true);
      expect(closureReasonLabel(r)).not.toBe(r);
    }
  });
});
