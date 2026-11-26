// #2171 — Admin › Features groups switches by module (pure, client-safe).
//
// Which module a flag belongs to, and whether it is the module's own
// switch or a feature inside it, comes from the server's module registry
// through `GET /v1/admin/features` (`module`, `module_switch`, …). The
// page keeps no map of its own.

import type { FeatureFlag } from "@/lib/api/admin";

export type ModuleGroup = {
  key: string;
  name: string;
  /** A core module is always on and has no switch. */
  core: boolean;
  /** The optional module's own switch, when it has one. */
  moduleSwitch?: FeatureFlag;
  /** The features inside the module, by key. */
  features: FeatureFlag[];
};

const OTHER = "other";

/**
 * groupByModule groups flags under their module: optional modules first,
 * then core ones, each set by name, with a flag the registry places in no
 * module under "Other" last.
 */
export function groupByModule(flags: readonly FeatureFlag[]): ModuleGroup[] {
  const groups = new Map<string, ModuleGroup>();
  for (const f of flags) {
    const key = f.module || OTHER;
    let g = groups.get(key);
    if (!g) {
      g = {
        key,
        name: f.module ? f.module_name ?? f.module : "Other",
        core: f.module_core === true,
        features: [],
      };
      groups.set(key, g);
    }
    if (f.module_switch) {
      g.moduleSwitch = f;
    } else {
      g.features.push(f);
    }
  }
  const rank = (g: ModuleGroup) => (g.key === OTHER ? 2 : g.core ? 1 : 0);
  const out = [...groups.values()];
  for (const g of out) {
    g.features.sort((a, b) => a.key.localeCompare(b.key));
  }
  return out.sort((a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name));
}
