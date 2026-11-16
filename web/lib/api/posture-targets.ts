// #2136 — an organisation's posture target per framework version.
//
// The platform holds one target per framework version: the coverage the
// organisation aims for, a warning and a critical threshold, and the
// direction that counts as better. GET /v1/frameworks/posture returns it on
// each row (`target`, null when none is set) and the dashboard tile colours
// coverage against it with the shared threshold rule (`thresholdBadgeColor`,
// the metric tiles' rule). Without a target the tile stays neutral.
//
// Every role reads a target. Setting one goes through the BFF to
// PUT /v1/framework-scopes/posture-targets/{framework_version_id}, which the
// platform refuses (403) for anyone but admin and grc_engineer, and refuses
// (409) for a version with no activated scope. The helpers here only decide
// what the tile offers; they never replace either refusal.

import { APIError } from "./base";
import { thresholdBadgeColor, type ThresholdColor } from "./metrics";

export type PostureDirection =
  | "higher_is_better"
  | "lower_is_better"
  | "target_is_better";

export const POSTURE_DIRECTIONS: { value: PostureDirection; label: string }[] =
  [
    { value: "higher_is_better", label: "Higher is better" },
    { value: "lower_is_better", label: "Lower is better" },
    { value: "target_is_better", label: "Closer to target is better" },
  ];

// PostureTarget mirrors posturetargets.Target in
// internal/api/posturetargets/target.go. Values are coverage percentages.
export type PostureTarget = {
  framework_version_id: string;
  target_value: number;
  warning_threshold: number;
  critical_threshold: number;
  direction: PostureDirection | string;
  updated_by?: string;
  updated_at?: string;
};

export type PostureTargetInput = {
  target_value: number;
  warning_threshold: number;
  critical_threshold: number;
  direction: PostureDirection;
};

/** The roles the platform lets set a posture target (hasTargetAdmin). */
const TARGET_ROLES = ["admin", "grc_engineer"];

/**
 * canSetPostureTarget mirrors the platform's guard, so the tile offers
 * "Set target" only to a caller whose PUT would succeed. Like
 * `canManageTaxonomy`, it reads what `/v1/me` echoes of the credential:
 * `is_admin` and the JWT's tenant role list in `owner_roles`. Anything
 * unknown is view-only.
 */
export function canSetPostureTarget(
  me: { is_admin?: boolean; owner_roles?: readonly string[] } | undefined,
): boolean {
  if (!me) return false;
  if (me.is_admin === true) return true;
  return (me.owner_roles ?? []).some((r) => TARGET_ROLES.includes(r));
}

/**
 * postureTileColor grades a tile's coverage against its target with the
 * shared threshold rule. No target, or no coverage (scope not set), is
 * neutral.
 */
export function postureTileColor(
  coverage: number | null,
  target: PostureTarget | null | undefined,
): ThresholdColor {
  if (coverage === null || !target) return "neutral";
  return thresholdBadgeColor(coverage, {
    target_value: String(target.target_value),
    warning_threshold: String(target.warning_threshold),
    critical_threshold: String(target.critical_threshold),
    direction: target.direction,
  });
}

/** The words that go with each colour, so colour is never the only cue. */
export const POSTURE_COLOR_LABEL: Record<ThresholdColor, string> = {
  green: "On target",
  yellow: "Warning",
  red: "Critical",
  neutral: "No target set",
};

/** The coverage text colour for each grade. */
export function postureColorClass(color: ThresholdColor): string {
  switch (color) {
    case "green":
      return "text-emerald-700 dark:text-emerald-400";
    case "yellow":
      return "text-amber-700 dark:text-amber-400";
    case "red":
      return "text-destructive";
    default:
      return "text-foreground";
  }
}

/**
 * postureTargetError checks a set before it is sent, with the platform's
 * rules (Input.Validate): every value 0..100 with at most two decimals,
 * and thresholds ordered the way the direction says. Returns the first
 * problem, or null. The platform checks again.
 */
export function postureTargetError(input: {
  target_value: number;
  warning_threshold: number;
  critical_threshold: number;
  direction: string;
}): string | null {
  const values: [string, number][] = [
    ["Target", input.target_value],
    ["Warning", input.warning_threshold],
    ["Critical", input.critical_threshold],
  ];
  for (const [name, v] of values) {
    if (!Number.isFinite(v) || v < 0 || v > 100) {
      return `${name} must be between 0 and 100.`;
    }
    if (Math.round(v * 100) !== v * 100) {
      return `${name} can have at most two decimal places.`;
    }
  }
  const t = input.target_value;
  const w = input.warning_threshold;
  const c = input.critical_threshold;
  switch (input.direction) {
    case "higher_is_better":
      return c <= w && w <= t
        ? null
        : "When higher is better, critical ≤ warning ≤ target.";
    case "lower_is_better":
      return t <= w && w <= c
        ? null
        : "When lower is better, target ≤ warning ≤ critical.";
    case "target_is_better":
      return Math.abs(w - t) <= Math.abs(c - t)
        ? null
        : "Warning must be no further from the target than critical.";
    default:
      return "Choose a direction.";
  }
}

/** setPostureTarget sets a framework version's target through the BFF. */
export async function setPostureTarget(
  frameworkVersionId: string,
  input: PostureTargetInput,
): Promise<PostureTarget> {
  const res = await fetch(
    `/api/framework-scopes/posture-targets/${encodeURIComponent(
      frameworkVersionId,
    )}`,
    {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    },
  );
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      // body not JSON — keep the status line
    }
    throw new APIError(res.status, msg);
  }
  return (await res.json()) as PostureTarget;
}
