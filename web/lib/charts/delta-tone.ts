// #2095 — colour a change by its direction relative to a target, never
// by its sign alone.
//
// A delta is "improving" when it moves the value the way the target's
// direction says is better, "worsening" when it moves the other way.
// With no target there is nothing to measure against, so the tone is
// "neutral" whatever the sign. Colour is never the only cue: callers
// pair the tone with an arrow and `deltaAriaLabel`.

export type DeltaTone = "improving" | "worsening" | "flat" | "neutral";

export type DeltaTarget = {
  target_value?: string | number | null;
  // higher_is_better | lower_is_better | target_is_better
  direction: string;
} | null;

function targetNumber(target: DeltaTarget): number | undefined {
  if (!target) return undefined;
  const raw = target.target_value;
  if (raw === undefined || raw === null || raw === "") return undefined;
  const n = Number(raw);
  return Number.isFinite(n) ? n : undefined;
}

// deltaTone grades `delta`, the change that brought the series to
// `value`. For target_is_better it compares the distance to the target
// before and after the change.
export function deltaTone(
  value: number,
  delta: number,
  target: DeltaTarget,
): DeltaTone {
  const t = targetNumber(target);
  if (t === undefined || !target) return "neutral";
  if (delta === 0) return "flat";
  switch (target.direction) {
    case "higher_is_better":
      return delta > 0 ? "improving" : "worsening";
    case "lower_is_better":
      return delta < 0 ? "improving" : "worsening";
    case "target_is_better": {
      const before = Math.abs(value - delta - t);
      const after = Math.abs(value - t);
      if (after === before) return "flat";
      return after < before ? "improving" : "worsening";
    }
    default:
      return "neutral";
  }
}

// deltaToneClass maps a tone to its text colour. Neutral and flat share
// the muted foreground: no target, no verdict.
export function deltaToneClass(tone: DeltaTone): string {
  switch (tone) {
    case "improving":
      return "text-emerald-600 dark:text-emerald-400";
    case "worsening":
      return "text-destructive";
    default:
      return "text-muted-foreground";
  }
}

// deltaArrow is the non-colour cue for the sign of the change.
export function deltaArrow(delta: number): string {
  if (delta > 0) return "↑";
  if (delta < 0) return "↓";
  return "→";
}

// deltaAriaLabel spells out the change and, when there is a target,
// whether it moves toward it. `formatted` is the caller's signed,
// unit-formatted delta (e.g. "+2.0 pts").
export function deltaAriaLabel(tone: DeltaTone, formatted: string): string {
  switch (tone) {
    case "improving":
      return `${formatted}, improving against target`;
    case "worsening":
      return `${formatted}, worsening against target`;
    case "flat":
      return `${formatted}, unchanged`;
    default:
      return `${formatted}, no target set`;
  }
}
