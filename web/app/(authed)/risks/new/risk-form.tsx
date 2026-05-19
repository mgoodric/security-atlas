"use client";

// Slice 105 — risk-create form.
//
// Bound DIRECTLY to the slice-019 `createReq` wire shape
// (`internal/api/risks/handlers.go`). No invented fields per P0-A4. The
// optional fields not enumerated in AC-2 (description, residual_score,
// review_due_at, accepted_until, accepter, instrument_reference,
// linked_control_ids) are omitted from the UI rather than fabricated —
// a future slice adds the richer editor.
//
// Enum option lists mirror `internal/db/dbx/models.go`:
//   RiskCategory:    confidentiality | integrity | availability | privacy
//                    | regulatory | operational | financial
//   RiskMethodology: nist_800_30 (default) | fair | cis_ram | iso_27005
//                    | qualitative_5x5
//   RiskTreatment:   accept | mitigate (default) | transfer | avoid
//
// 5x5 inherent_score widget: two native `<select>` dropdowns (1..5 each)
// that serialize into `inherent_score: {likelihood, impact}`. This is
// the shape `severityOf()` reads to compute the 5x5 severity scalar
// (handlers.go) — sending it as a JSON object preserves the wire
// contract and lets the existing dashboard/heatmap consumers light up
// the row immediately. We use native `<select>` because shadcn/ui Select
// is not yet installed in `web/components/ui/` (vendor-form precedent).
//
// On 4xx the upstream error message renders in an Alert above the
// submit row and the form state is preserved (the user can fix the
// flagged field and resubmit without re-typing everything).

import { useState } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { RiskCreateInput } from "@/lib/api";

import { ControlMultiSelect } from "./control-multi-select";
import { FieldErrors, hasErrors, validateRiskForm } from "./validate";

const CATEGORIES = [
  "confidentiality",
  "integrity",
  "availability",
  "privacy",
  "regulatory",
  "operational",
  "financial",
] as const;

const METHODOLOGIES = [
  "nist_800_30",
  "fair",
  "cis_ram",
  "iso_27005",
  "qualitative_5x5",
] as const;

const TREATMENTS = ["mitigate", "transfer", "accept", "avoid"] as const;

const SCORE_LEVELS = [1, 2, 3, 4, 5] as const;

type FormState = {
  title: string;
  description: string;
  category: (typeof CATEGORIES)[number];
  methodology: (typeof METHODOLOGIES)[number];
  treatment: (typeof TREATMENTS)[number];
  treatment_owner: string;
  likelihood: number;
  impact: number;
  // Slice 151: linked_control_ids drives the ControlMultiSelect picker.
  // The form keeps the selection in state even when the picker is
  // hidden (treatment !== mitigate) so toggling treatment back to
  // mitigate does NOT wipe the user's prior selection (D-151-Q8).
  linked_control_ids: string[];
};

function initialState(): FormState {
  return {
    title: "",
    description: "",
    category: "operational",
    methodology: "nist_800_30",
    treatment: "mitigate",
    treatment_owner: "",
    likelihood: 3,
    impact: 3,
    linked_control_ids: [],
  };
}

function toCreateInput(s: FormState): RiskCreateInput {
  const body: RiskCreateInput = {
    title: s.title.trim(),
    description: s.description.trim(),
    category: s.category,
    methodology: s.methodology,
    treatment: s.treatment,
    treatment_owner: s.treatment_owner.trim(),
    inherent_score: { likelihood: s.likelihood, impact: s.impact },
  };
  // Slice 151: only post linked_control_ids when treatment requires
  // them. Sending an empty array for non-mitigate is noise; the
  // backend defaults to no links absent the field (D-151-4).
  if (s.treatment === "mitigate" && s.linked_control_ids.length > 0) {
    body.linked_control_ids = s.linked_control_ids;
  }
  return body;
}

type Props = {
  onSubmit: (body: RiskCreateInput) => Promise<void>;
};

// Plain Tailwind-styled select — shadcn does not ship a Select primitive
// in this repo yet, and the vendor-form precedent uses native controls.
const SELECT_CLASS =
  "h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring";

const LABEL_CLASS = "block text-sm font-medium text-foreground mb-1.5";

const HELP_CLASS = "mt-1 text-xs text-muted-foreground";

export function RiskForm({ onSubmit }: Props) {
  const [state, setState] = useState<FormState>(initialState());
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // Slice 151: field-level errors from validateRiskForm. Empty object
  // until the user attempts a submit (so we don't pre-shame an empty
  // form). After a failed submit, validation re-runs on every state
  // change so the error clears as the user fixes the field — the
  // P0-RISK-1 contract is "field-level error before submit", which
  // means clearing on input is required for honest UX.
  const [fieldErrors, setFieldErrors] = useState<FieldErrors>({});
  const [submitAttempted, setSubmitAttempted] = useState(false);

  function update<K extends keyof FormState>(key: K, value: FormState[K]) {
    setState((prev) => {
      const next = { ...prev, [key]: value };
      if (submitAttempted) {
        // Re-validate live after the first submit attempt so cleared
        // errors disappear immediately as the user fixes the field.
        setFieldErrors(validateRiskForm(next));
      }
      return next;
    });
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setSubmitAttempted(true);
    const errs = validateRiskForm(state);
    setFieldErrors(errs);
    if (hasErrors(errs)) {
      // Surface the inline field errors and stop. Server is not even
      // contacted — P0-RISK-1 ("client-side gate, not server bounce").
      return;
    }
    setSubmitting(true);
    setError(null);
    try {
      await onSubmit(toCreateInput(state));
    } catch (err) {
      const msg = (err as Error).message ?? "failed to create risk";
      setError(msg);
    } finally {
      setSubmitting(false);
    }
  }

  const severity = state.likelihood * state.impact;

  return (
    <form
      onSubmit={handleSubmit}
      className="space-y-6"
      data-testid="risks-create-form"
    >
      {error && (
        <Alert variant="destructive" data-testid="risks-create-error">
          <AlertTitle>Could not create risk</AlertTitle>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      <div>
        <label className={LABEL_CLASS} htmlFor="risk-title">
          Title <span className="text-destructive">*</span>
        </label>
        <Input
          id="risk-title"
          name="title"
          required
          value={state.title}
          onChange={(e) => update("title", e.target.value)}
          placeholder="Short, scannable risk statement"
          data-testid="risks-create-title"
        />
        {fieldErrors.title && (
          <p
            className="mt-1 text-sm text-destructive"
            data-testid="risks-create-title-error"
          >
            {fieldErrors.title}
          </p>
        )}
      </div>

      <div>
        <label className={LABEL_CLASS} htmlFor="risk-description">
          Description
        </label>
        <textarea
          id="risk-description"
          name="description"
          value={state.description}
          onChange={(e) => update("description", e.target.value)}
          rows={3}
          className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
          placeholder="Optional — context, threat actor, affected assets"
          data-testid="risks-create-description"
        />
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <div>
          <label className={LABEL_CLASS} htmlFor="risk-category">
            Category <span className="text-destructive">*</span>
          </label>
          <select
            id="risk-category"
            name="category"
            required
            value={state.category}
            onChange={(e) =>
              update("category", e.target.value as FormState["category"])
            }
            className={SELECT_CLASS}
            data-testid="risks-create-category"
          >
            {CATEGORIES.map((c) => (
              <option key={c} value={c}>
                {c}
              </option>
            ))}
          </select>
        </div>

        <div>
          <label className={LABEL_CLASS} htmlFor="risk-methodology">
            Methodology
          </label>
          <select
            id="risk-methodology"
            name="methodology"
            value={state.methodology}
            onChange={(e) =>
              update("methodology", e.target.value as FormState["methodology"])
            }
            className={SELECT_CLASS}
            data-testid="risks-create-methodology"
          >
            {METHODOLOGIES.map((m) => (
              <option key={m} value={m}>
                {m}
              </option>
            ))}
          </select>
          <p className={HELP_CLASS}>
            Default <span className="font-mono">nist_800_30</span> per canvas
            §6.2.
          </p>
        </div>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <div>
          <label className={LABEL_CLASS} htmlFor="risk-treatment">
            Treatment
          </label>
          <select
            id="risk-treatment"
            name="treatment"
            value={state.treatment}
            onChange={(e) =>
              update("treatment", e.target.value as FormState["treatment"])
            }
            className={SELECT_CLASS}
            data-testid="risks-create-treatment"
          >
            {TREATMENTS.map((t) => (
              <option key={t} value={t}>
                {t}
              </option>
            ))}
          </select>
        </div>

        <div>
          <label className={LABEL_CLASS} htmlFor="risk-treatment-owner">
            Treatment owner <span className="text-destructive">*</span>
          </label>
          <Input
            id="risk-treatment-owner"
            name="treatment_owner"
            required
            value={state.treatment_owner}
            onChange={(e) => update("treatment_owner", e.target.value)}
            placeholder="Person or role accountable for treatment"
            data-testid="risks-create-treatment-owner"
          />
          {fieldErrors.treatment_owner && (
            <p
              className="mt-1 text-sm text-destructive"
              data-testid="risks-create-treatment-owner-error"
            >
              {fieldErrors.treatment_owner}
            </p>
          )}
        </div>
      </div>

      {/* Slice 151: Control-link multi-select. Renders ONLY when
          treatment === 'mitigate' (D-151-Q8): toggling treatment away
          hides the picker but the selection persists in form state so
          toggling back restores it. */}
      {state.treatment === "mitigate" && (
        <ControlMultiSelect
          selectedIds={state.linked_control_ids}
          onChange={(ids) => update("linked_control_ids", ids)}
          showRequiredError={Boolean(fieldErrors.linked_control_ids)}
        />
      )}

      <fieldset
        className="rounded-md border border-input p-4"
        data-testid="risks-create-inherent-score"
      >
        <legend className="px-2 text-sm font-medium text-foreground">
          Inherent score (5×5)
        </legend>
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4 items-end">
          <div>
            <label className={LABEL_CLASS} htmlFor="risk-likelihood">
              Likelihood
            </label>
            <select
              id="risk-likelihood"
              name="likelihood"
              value={state.likelihood}
              onChange={(e) => update("likelihood", Number(e.target.value))}
              className={SELECT_CLASS}
              data-testid="risks-create-likelihood"
            >
              {SCORE_LEVELS.map((n) => (
                <option key={n} value={n}>
                  {n}
                </option>
              ))}
            </select>
          </div>
          <div>
            <label className={LABEL_CLASS} htmlFor="risk-impact">
              Impact
            </label>
            <select
              id="risk-impact"
              name="impact"
              value={state.impact}
              onChange={(e) => update("impact", Number(e.target.value))}
              className={SELECT_CLASS}
              data-testid="risks-create-impact"
            >
              {SCORE_LEVELS.map((n) => (
                <option key={n} value={n}>
                  {n}
                </option>
              ))}
            </select>
          </div>
          <div className="text-sm text-muted-foreground">
            Severity{" "}
            <span
              className="font-mono text-foreground"
              data-testid="risks-create-severity"
            >
              {severity}
            </span>{" "}
            (likelihood × impact)
          </div>
        </div>
      </fieldset>

      <div className="flex items-center gap-3">
        <Button
          type="submit"
          disabled={submitting}
          data-testid="risks-create-submit"
        >
          {submitting ? "Creating…" : "Create risk"}
        </Button>
        <span className="text-xs text-muted-foreground">
          On success you return to the risk register.
        </span>
      </div>
    </form>
  );
}
