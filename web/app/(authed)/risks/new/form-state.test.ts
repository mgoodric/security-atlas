// #2104 — the edit form opens on the stored risk and PATCHes only what
// the user changed; treatment and acceptance never go on the wire.

import { describe, expect, test } from "vitest";

import type { Risk } from "@/lib/api/risks";

import { riskToFormState, toPatchInput } from "./form-state";

const RISK: Risk = {
  id: "r1",
  title: "Stored title",
  description: "Stored description",
  category: "operational",
  methodology: "nist_800_30",
  inherent_score: { likelihood: 3, impact: 4 },
  treatment: "accept",
  acceptance_status: "proposed",
  treatment_owner: "owner-a",
  residual_score: {},
  review_due_at: "2027-03-31T17:00:00-07:00",
  accepter: "",
  instrument_reference: "",
  linked_control_ids: [],
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
  themes: [],
  severity: 12,
};

describe("risk edit form state (#2104)", () => {
  test("opens on the risk as stored, review date as its UTC day", () => {
    const s = riskToFormState(RISK);
    expect(s.title).toBe("Stored title");
    expect(s.likelihood).toBe(3);
    expect(s.impact).toBe(4);
    expect(s.review_due).toBe("2027-04-01");
  });

  // #2129: the owner is a user; free text is the unlinked owner.
  test("opens on the owner user, or the unlinked text", () => {
    const uid = "a1a1a1a1-0000-4000-8000-000000002129";
    const linked = riskToFormState({
      ...RISK,
      owner_user_id: uid,
      owner_unlinked: "",
    });
    expect(linked.owner_user_id).toBe(uid);
    expect(linked.treatment_owner).toBe("");
    const unlinked = riskToFormState({
      ...RISK,
      owner_user_id: null,
      owner_unlinked: "Finance",
    });
    expect(unlinked.treatment_owner).toBe("Finance");
    // An older response without owner_unlinked: treatment_owner is the text.
    const older = riskToFormState({ ...RISK, treatment_owner: "owner-a" });
    expect(older.treatment_owner).toBe("owner-a");
    expect(toPatchInput(unlinked, { ...unlinked, owner_user_id: uid })).toEqual(
      {
        owner_user_id: uid,
      },
    );
  });

  test("an untouched form sends nothing", () => {
    const s = riskToFormState(RISK);
    expect(toPatchInput(s, { ...s })).toEqual({});
  });

  test("sends only the changed fields, never treatment", () => {
    const opening = riskToFormState(RISK);
    const body = toPatchInput(opening, {
      ...opening,
      title: "  New title ",
      review_due: "2027-06-01",
      impact: 5,
      treatment: "avoid",
    });
    expect(body).toEqual({
      title: "New title",
      review_due_at: "2027-06-01T00:00:00Z",
      inherent_score: { likelihood: 3, impact: 5 },
    });
  });

  test("a methodology change sends the score on the new scale", () => {
    const opening = riskToFormState(RISK);
    const body = toPatchInput(opening, {
      ...opening,
      methodology: "fair",
      loss_event_frequency: "2",
      loss_magnitude: "1000",
    });
    expect(body).toEqual({
      methodology: "fair",
      inherent_score: {
        loss_event_frequency: 2,
        loss_magnitude: 1000,
        annualized_loss_exposure: 2000,
      },
    });
  });
});

// #2511: a risk logged with "Score later" opens with the box checked, and
// the edit sends no score until it is unchecked; then it sends the score
// even at the form's default values.
describe("score later (#2511)", () => {
  const UNSCORED: Risk = { ...RISK, inherent_score: {}, severity: 0 };

  test("a fresh form scores by default; an unscored risk opens on Score later", () => {
    expect(riskToFormState(RISK).score_later).toBe(false);
    expect(riskToFormState(UNSCORED).score_later).toBe(true);
    expect(riskToFormState({ ...RISK, inherent_score: null }).score_later).toBe(
      true,
    );
  });

  test("an unscored risk left unscored sends no score", () => {
    const opening = riskToFormState(UNSCORED);
    expect(toPatchInput(opening, { ...opening, title: "New" })).toEqual({
      title: "New",
    });
  });

  test("scoring an unscored risk sends the score at its default values", () => {
    const opening = riskToFormState(UNSCORED);
    expect(toPatchInput(opening, { ...opening, score_later: false })).toEqual({
      inherent_score: { likelihood: 3, impact: 3 },
    });
  });
});
