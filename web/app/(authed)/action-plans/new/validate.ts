// Slice 384 — pure-Go-spirit form validators for the action-plan create
// form. Mirrors the backend bounds in internal/actionplan/statemachine.go so
// the client gives fast feedback; the server re-validates (defense in depth).

export const MAX_TITLE = 200;
export const MAX_DESCRIPTION = 4000;
export const MAX_TRIGGERING_EVENT = 500;
export const MAX_LINKED = 50;

export type ActionPlanFormValues = {
  title: string;
  description: string;
  triggeringEvent: string;
  ownerId: string;
  dueDate: string; // YYYY-MM-DD or ""
  riskIds: string[];
  controlIds: string[];
};

export type FieldErrors = Partial<
  Record<
    | "title"
    | "ownerId"
    | "description"
    | "triggeringEvent"
    | "dueDate"
    | "links",
    string
  >
>;

const UUID_RE =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/** Validate the create form. Returns a (possibly empty) error map. */
export function validateActionPlanForm(v: ActionPlanFormValues): FieldErrors {
  const errs: FieldErrors = {};

  const title = v.title.trim();
  if (!title) errs.title = "Title is required.";
  else if (title.length > MAX_TITLE)
    errs.title = `Title must be ${MAX_TITLE} characters or fewer.`;

  if (v.description.length > MAX_DESCRIPTION)
    errs.description = `Description must be ${MAX_DESCRIPTION} characters or fewer.`;

  if (v.triggeringEvent.length > MAX_TRIGGERING_EVENT)
    errs.triggeringEvent = `Triggering event must be ${MAX_TRIGGERING_EVENT} characters or fewer.`;

  const owner = v.ownerId.trim();
  if (!owner) errs.ownerId = "Owner is required.";
  else if (!UUID_RE.test(owner)) errs.ownerId = "Owner must be a user UUID.";

  if (v.dueDate) {
    const due = new Date(v.dueDate);
    if (Number.isNaN(due.getTime())) {
      errs.dueDate = "Due date is not a valid date.";
    } else {
      const horizon = new Date();
      horizon.setFullYear(horizon.getFullYear() + 5);
      if (due.getTime() > horizon.getTime())
        errs.dueDate = "Due date cannot be more than 5 years out.";
    }
  }

  if (v.riskIds.length > MAX_LINKED || v.controlIds.length > MAX_LINKED)
    errs.links = `At most ${MAX_LINKED} risks and ${MAX_LINKED} controls.`;

  return errs;
}

export function hasErrors(e: FieldErrors): boolean {
  return Object.keys(e).length > 0;
}
