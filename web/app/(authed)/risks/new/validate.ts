// Slice 151 — pure validation fn for the risk-create form.
//
// Extracted so vitest can pin the validation logic without standing up
// the React tree. The form imports `validateRiskForm` and renders the
// returned field-error messages inline.
//
// Mirrors canvas §6.1 per-treatment rules (the backend slice-019
// `internal/risk/treatment.go` is the source of truth). For slice 151
// we mirror ONLY the mitigate → ≥1 linked control rule client-side —
// the other treatment rules (accept → accepted_until + accepter,
// transfer → instrument_reference) are not yet rendered by the form
// (slice 105 omitted those inputs), so client-side validation for them
// would be dead code. The server-side validation still fires; the
// existing inline-error rendering surfaces those failures.

export type RiskTreatment =
  | "untreated"
  | "mitigate"
  | "transfer"
  | "accept"
  | "avoid";

// OFFERED_METHODOLOGIES lists only methodologies this form can save with a
// matching score widget. FAIR is offered because the form renders its
// loss_event_frequency/loss_magnitude dollar inputs; CIS RAM and ISO 27005 stay
// out of scope until they have real methodology-specific score widgets.
export const OFFERED_METHODOLOGIES = [
  "nist_800_30",
  "fair",
  "qualitative_5x5",
] as const;

export type RiskMethodology = (typeof OFFERED_METHODOLOGIES)[number];

// DEFAULT_TREATMENT — slice 663, revised by #1746.
//
// The risk-create form (risk-form.tsx) opens on "untreated": no treatment
// decision yet. It needs no satellite fields, so a fresh tenant with zero
// controls can still create its first risk (the slice 663 dead-end), and
// it records no decision the operator did not make. Slice 663 opened on
// "avoid" for the same reason, which silently recorded avoid decisions
// nobody took. The server stores untreated when the wire treatment is
// empty, so the default is consistent client-to-server.
//
// The mitigate rule is unchanged in the form: a new risk has no linked
// action plans yet, so mitigate still needs a linked control here.
export const DEFAULT_TREATMENT: RiskTreatment = "untreated";

export type RiskFormForValidation = {
  title: string;
  // #1817: the category slug. Optional so a caller without a picker skips
  // the check; "" means none is chosen (the list has not loaded, or the
  // organisation has no active category).
  category?: string;
  // #2129: the owner is a user, picked by id ("" for none). A risk whose
  // free-text owner matched no user keeps that text as treatment_owner,
  // shown as "Owner (unlinked)"; it stands in for an owner until one is
  // picked, so an edit of another field is not blocked on it.
  owner_user_id: string;
  treatment_owner: string;
  treatment: RiskTreatment;
  linked_control_ids: string[];
  // #2391: with treatment=accept, the proposal's justification and
  // proposed expiry (YYYY-MM-DD). Optional so a caller without the
  // inputs (the edit form) skips the check.
  justification?: string;
  proposed_until?: string;
};

export type FieldErrors = Partial<{
  title: string;
  category: string;
  owner: string;
  linked_control_ids: string;
  justification: string;
  proposed_until: string;
}>;

// todayUTC is today's calendar day in UTC, YYYY-MM-DD: the day the
// platform compares a proposed expiry against.
function todayUTC(): string {
  return new Date().toISOString().slice(0, 10);
}

// validateRiskForm returns a map of field → error message. An empty
// object means the form is valid client-side and the submit can proceed.
//
// The mitigate-requires-link rule is the only one slice 151 enforces
// here; it complements the server-side check in `internal/risk/treatment.go`
// (P0-RISK-1: client-side gate before submit, NOT reliance on server
// error display).
export function validateRiskForm(s: RiskFormForValidation): FieldErrors {
  const errors: FieldErrors = {};

  if (!s.title.trim()) {
    errors.title = "Title is required.";
  }
  if (s.category !== undefined && !s.category.trim()) {
    errors.category = "Category is required.";
  }
  if (!s.owner_user_id && !s.treatment_owner.trim()) {
    errors.owner = "Owner is required.";
  }
  if (s.treatment === "mitigate" && s.linked_control_ids.length === 0) {
    errors.linked_control_ids =
      "Select at least one control when treatment is mitigate.";
  }
  // #2391: the same rule as the platform's (risk.ValidateProposal).
  if (s.treatment === "accept") {
    if (s.justification !== undefined && !s.justification.trim()) {
      errors.justification = "Justification is required to propose acceptance.";
    }
    if (s.proposed_until !== undefined) {
      if (!s.proposed_until) {
        errors.proposed_until = "Proposed expiry is required.";
      } else if (s.proposed_until <= todayUTC()) {
        errors.proposed_until = "Proposed expiry must be after today.";
      }
    }
  }

  return errors;
}

// hasErrors is a small convenience for the form: returns true iff any
// field has a non-empty error message.
export function hasErrors(e: FieldErrors): boolean {
  return Object.values(e).some((v) => v && v.length > 0);
}
