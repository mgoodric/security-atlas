// Slice 613 — pure helpers for the per-tenant control-bundle gate-policy
// control in the Settings → Tenant section.
//
// The gate policy (`tenants.bundle_gate_mode`) is the slice-608 per-tenant
// upload gate for control bundles. Three modes, default `strict`:
//
//   - strict           block on red tests; allow a no-tests bundle (warning).
//   - advisory         accept a red bundle with a warning (non-blocking).
//   - mandatory_tests  reject a bundle that carries no tests.
//
// This module holds ONLY pure logic (the mode set, the human labels +
// one-line explanations, and a defensive parser). It is intentionally a
// `.ts` (not the page `.tsx`) so the vitest node tier can exercise every
// branch — the page component itself is Playwright-covered (slice 069:
// vitest is node-only, no JSX). No backend change: this drives the existing
// `PATCH /v1/tenants/{id}` surface that slice 608 shipped.

// GateMode is the canonical union of the three persisted values. It mirrors
// the platform's `controls.GateMode` enum (internal/api/controls); the wire
// strings are identical on both sides.
export type GateMode = "strict" | "advisory" | "mandatory_tests";

// GATE_MODES is the ordered list the control renders. `strict` is first
// because it is the default (slice 608 D2) and the safest posture.
export const GATE_MODES: readonly GateMode[] = [
  "strict",
  "advisory",
  "mandatory_tests",
] as const;

// DEFAULT_GATE_MODE is the value the control pre-selects when the current
// tenant value is unknown. Slice 608 D2: an absent tenants row (or any
// unrecognised stored value) resolves to `strict` server-side, so `strict`
// is the honest display default — it is exactly what the gate enforces for a
// tenant that has never changed the policy.
export const DEFAULT_GATE_MODE: GateMode = "strict";

// GateModeOption is the rendered shape: the persisted value, a short human
// label, and the AC-3 one-line explanation of the mode's effect.
export type GateModeOption = {
  value: GateMode;
  label: string;
  description: string;
};

// GATE_MODE_OPTIONS carries the label + AC-3 explanation for each mode. The
// copy is factual and measured (project tone discipline): it names the exact
// gate behavior, no superlatives.
export const GATE_MODE_OPTIONS: readonly GateModeOption[] = [
  {
    value: "strict",
    label: "Strict",
    description:
      "Block a bundle whose tests fail; allow a bundle with no tests (with a warning). The default.",
  },
  {
    value: "advisory",
    label: "Advisory",
    description:
      "Accept a bundle even when its tests fail, surfacing the red report as a warning instead of blocking.",
  },
  {
    value: "mandatory_tests",
    label: "Mandatory tests",
    description:
      "Reject a bundle that carries no tests, in addition to blocking on failing tests.",
  },
];

// isGateMode is the type guard for an arbitrary string.
export function isGateMode(v: unknown): v is GateMode {
  return v === "strict" || v === "advisory" || v === "mandatory_tests";
}

// parseGateMode maps an arbitrary wire value (e.g. from a PATCH response) to a
// GateMode, falling back to the default for anything unrecognised, empty, or
// missing. This mirrors the platform resolver's fail-safe-toward-strict read
// (slice 608 D2): the display never claims a looser posture than the server
// would actually enforce.
export function parseGateMode(v: unknown): GateMode {
  return isGateMode(v) ? v : DEFAULT_GATE_MODE;
}

// describeGateMode returns the AC-3 one-line explanation for a mode. Falls
// back to the default's description for an unknown value so the UI never
// renders an empty explanation.
export function describeGateMode(v: GateMode): string {
  const opt = GATE_MODE_OPTIONS.find((o) => o.value === v);
  return (opt ?? GATE_MODE_OPTIONS[0]).description;
}
