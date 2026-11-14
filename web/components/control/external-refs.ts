// #1737 — pure helpers for the control-detail "External references" row.
//
// A control's external_refs (control.yaml) trace it to identifiers outside
// the platform: an auditor's control number ("reference") or a framework
// requirement it was audited against ("tested_under"). The page lists the
// tested_under entries first because they are what an auditor asks about.

import type { ExternalRefWire } from "@/lib/api/control-detail";

export type ExternalRefGroup = {
  kind: ExternalRefWire["kind"];
  label: string;
  refs: ExternalRefWire[];
};

const GROUP_ORDER: { kind: ExternalRefWire["kind"]; label: string }[] = [
  { kind: "tested_under", label: "Tested under" },
  { kind: "reference", label: "References" },
];

/**
 * Split refs into display groups, in GROUP_ORDER, dropping empty groups.
 * Order within a group is the manifest order.
 */
export function groupExternalRefs(
  refs: ExternalRefWire[] | undefined,
): ExternalRefGroup[] {
  if (!refs || refs.length === 0) return [];
  return GROUP_ORDER.map(({ kind, label }) => ({
    kind,
    label,
    refs: refs.filter((r) => r.kind === kind),
  })).filter((g) => g.refs.length > 0);
}

/** "soc2 CC3.1" — the chip text for one ref. */
export function formatExternalRef(ref: ExternalRefWire): string {
  return `${ref.system} ${ref.id}`;
}
