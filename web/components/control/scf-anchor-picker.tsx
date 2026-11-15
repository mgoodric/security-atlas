"use client";

// ScfAnchorPicker: a search box over the SCF anchor catalog and a short
// filtered list; picking one hands its SCF ID to the caller. It never
// accepts a free-form anchor: every option is a real scf_anchors row
// (invariant #7), and the platform validates the choice again.
//
// Extracted from the vendor-claims page (slice 620) for the control
// import review page (#1785), which approves a row with a searched
// anchor. Testids are unchanged so both pages' specs select the same.

import { useMemo, type ReactNode } from "react";

import type { Anchor } from "@/lib/api/anchors";

// filterAnchors narrows the SCF-anchor catalog by a case-insensitive substring
// match over scf_id + family + name. A blank query caps the list at `cap` so
// the picker never renders the full ~1,400-anchor catalog.
export function filterAnchors(
  anchors: Anchor[],
  query: string,
  cap = 25,
): Anchor[] {
  const q = query.trim().toLowerCase();
  if (q === "") return anchors.slice(0, cap);
  return anchors
    .filter(
      (a) =>
        a.scf_id.toLowerCase().includes(q) ||
        a.family.toLowerCase().includes(q) ||
        a.name.toLowerCase().includes(q),
    )
    .slice(0, cap);
}

export function ScfAnchorPicker({
  prompt,
  anchors,
  loading,
  query,
  onQueryChange,
  onPick,
  pending,
  children,
}: {
  /** One line above the search box saying what picking an anchor does. */
  prompt: ReactNode;
  anchors: Anchor[];
  loading: boolean;
  query: string;
  onQueryChange: (v: string) => void;
  /** Called with the picked anchor's SCF ID. */
  onPick: (scfID: string) => void;
  pending: boolean;
  children?: ReactNode;
}) {
  const filtered = useMemo(
    () => filterAnchors(anchors, query),
    [anchors, query],
  );

  return (
    <div
      className="space-y-2 rounded-md border border-dashed p-3"
      data-testid="scf-anchor-picker"
    >
      <p className="text-xs font-medium text-muted-foreground">{prompt}</p>
      <input
        type="text"
        className="w-full rounded-md border p-2 text-sm"
        placeholder="Search SCF anchors (code, family, name)…"
        aria-label="Search SCF anchors"
        data-testid="scf-anchor-search"
        value={query}
        onChange={(e) => onQueryChange(e.target.value)}
      />
      {loading ? (
        <p className="text-xs text-muted-foreground">Loading anchors…</p>
      ) : filtered.length === 0 ? (
        <p
          className="text-xs text-muted-foreground"
          data-testid="scf-anchor-empty"
        >
          No matching SCF anchors.
        </p>
      ) : (
        <ul
          className="max-h-48 space-y-1 overflow-y-auto"
          data-testid="scf-anchor-options"
        >
          {filtered.map((a) => (
            <li key={a.id}>
              <button
                type="button"
                className="flex w-full flex-col rounded-md border px-2 py-1 text-left text-sm hover:bg-accent disabled:opacity-50"
                data-testid="scf-anchor-option"
                data-scf-id={a.scf_id}
                disabled={pending}
                onClick={() => onPick(a.scf_id)}
              >
                <span className="font-mono text-xs">{a.scf_id}</span>
                <span className="text-xs text-muted-foreground">
                  {a.family} · {a.name}
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
      {children}
    </div>
  );
}
