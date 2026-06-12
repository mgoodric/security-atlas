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

export type RiskTreatment = "mitigate" | "transfer" | "accept" | "avoid";

// DEFAULT_TREATMENT — slice 663.
//
// The risk-create form (risk-form.tsx) opens on this treatment. It is
// deliberately "avoid", NOT "mitigate".
//
// Why: "mitigate" requires >=1 linked control (canvas §6.1). On a fresh
// tenant with zero instantiated controls there is nothing to link, so a
// mitigate-default form CANNOT be submitted — the primary create flow
// dead-ends for a brand-new operator (slice 663 / ATLAS-006).
//
// "avoid" is the only treatment with zero required satellite fields
// (canvas §6.1: accept needs accepter + accepted_until; transfer needs
// instrument_reference; mitigate needs a linked control; avoid is
// status-only). It also matches the SERVER's own omitted-treatment
// safe-default (`internal/api/risks/handlers.go` falls back to
// RiskTreatmentAvoid when the wire treatment is empty), so the default
// is consistent client-to-server.
//
// The mitigate-requires-control rule is UNCHANGED: an operator who
// picks "mitigate" in a populated tenant still must link a control
// (slice 663 AC-3). Only the *opening default* moved.
export const DEFAULT_TREATMENT: RiskTreatment = "avoid";

export type RiskFormForValidation = {
  title: string;
  treatment_owner: string;
  treatment: RiskTreatment;
  linked_control_ids: string[];
};

export type FieldErrors = Partial<{
  title: string;
  treatment_owner: string;
  linked_control_ids: string;
}>;

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
  if (!s.treatment_owner.trim()) {
    errors.treatment_owner = "Treatment owner is required.";
  }
  if (s.treatment === "mitigate" && s.linked_control_ids.length === 0) {
    errors.linked_control_ids =
      "Select at least one control when treatment is mitigate.";
  }

  return errors;
}

// hasErrors is a small convenience for the form: returns true iff any
// field has a non-empty error message.
export function hasErrors(e: FieldErrors): boolean {
  return Object.values(e).some((v) => v && v.length > 0);
}
