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

/**
 * A module's state for the caller, as `GET /v1/me/modules` reports it.
 * `no_access` (#2172): switched on, but outside the caller's module
 * access. Switched off wins: a module off for the tenant reads
 * `switched_off` for everyone.
 */
export type ModuleState = "on" | "switched_off" | "no_access";

/**
 * A feature inside an optional module (#2171). Its `state` is already
 * effective: `switched_off` when its module or its own switch is off.
 */
export type EffectiveFeature = {
  key: string;
  name: string;
  state: ModuleState;
  /** The web app paths the feature owns. */
  pages: string[];
};

/** One optional module in the `GET /v1/me/modules` response. */
export type EffectiveModule = EffectiveFeature & {
  /** The module's features; absent from an older platform. */
  features?: EffectiveFeature[];
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

/**
 * moduleForPage returns the optional module, or the feature inside one,
 * that owns `href`, if any. The longest owning page wins, so a feature's
 * page beats a broader page of another module.
 */
export function moduleForPage(
  modules: EffectiveModules,
  href: string,
): EffectiveFeature | undefined {
  let best: EffectiveFeature | undefined;
  let bestLen = -1;
  for (const m of modules ?? []) {
    for (const entry of [m, ...(m.features ?? [])]) {
      for (const p of entry.pages) {
        if (ownsPage(p, href) && p.length > bestLen) {
          best = entry;
          bestLen = p.length;
        }
      }
    }
  }
  return best;
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

/**
 * seesPage reports whether the caller may see links into the module that
 * owns `page` (#2174): a record's link section for another module (linked
 * policies on a control) renders only when this is true.
 *
 * - `undefined`: no modules were provided (a component rendered outside
 *   the authed shell), so nothing is hidden. The API still refuses.
 * - `null`: the read failed. Fail closed, as FeatureGate does: hidden.
 * - A page no module owns is core and always seen.
 */
export function seesPage(
  modules: EffectiveModules | undefined,
  page: string,
): boolean {
  if (modules === undefined) return true;
  if (modules === null) return false;
  const owner = moduleForPage(modules, page);
  return owner === undefined || owner.state === "on";
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
// one of three calm states. Only an admin (`is_admin`, the super-admin
// bit) is told where to switch the module on; everyone else is told to
// ask one, and never sees the Admin link. `no-access` (#2172) is a module
// that is on but outside the caller's module access.
export type FeatureGateState = "on" | "off-admin" | "off-member" | "no-access";

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
  const state = moduleForPage(modules, page)?.state;
  if (state === "on") {
    return "on";
  }
  if (state === "no_access") {
    return "no-access";
  }
  return isAdmin ? "off-admin" : "off-member";
}

/**
 * owningModuleName returns the display name of the optional module that
 * owns `href` (the module, never a feature inside it), so the no-access
 * message names the module the caller lacks: "You don't have access to
 * Third-party risk" on the vendor claims page.
 */
export function owningModuleName(
  modules: EffectiveModules,
  href: string,
): string | undefined {
  const owner = moduleForPage(modules, href);
  // The owner is one of `modules` or a feature inside one, so a page some
  // module owns always finds its module here.
  return (modules ?? []).find(
    (m) => m === owner || (m.features ?? []).some((f) => f === owner),
  )?.name;
}
