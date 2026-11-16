// #2104 — the risk form's state, shared by the create and edit routes.
//
// Pure, so vitest pins the edit-mode rules without a React tree:
//   - `riskToFormState` opens the edit form on the risk as stored.
//   - `toPatchInput` sends only the record fields the user changed. An
//     untouched form sends `{}`, which the edit page treats as "nothing
//     to save" rather than a PATCH the platform would refuse.
// Treatment and acceptance are never in the PATCH: the platform refuses
// them there.

import type { Risk, RiskPatchInput } from "@/lib/api/risks";

import { DEFAULT_TREATMENT, type RiskTreatment } from "./validate";

export type FormState = {
  title: string;
  description: string;
  // The category slug; "" until the user picks one, meaning the
  // organisation default the picker shows.
  category: string;
  methodology: string;
  treatment: RiskTreatment;
  // #2129: the owner, a user id ("" for none), set with the user picker.
  owner_user_id: string;
  // The free-text owner no user matched ("Owner (unlinked)"). Read-only:
  // the form shows it with a prompt to pick the user it names, and the
  // platform clears it once one is picked.
  treatment_owner: string;
  likelihood: number;
  impact: number;
  loss_event_frequency: string;
  loss_magnitude: string;
  // Slice 151: linked_control_ids drives the control picker
  // (`EntityMultiPicker`, #2087).
  // The form keeps the selection in state even when the picker is
  // hidden (treatment !== mitigate) so toggling treatment back to
  // mitigate does NOT wipe the user's prior selection (D-151-Q8).
  linked_control_ids: string[];
  // #2059: the review date the user picked, YYYY-MM-DD. Empty until they
  // touch the field; the field shows the computed default meanwhile.
  review_due: string;
  // #2104: edit mode only; create has no input for it.
  instrument_reference: string;
};

export function initialState(): FormState {
  return {
    title: "",
    description: "",
    category: "",
    methodology: "nist_800_30",
    // #1746: open on "untreated" (no decision yet, no required fields).
    // See validate.ts DEFAULT_TREATMENT.
    treatment: DEFAULT_TREATMENT,
    owner_user_id: "",
    treatment_owner: "",
    likelihood: 3,
    impact: 3,
    loss_event_frequency: "1",
    loss_magnitude: "50000",
    linked_control_ids: [],
    review_due: "",
    instrument_reference: "",
  };
}

export function fairNumbers(s: FormState) {
  const lossEventFrequency = Number(s.loss_event_frequency);
  const lossMagnitude = Number(s.loss_magnitude);
  const annualizedLossExposure = lossEventFrequency * lossMagnitude;
  return { lossEventFrequency, lossMagnitude, annualizedLossExposure };
}

/** The inherent_score the form's score widget encodes. */
export function inherentScoreOf(s: FormState): Record<string, number> {
  if (s.methodology === "fair") {
    const f = fairNumbers(s);
    return {
      loss_event_frequency: f.lossEventFrequency,
      loss_magnitude: f.lossMagnitude,
      annualized_loss_exposure: f.annualizedLossExposure,
    };
  }
  return { likelihood: s.likelihood, impact: s.impact };
}

function num(v: unknown): number | undefined {
  return typeof v === "number" && Number.isFinite(v) ? v : undefined;
}

/** A stored timestamp as its UTC calendar day, `YYYY-MM-DD`. */
export function utcDay(iso: string | undefined | null): string {
  if (!iso) return "";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "" : d.toISOString().slice(0, 10);
}

/** The edit form's opening state: the risk as stored. */
export function riskToFormState(risk: Risk): FormState {
  const s = initialState();
  const score = (risk.inherent_score ?? {}) as Record<string, unknown>;
  s.title = risk.title;
  s.description = risk.description;
  s.category = risk.category;
  s.methodology = risk.methodology;
  s.treatment = risk.treatment as RiskTreatment;
  s.owner_user_id = risk.owner_user_id ?? "";
  s.treatment_owner = risk.owner_user_id ? "" : risk.owner_unlinked ?? "";
  s.instrument_reference = risk.instrument_reference;
  s.linked_control_ids = risk.linked_control_ids;
  s.review_due = utcDay(risk.review_due_at);
  if (risk.methodology === "fair") {
    const lef = num(score.loss_event_frequency) ?? num(score.lef);
    const lm = num(score.loss_magnitude) ?? num(score.lm);
    if (lef !== undefined) s.loss_event_frequency = String(lef);
    if (lm !== undefined) s.loss_magnitude = String(lm);
  } else {
    s.likelihood = num(score.likelihood) ?? s.likelihood;
    s.impact = num(score.impact) ?? s.impact;
  }
  return s;
}

/** The PATCH body for the fields that differ from `initial`. */
export function toPatchInput(initial: FormState, s: FormState): RiskPatchInput {
  const body: RiskPatchInput = {};
  const title = s.title.trim();
  if (title !== initial.title.trim()) body.title = title;
  const description = s.description.trim();
  if (description !== initial.description.trim()) {
    body.description = description;
  }
  if (s.category && s.category !== initial.category) body.category = s.category;
  if (s.owner_user_id !== initial.owner_user_id) {
    body.owner_user_id = s.owner_user_id || null;
  }
  const instrument = s.instrument_reference.trim();
  if (instrument !== initial.instrument_reference.trim()) {
    body.instrument_reference = instrument;
  }
  if (s.review_due && s.review_due !== initial.review_due) {
    body.review_due_at = `${s.review_due}T00:00:00Z`;
  }
  const methodologyChanged = s.methodology !== initial.methodology;
  if (methodologyChanged) body.methodology = s.methodology;
  const score = inherentScoreOf(s);
  if (
    methodologyChanged ||
    JSON.stringify(score) !== JSON.stringify(inherentScoreOf(initial))
  ) {
    body.inherent_score = score;
  }
  return body;
}
