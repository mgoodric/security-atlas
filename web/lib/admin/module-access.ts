// #2172 — module access for each member (pure, client-safe).
//
// A member's module access is `null` for "all" (a wildcard that includes
// modules switched on later) or an explicit list of optional-module keys.
// It applies only to grc_engineer, control_owner and viewer: an admin
// always has every switched-on module and an auditor is scoped by audit
// periods instead, so the server ignores a value stored on either.

import type { FeatureFlag } from "@/lib/api/admin";

/** One optional module the admin can grant, from the server's registry. */
export type OptionalModule = { key: string; name: string };

/**
 * optionalModules lists the optional modules from GET /v1/admin/features:
 * each optional module's own switch, in registry order. The web app keeps
 * no module list of its own.
 */
export function optionalModules(
  flags: readonly FeatureFlag[],
): OptionalModule[] {
  const out: OptionalModule[] = [];
  for (const f of flags) {
    if (f.module_switch && !f.module_core && f.module && f.module_name) {
      if (!out.some((m) => m.key === f.module)) {
        out.push({ key: f.module, name: f.module_name });
      }
    }
  }
  return out;
}

/**
 * accessApplies reports whether module access narrows a member with these
 * roles: false when they hold admin or auditor.
 */
export function accessApplies(roles: readonly string[]): boolean {
  return !roles.includes("admin") && !roles.includes("auditor");
}

/**
 * accessSummary renders a member's module access for their row: "All
 * modules", "Core modules only", or the granted modules' names.
 */
export function accessSummary(
  access: readonly string[] | null | undefined,
  modules: readonly OptionalModule[],
): string {
  if (access === null || access === undefined) {
    return "All modules";
  }
  if (access.length === 0) {
    return "Core modules only";
  }
  return access
    .map((k) => modules.find((m) => m.key === k)?.name ?? k)
    .join(", ");
}
