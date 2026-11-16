// Issue #1778 — pure helpers for the SCF remap review page. No React, no
// I/O, so vitest covers them directly.

import type {
  EdgeRemapProposal,
  RemapClass,
  RemapProposal,
} from "@/lib/api/remap";
import { REMAP_CLASSES } from "@/lib/api/remap";

export const REMAP_CLASS_LABELS: Record<RemapClass, string> = {
  unchanged: "Unchanged",
  renamed: "Renamed",
  split: "Split",
  reused_id: "Reused ID",
  no_successor: "No successor",
};

// A reviewer can still decide an open proposal, and a rejected one, which
// stays revisitable. Rebound, approved and kept_legacy are settled.
export function isPending(p: { status: string }): boolean {
  return p.status === "open" || p.status === "rejected";
}

/** pendingByClass groups the pending proposals by class, in tab order. */
export function pendingByClass(
  proposals: RemapProposal[],
): Record<RemapClass, RemapProposal[]> {
  const out = Object.fromEntries(
    REMAP_CLASSES.map((c) => [c, [] as RemapProposal[]]),
  ) as Record<RemapClass, RemapProposal[]>;
  for (const p of proposals) {
    if (isPending(p) && p.class in out) out[p.class].push(p);
  }
  return out;
}

/**
 * needsExplicitAnchor: the page sends an explicit scf_id for these
 * classes. Split and no_successor have no single successor; reused_id has
 * an ambiguous code, so it is never approved by default.
 */
export function needsExplicitAnchor(c: RemapClass): boolean {
  return c === "split" || c === "no_successor" || c === "reused_id";
}

/**
 * edgesFor returns the pending edge proposals on the same legacy anchor,
 * by the same open-or-rejected rule as the control tabs.
 */
export function edgesFor(
  p: Pick<RemapProposal, "legacy_scf_id" | "legacy_release">,
  edges: EdgeRemapProposal[],
): EdgeRemapProposal[] {
  return edges.filter(
    (e) =>
      isPending(e) &&
      e.legacy_scf_id === p.legacy_scf_id &&
      e.legacy_release === p.legacy_release,
  );
}

// SCF codes ("IAC-21", "GOV-01.1") and release labels ("2025.2",
// "test-2026.3"). A code reaches an upstream path segment and a release
// an upstream query string, so the BFF rejects anything else, including a
// bare "..".
const SCF_CODE_RE = /^[A-Za-z][A-Za-z0-9.-]{0,31}$/;
const RELEASE_RE = /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/;

export function isSCFCode(v: string): boolean {
  return SCF_CODE_RE.test(v);
}

export function isReleaseLabel(v: string): boolean {
  return RELEASE_RE.test(v);
}

/** matchesAnchorQuery: case-insensitive match on code or title. */
export function matchesAnchorQuery(
  a: { scf_id: string; name: string },
  q: string,
): boolean {
  const needle = q.trim().toLowerCase();
  if (!needle) return false;
  return (
    a.scf_id.toLowerCase().includes(needle) ||
    a.name.toLowerCase().includes(needle)
  );
}
