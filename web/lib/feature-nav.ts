// Slice 660 — feature-flag nav gating (pure, client-safe).
//
// This module holds ONLY pure logic + constants so it can be imported by
// client components (the gated OSCAL / board-pack pages) AND by the
// server-side nav resolver without pulling `next/headers` into the client
// bundle. The server-only fetch lives in `feature-nav.server.ts`.
//
// The href -> flag-key binding (NAV_FEATURE_GATES) is the single source of
// truth for which nav entries are gated. Keys mirror
// internal/featureflag.GatingKeys on the Go side.

import { APIError } from "@/lib/api/base";

// NAV_FEATURE_GATES maps a nav-item href to the feature-flag key that
// gates it. A nav item NOT in this map is never gated (renders always).
// Mirrors internal/featureflag.GatingKeys.
export const NAV_FEATURE_GATES: Record<string, string> = {
  "/oscal/component-definitions": "oscal.export",
  "/board-packs": "board.reporting",
};

export type EnabledModules = Record<string, boolean>;

/**
 * gateNavItems drops nav items whose gating flag is off.
 *
 * - An item with no gate (href not in NAV_FEATURE_GATES) always renders.
 * - A gated item renders only when its flag is explicitly `true` in the
 *   modules map. A missing key reads as off (fail-closed) — we never
 *   render a pre-GA nav link the route would 404 on.
 */
export function gateNavItems<T extends { href: string }>(
  items: readonly T[],
  modules: EnabledModules,
): T[] {
  return items.filter((item) => {
    const gateKey = NAV_FEATURE_GATES[item.href];
    if (!gateKey) {
      return true;
    }
    return modules[gateKey] === true;
  });
}

// FEATURE_DISABLED_MESSAGE is the error string the platform's
// featureflag.Gate middleware returns ({"error":"feature disabled"}) with
// a 404 when a module's flag is off. The gated FE pages match on it to
// render a clean "module not enabled" state instead of a raw error.
export const FEATURE_DISABLED_MESSAGE = "feature disabled";

/**
 * isFeatureDisabledError reports whether a thrown error is the gated-route
 * signal (404 + "feature disabled") from featureflag.Gate. Pure +
 * client-safe — used by the OSCAL + board-pack pages to branch to the
 * clean disabled panel.
 */
export function isFeatureDisabledError(err: unknown): boolean {
  return (
    err instanceof APIError &&
    err.status === 404 &&
    err.message === FEATURE_DISABLED_MESSAGE
  );
}

// FeatureGateState is what a gated route renders: the module itself, or
// one of two calm off states. Only an admin (`is_admin`, the super-admin
// bit) is told where to switch the module on; everyone else is told to
// ask one, and never sees the Admin link.
export type FeatureGateState = "on" | "off-admin" | "off-member";

/**
 * resolveFeatureGate decides a gated route's render from the caller's
 * enabled-modules read and their `is_admin` bit. Fail-closed like
 * gateNavItems: a missing key reads as off.
 */
export function resolveFeatureGate(
  modules: EnabledModules,
  flag: string,
  isAdmin: boolean,
): FeatureGateState {
  if (modules[flag] === true) {
    return "on";
  }
  return isAdmin ? "off-admin" : "off-member";
}
