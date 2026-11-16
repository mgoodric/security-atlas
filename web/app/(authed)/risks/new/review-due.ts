// #2059 — the review date a new risk gets.
//
// The platform sets `review_due_at` to created + the organisation risk
// review cadence when the create body omits it (the
// risks_review_due_default trigger). The form shows that date before the
// risk exists and lets the user pick another; it sends `review_due_at`
// only when the user changed it, so an untouched form gets the platform's
// exact created + cadence timestamp.

/** The fallback cadence when the platform's value cannot be read. */
export const DEFAULT_REVIEW_CADENCE_DAYS = 365;

/** `now + cadenceDays` as a UTC calendar day, `YYYY-MM-DD`. */
export function defaultReviewDueDate(
  cadenceDays: number,
  now: Date = new Date(),
): string {
  const due = new Date(now.getTime());
  due.setUTCDate(due.getUTCDate() + cadenceDays);
  return due.toISOString().slice(0, 10);
}

/**
 * The `review_due_at` to send for the form's chosen date, or undefined to
 * let the platform apply the cadence: an empty choice or one equal to the
 * computed default is not sent.
 */
export function reviewDueToSend(
  chosen: string,
  computedDefault: string,
): string | undefined {
  const day = chosen.trim();
  if (day === "" || day === computedDefault) return undefined;
  return `${day}T00:00:00Z`;
}
