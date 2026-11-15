// Slice 484 — pure option-building for the SCF anchor-detail framework-version
// selector. The node-testable logic lives here (per the Select primitive's
// note that option-mapping logic belongs in lib, not the .tsx) so vitest can
// cover it without a DOM.

import type { AnchorDetail } from "@/lib/api/anchors";

// The sentinel value for "no pin — show each framework's current version"
// (ADR 0019 §4 default). Distinct from any real `slug:version` pin.
export const ALL_CURRENT = "__all_current__";

export type VersionOption = {
  // The <option> value: ALL_CURRENT for the default, or `slug:version` for a
  // specific pin forwarded to the BFF as ?framework_version=.
  value: string;
  // The human label, e.g. "SOC 2 — 2017".
  label: string;
};

// buildVersionOptions derives the selectable framework versions from an
// unpinned anchor-detail payload: one option per distinct (framework, version)
// present in the requirements, plus the leading ALL_CURRENT default. The
// `slug:version` pin value uses the row's own `framework_slug` (#1800: the
// rows carry the slug; the former nested shape did not, so this used to guess
// it from the display name).
//
// Deterministic: options are sorted by label after the leading default, so the
// dropdown order is stable across renders.
export function buildVersionOptions(
  detail: Pick<AnchorDetail, "requirements"> | null | undefined,
): VersionOption[] {
  const seen = new Map<string, VersionOption>();
  for (const r of detail?.requirements ?? []) {
    const value = `${r.framework_slug}:${r.framework_version}`;
    if (!seen.has(value)) {
      seen.set(value, {
        value,
        label: `${r.framework_name} — ${r.framework_version}`,
      });
    }
  }
  const specific = [...seen.values()].sort((a, b) =>
    a.label.localeCompare(b.label),
  );
  return [{ value: ALL_CURRENT, label: "All current versions" }, ...specific];
}

// pinFor maps a selected option value to the BFF `frameworkVersion` argument:
// the ALL_CURRENT sentinel maps to undefined (no pin); any real value passes
// through unchanged.
export function pinFor(selected: string): string | undefined {
  return selected === ALL_CURRENT ? undefined : selected;
}
