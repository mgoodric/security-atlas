// Slice 660 / #2170 — module nav gating (pure, client-safe).
//
// This module holds ONLY pure logic + types so it can be imported by
// client components (the gated pages' error handling) AND by the
// server-side nav resolver without pulling `next/headers` into the client
// bundle. The server-only fetch lives in `feature-nav.server.ts`.
//
// The web app keeps no gating map of its own (#2170). Which pages belong
// to which optional module comes from the server's module registry
// (internal/featureflag.Modules), through `GET /v1/me/modules`: each
// module reports its state for the caller and the pages it owns.

import { APIError } from "@/lib/api/base";

/** A module's state for the caller, as `GET /v1/me/modules` reports it. */
export type ModuleState = "on" | "switched_off";

/** One optional module in the `GET /v1/me/modules` response. */
export type EffectiveModule = {
  key: string;
  name: string;
  state: ModuleState;
  /** The web app paths the module owns. */
  pages: string[];
};

/**
 * The caller's effective modules, or `null` when the read failed and the
 * modules are unknown.
 */
export type EffectiveModules = EffectiveModule[] | null;

/**
 * ownsPage reports whether a module page owns `href`: the page itself,
 * or any path below it (`/questionnaires` owns `/questionnaires/{id}`).
 */
function ownsPage(page: string, href: string): boolean {
  return href === page || href.startsWith(`${page}/`);
}

/** moduleForPage returns the optional module that owns `href`, if any. */
export function moduleForPage(
  modules: EffectiveModules,
  href: string,
): EffectiveModule | undefined {
  return modules?.find((m) => m.pages.some((p) => ownsPage(p, href)));
}

/**
 * gateNavItems drops nav items whose module is not on for the caller.
 *
 * - An item no module owns is core and always renders.
 * - An item a module owns renders only when that module is `on`.
 * - When the read failed (`null`), no module is known, so every item
 *   renders. The page itself is still gated: FeatureGate fails closed.
 */
export function gateNavItems<T extends { href: string }>(
  items: readonly T[],
  modules: EffectiveModules,
): T[] {
  return items.filter((item) => {
    const owner = moduleForPage(modules, item.href);
    return owner === undefined || owner.state === "on";
  });
}

// FEATURE_DISABLED_MESSAGE is the error string the platform's module gate
// returns ({"error":"feature disabled"}) with a 404 when a module is
// switched off. The gated FE pages match on it to render a clean "module
// not enabled" state instead of a raw error.
export const FEATURE_DISABLED_MESSAGE = "feature disabled";

/**
 * isFeatureDisabledError reports whether a thrown error is the gated-route
 * signal (404 + "feature disabled") from the module gate. Pure +
 * client-safe — used by the gated pages to branch to the clean disabled
 * panel.
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
 * resolveFeatureGate decides a gated page's render from the caller's
 * effective modules and their `is_admin` bit. Fail-closed: a page whose
 * module is unknown (the read failed) reads as off.
 */
export function resolveFeatureGate(
  modules: EffectiveModules,
  page: string,
  isAdmin: boolean,
): FeatureGateState {
  if (moduleForPage(modules, page)?.state === "on") {
    return "on";
  }
  return isAdmin ? "off-admin" : "off-member";
}
