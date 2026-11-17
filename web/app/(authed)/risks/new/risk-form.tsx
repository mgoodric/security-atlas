"use client";

// Slice 105 — risk-create form; #2104 adds its edit mode (see Props).
//
// Bound DIRECTLY to the slice-019 `createReq` wire shape
// (`internal/api/risks/handlers.go`). No invented fields per P0-A4. The
// optional fields not enumerated in AC-2 (residual_score, accepted_until,
// accepter, instrument_reference) are omitted from the UI rather than
// fabricated — a future slice adds the richer editor. review_due_at
// (#2059) shows created + the organisation review cadence and is sent
// only when the user changes it.
//
// Category (#1817): the organisation's active categories, read from
// `/api/risks/categories` and listed by name. The form posts the slug, so
// a category renamed while the form is open still resolves. The picker
// opens on the organisation default. A category the platform refuses
// (one archived after the form loaded) comes back as
// a 400 and renders in the error Alert like any other upstream refusal.
//
// Enum option lists mirror `internal/db/dbx/models.go`:
//   RiskMethodology: OE-592 — deliberately NARROWER than the dbx enum.
//                    The form offers only methodologies that have a matching
//                    score widget here: nist_800_30, qualitative_5x5, and FAIR.
//   RiskTreatment:   untreated (default) | mitigate | transfer | accept
//                    | avoid. accept is posted as a proposed acceptance
//                    (acceptance_status=proposed): the form has no
//                    accepter / expiry inputs, so it cannot approve one.
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

import { useQuery } from "@tanstack/react-query";
import { useState } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { EntityMultiPicker, EntityPicker } from "@/components/ui/entity-picker";
import { Input } from "@/components/ui/input";
import type { EntityKind } from "@/lib/entity-picker";
import Link from "next/link";

import { enumLabel } from "@/lib/labels/enum-labels";
import {
  fetchRiskCategories,
  type Risk,
  type RiskCategory,
  RiskCreateInput,
  type RiskPatchInput,
} from "@/lib/api/risks";

import {
  fairNumbers,
  type FormState,
  inherentScoreOf,
  initialState,
  riskToFormState,
  toPatchInput,
} from "./form-state";
import {
  DEFAULT_REVIEW_CADENCE_DAYS,
  defaultReviewDueDate,
  reviewDueToSend,
} from "./review-due";
import {
  DEFAULT_TREATMENT,
  FieldErrors,
  hasErrors,
  OFFERED_METHODOLOGIES,
  validateRiskForm,
} from "./validate";

const TREATMENTS = [
  "untreated",
  "mitigate",
  "transfer",
  "accept",
  "avoid",
] as const;

// #1746: the option text says what each value records, so the opening
// "untreated" reads as "no decision yet" rather than a choice.
const TREATMENT_LABELS: Record<(typeof TREATMENTS)[number], string> = {
  untreated: "Untreated (not yet decided)",
  mitigate: enumLabel("RiskTreatment", "mitigate"),
  transfer: enumLabel("RiskTreatment", "transfer"),
  accept: "Accept (proposed, pending approval)",
  avoid: enumLabel("RiskTreatment", "avoid"),
};

const SCORE_LEVELS = [1, 2, 3, 4, 5] as const;

const CONTROL_KINDS: readonly EntityKind[] = ["control"];

// #2059: the organisation risk review cadence, read so the form can show
// the review date the platform will set. Falls back to the platform
// default when the read fails; the platform applies the real cadence
// either way when the date is left unchanged.
async function fetchReviewCadenceDays(): Promise<number> {
  const res = await fetch("/api/risks/review-cadence", { cache: "no-store" });
  if (!res.ok) throw new Error(`review cadence: ${res.status}`);
  const body = (await res.json()) as { risk_review_cadence_days?: unknown };
  const days = body.risk_review_cadence_days;
  return typeof days === "number" && days > 0
    ? days
    : DEFAULT_REVIEW_CADENCE_DAYS;
}

function toCreateInput(
  s: FormState,
  reviewDueDefault: string,
): RiskCreateInput {
  const inherentScore = inherentScoreOf(s);
  const body: RiskCreateInput = {
    title: s.title.trim(),
    description: s.description.trim(),
    category: s.category,
    methodology: s.methodology,
    treatment: s.treatment,
    owner_user_id: s.owner_user_id,
    inherent_score: inherentScore,
  };
  if (s.treatment === "accept") {
    body.acceptance_status = "proposed";
  }
  // Slice 151: only post linked_control_ids when treatment requires
  // them. Sending an empty array for non-mitigate is noise; the
  // backend defaults to no links absent the field (D-151-4).
  if (s.treatment === "mitigate" && s.linked_control_ids.length > 0) {
    body.linked_control_ids = s.linked_control_ids;
  }
  const reviewDue = reviewDueToSend(s.review_due, reviewDueDefault);
  if (reviewDue) {
    body.review_due_at = reviewDue;
  }
  return body;
}

// #2104: with `initial`, the form edits that risk. It opens on the risk as
// stored, hides treatment and the control picker (treatment and acceptance
// are not record fields; PATCH refuses them), adds the instrument
// reference, and hands `onSave` only the fields the user changed.
type Props =
  | {
      initial?: undefined;
      onSubmit: (body: RiskCreateInput) => Promise<void>;
    }
  | {
      initial: Risk;
      onSave: (body: RiskPatchInput) => Promise<void>;
      onCancelHref: string;
    };

// Plain Tailwind-styled select — shadcn does not ship a Select primitive
// in this repo yet, and the vendor-form precedent uses native controls.
const SELECT_CLASS =
  "h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring";

const LABEL_CLASS = "block text-sm font-medium text-foreground mb-1.5";

const HELP_CLASS = "mt-1 text-xs text-muted-foreground";

function formatDollars(n: number): string {
  if (!Number.isFinite(n)) return "$0";
  return new Intl.NumberFormat("en-US", {
    style: "currency",
    currency: "USD",
    maximumFractionDigits: 0,
  }).format(n);
}

export function RiskForm(props: Props) {
  const editing = props.initial !== undefined;
  // The risk as the edit form opened on it; the PATCH sends what differs.
  const [opening] = useState<FormState | null>(() =>
    props.initial ? riskToFormState(props.initial) : null,
  );
  const [state, setState] = useState<FormState>(opening ?? initialState());
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
  const cadence = useQuery({
    queryKey: ["risks", "review-cadence"],
    queryFn: fetchReviewCadenceDays,
    staleTime: 60_000,
  });
  // Editing reads archived categories too, so a risk on one shows it; the
  // picker still offers no other archived category (#2104).
  const categoriesQ = useQuery<RiskCategory[]>({
    queryKey: editing
      ? ["risks", "categories", "with-archived"]
      : ["risks", "categories", "active"],
    queryFn: () => fetchRiskCategories(editing),
    staleTime: 60_000,
  });
  // Upstream already leaves archived categories out without
  // include_archived; filter again so the picker never offers one.
  const categories = (categoriesQ.data ?? []).filter(
    (c) => !c.archived || c.slug === opening?.category,
  );
  const methodologies: readonly string[] =
    opening &&
    !(OFFERED_METHODOLOGIES as readonly string[]).includes(opening.methodology)
      ? [...OFFERED_METHODOLOGIES, opening.methodology]
      : OFFERED_METHODOLOGIES;

  // Editing leaves treatment and control links alone, so the mitigate
  // rule (a new risk needs a control) does not apply to it.
  function validate(s: FormState) {
    return validateRiskForm({
      ...s,
      category: s.category || defaultCategory,
      treatment: editing ? DEFAULT_TREATMENT : s.treatment,
    });
  }
  // Until the user picks one, the picker shows the organisation default
  // (else the first listed category) and that is what gets posted.
  const defaultCategory =
    categories.find((c) => c.is_default)?.slug ?? categories[0]?.slug ?? "";
  const category = state.category || defaultCategory;
  const cadenceDays = cadence.data ?? DEFAULT_REVIEW_CADENCE_DAYS;
  const reviewDueDefault = defaultReviewDueDate(cadenceDays);

  function update<K extends keyof FormState>(key: K, value: FormState[K]) {
    setState((prev) => {
      const next = { ...prev, [key]: value };
      if (submitAttempted) {
        // Re-validate live after the first submit attempt so cleared
        // errors disappear immediately as the user fixes the field.
        setFieldErrors(validate(next));
      }
      return next;
    });
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setSubmitAttempted(true);
    const errs = validate(state);
    setFieldErrors(errs);
    if (hasErrors(errs)) {
      // Surface the inline field errors and stop. Server is not even
      // contacted — P0-RISK-1 ("client-side gate, not server bounce").
      return;
    }
    setSubmitting(true);
    setError(null);
    try {
      if (props.initial && opening) {
        await props.onSave(toPatchInput(opening, { ...state, category }));
      } else if (!props.initial) {
        await props.onSubmit(
          toCreateInput({ ...state, category }, reviewDueDefault),
        );
      }
    } catch (err) {
      const msg =
        (err as Error).message ??
        (editing ? "failed to save risk" : "failed to create risk");
      setError(msg);
    } finally {
      setSubmitting(false);
    }
  }

  const severity = state.likelihood * state.impact;
  const fair = fairNumbers(state);

  return (
    // #2089: noValidate — validateRiskForm renders errors inline beside
    // each field instead of the browser's native bubbles.
    <form
      onSubmit={handleSubmit}
      noValidate
      className="space-y-6"
      data-testid={editing ? "risks-edit-form" : "risks-create-form"}
    >
      {error && (
        <Alert variant="destructive" data-testid="risks-create-error">
          <AlertTitle>
            {editing ? "Could not save risk" : "Could not create risk"}
          </AlertTitle>
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
          aria-invalid={fieldErrors.title ? true : undefined}
          aria-describedby={fieldErrors.title ? "risk-title-error" : undefined}
          data-testid="risks-create-title"
        />
        {fieldErrors.title && (
          <p
            id="risk-title-error"
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
            value={category}
            onChange={(e) => update("category", e.target.value)}
            disabled={categories.length === 0}
            className={SELECT_CLASS}
            aria-invalid={fieldErrors.category ? true : undefined}
            aria-describedby={
              fieldErrors.category ? "risk-category-error" : undefined
            }
            data-testid="risks-create-category"
          >
            {categories.length === 0 && (
              <option value="">
                {categoriesQ.isLoading ? "Loading categories" : "No categories"}
              </option>
            )}
            {categories.map((c) => (
              <option key={c.slug} value={c.slug}>
                {c.name}
              </option>
            ))}
          </select>
          {categoriesQ.isError && (
            <p
              className="mt-1 text-sm text-destructive"
              data-testid="risks-create-category-load-error"
            >
              Could not load categories: {(categoriesQ.error as Error).message}
            </p>
          )}
          {fieldErrors.category && (
            <p
              id="risk-category-error"
              className="mt-1 text-sm text-destructive"
              data-testid="risks-create-category-error"
            >
              {fieldErrors.category}
            </p>
          )}
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
            {methodologies.map((m) => (
              <option key={m} value={m}>
                {enumLabel("RiskMethodology", m)}
              </option>
            ))}
          </select>
          <p className={HELP_CLASS}>
            NIST 800-30 is the default. FAIR uses annualized dollar exposure.
          </p>
        </div>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        {editing ? (
          <div>
            <label className={LABEL_CLASS} htmlFor="risk-instrument-reference">
              Instrument reference
            </label>
            <Input
              id="risk-instrument-reference"
              name="instrument_reference"
              value={state.instrument_reference}
              onChange={(e) => update("instrument_reference", e.target.value)}
              placeholder="Policy number or SOW for a transferred risk"
              data-testid="risks-edit-instrument-reference"
            />
            <p className={HELP_CLASS} data-testid="risks-edit-treatment-note">
              Treatment ({enumLabel("RiskTreatment", state.treatment)}) is not
              changed here.
            </p>
          </div>
        ) : (
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
                  {TREATMENT_LABELS[t]}
                </option>
              ))}
            </select>
          </div>
        )}

        <div>
          <label className={LABEL_CLASS} htmlFor="risk-owner">
            Owner <span className="text-destructive">*</span>
          </label>
          <EntityPicker
            kinds={["user"]}
            module="risk"
            id="risk-owner"
            value={state.owner_user_id}
            onChange={(id) => update("owner_user_id", id)}
            testId="risks-create-owner"
            error={fieldErrors.owner}
            errorId="risk-owner-error"
            placeholder="Search people by name"
            describedBy={
              !state.owner_user_id && state.treatment_owner
                ? "risk-owner-unlinked"
                : undefined
            }
          />
          {/* #2129: a free-text owner no user matched, kept until a user
              is picked. Rendered as text, never as markup. */}
          {!state.owner_user_id && state.treatment_owner ? (
            <p
              id="risk-owner-unlinked"
              className={HELP_CLASS}
              data-testid="risks-form-owner-unlinked"
            >
              Owner (unlinked): {state.treatment_owner}. Pick the person this
              names.
            </p>
          ) : null}
        </div>
      </div>

      <div>
        <label className={LABEL_CLASS} htmlFor="risk-review-due">
          Review due
        </label>
        <Input
          id="risk-review-due"
          name="review_due"
          type="date"
          className="w-48"
          value={state.review_due || reviewDueDefault}
          onChange={(e) => update("review_due", e.target.value)}
          data-testid="risks-create-review-due"
        />
        {editing ? null : (
          <p className={HELP_CLASS} data-testid="risks-create-review-due-help">
            Defaults to {cadenceDays} days from today, your organisation&apos;s
            risk review cadence. Change it for this risk if it needs an earlier
            or later review.
          </p>
        )}
      </div>

      {/* Slice 151: Control-link multi-select (the shared entity
          picker since #2087). Renders ONLY when treatment === 'mitigate'
          (D-151-Q8): toggling treatment away hides the picker but the
          selection persists in form state so toggling back restores it.
          Editing links controls from the risk page, not here (#2104). */}
      {!editing && state.treatment === "mitigate" && (
        <EntityMultiPicker
          kinds={CONTROL_KINDS}
          legend="Linked controls"
          required
          description={
            <>
              Required when treatment is{" "}
              <span className="font-mono">mitigate</span>. Select one or more
              controls that mitigate this risk.
            </>
          }
          searchPlaceholder="Search by title, SCF code, family, or bundle id"
          testId="risks-create-control-multi-select"
          value={state.linked_control_ids}
          onChange={(ids) => update("linked_control_ids", ids)}
          error={fieldErrors.linked_control_ids}
          emptyHint={
            <>
              No active controls in this tenant yet. Import your controls from
              the SCF catalog, or switch treatment to{" "}
              <span className="font-mono">accept</span> /{" "}
              <span className="font-mono">transfer</span> /{" "}
              <span className="font-mono">avoid</span>.
            </>
          }
        />
      )}

      {state.methodology === "fair" ? (
        <fieldset
          className="rounded-md border border-input p-4"
          data-testid="risks-create-inherent-score"
        >
          <legend className="px-2 text-sm font-medium text-foreground">
            FAIR inherent exposure
          </legend>
          <div className="grid grid-cols-1 md:grid-cols-3 gap-4 items-end">
            <div>
              <label
                className={LABEL_CLASS}
                htmlFor="risk-loss-event-frequency"
              >
                Loss event frequency
              </label>
              <Input
                id="risk-loss-event-frequency"
                name="loss_event_frequency"
                type="number"
                min="0"
                step="0.01"
                required
                value={state.loss_event_frequency}
                onChange={(e) => update("loss_event_frequency", e.target.value)}
                data-testid="risks-create-loss-event-frequency"
              />
            </div>
            <div>
              <label className={LABEL_CLASS} htmlFor="risk-loss-magnitude">
                Loss magnitude
              </label>
              <Input
                id="risk-loss-magnitude"
                name="loss_magnitude"
                type="number"
                min="0"
                step="1000"
                required
                value={state.loss_magnitude}
                onChange={(e) => update("loss_magnitude", e.target.value)}
                data-testid="risks-create-loss-magnitude"
              />
            </div>
            <div className="text-sm text-muted-foreground">
              Annualized exposure{" "}
              <span
                className="font-mono text-foreground"
                data-testid="risks-create-annualized-loss-exposure"
              >
                {formatDollars(fair.annualizedLossExposure)}
              </span>
            </div>
          </div>
        </fieldset>
      ) : (
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
      )}

      <div className="flex items-center gap-3">
        <Button
          type="submit"
          disabled={submitting}
          data-testid={editing ? "risks-edit-submit" : "risks-create-submit"}
        >
          {editing
            ? submitting
              ? "Saving…"
              : "Save changes"
            : submitting
              ? "Creating…"
              : "Create risk"}
        </Button>
        {props.initial ? (
          <Link
            href={props.onCancelHref}
            className="text-sm text-muted-foreground hover:underline"
            data-testid="risks-edit-cancel"
          >
            Cancel
          </Link>
        ) : (
          <span className="text-xs text-muted-foreground">
            On success you land on the new risk.
          </span>
        )}
      </div>
    </form>
  );
}
