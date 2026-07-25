// Slice 665 — pure validation fn for the board-pack generate-draft form.
//
// Extracted so vitest can pin the validation logic without standing up
// the React tree (the risks/new/validate.ts precedent — slice 151). The
// page imports `validateGenerateForm` and renders the returned field-error
// message inline.
//
// Background: clicking "Generate draft" with an empty quarter-end date
// silently no-opped — the submit handler's `if (periodEnd)` guard swallowed
// the empty case with no visible feedback, and the button's
// `disabled={!periodEnd}` left the operator unsure whether the action
// failed or was processing (audit ATLAS-015). This module is the
// client-side gate: an empty or malformed quarter-end date yields a
// field-level error message the form renders before any network call.

export type GenerateFormForValidation = {
  periodEnd: string;
};

export type GenerateFieldErrors = Partial<{
  periodEnd: string;
}>;

// A native <input type="date"> emits an ISO calendar date (YYYY-MM-DD)
// when the control holds a complete, valid date, and an empty string
// otherwise. We additionally guard the shape so a value that somehow
// reaches the handler without the expected format (controlled-input
// bypass, paste, programmatic set) is rejected rather than POSTed.
const ISO_DATE = /^\d{4}-\d{2}-\d{2}$/;

// validateGenerateForm returns a map of field -> error message. An empty
// object means the form is valid client-side and the submit can proceed.
export function validateGenerateForm(
  s: GenerateFormForValidation,
): GenerateFieldErrors {
  const errors: GenerateFieldErrors = {};

  const value = s.periodEnd.trim();
  if (!value) {
    errors.periodEnd = "Enter a quarter-end date.";
  } else if (!ISO_DATE.test(value) || Number.isNaN(Date.parse(value))) {
    errors.periodEnd = "Enter a valid quarter-end date (YYYY-MM-DD).";
  }

  return errors;
}

// hasErrors is a small convenience for the form: returns true iff any
// field has a non-empty error message. Mirrors risks/new/validate.ts.
export function hasErrors(e: GenerateFieldErrors): boolean {
  return Object.values(e).some((v) => v && v.length > 0);
}
