// Slice 263 — Stage C citation picker.
//
// Closes ISC-31..ISC-34 + AC-15..AC-17:
//
//   - "+ Cite" button opens a command-palette-style popover anchored
//     to the answer editor.
//   - The palette has a single search input that debounces by 250ms
//     before hitting /api/search?q=<query>&types=controls,evidence
//     (slice 268's unified search BFF — NO new endpoint per P0-263-7).
//   - Results render grouped by type (controls first, evidence second).
//   - Clicking a result row calls onPick with the citation envelope;
//     the parent (answer-editor) appends to the question's citation list
//     and PATCHes the answer.
//   - Currently-attached citations render as removable chips below the
//     textarea via the CitationChips sub-component.
//
// Implementation note: shadcn's <Command> primitive is NOT installed
// in this repo. We build a minimal command-palette inline using
// existing primitives (button + input + a popover-style div), matching
// the pattern slice 223's global-search.tsx already established for
// the topbar ⌘K palette. This keeps the dependency surface small.

"use client";

import { useEffect, useRef, useState } from "react";

import type { Citation } from "./types";

const DEBOUNCE_MS = 250;
const PER_TYPE_LIMIT = 10;

interface SearchHit {
  id: string;
  type: "controls" | "evidence" | string;
  title: string;
  snippet: string;
}

interface SearchResponse {
  hits?: SearchHit[];
}

async function searchAtlas(q: string): Promise<SearchHit[]> {
  const trimmed = q.trim();
  if (trimmed.length < 2) return [];
  const params = new URLSearchParams({
    q: trimmed,
    types: "controls,evidence",
    limit: String(PER_TYPE_LIMIT * 2),
  });
  const res = await fetch(`/api/search?${params.toString()}`, {
    cache: "no-store",
  });
  if (!res.ok) return [];
  const data = (await res.json()) as SearchResponse;
  return (data.hits ?? []).filter(
    (h) => h.type === "controls" || h.type === "evidence",
  );
}

interface CitationPickerProps {
  onPick: (citation: Citation) => void;
}

export function CitationPicker({ onPick }: CitationPickerProps) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<SearchHit[]>([]);
  const [loading, setLoading] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (!containerRef.current) return;
      if (!containerRef.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    return () => document.removeEventListener("mousedown", onDown);
  }, [open]);

  useEffect(() => {
    if (!open) return;
    inputRef.current?.focus();
  }, [open]);

  useEffect(() => {
    let cancelled = false;
    const trimmed = query.trim();
    if (trimmed.length < 2) {
      // setState through setTimeout(0) to satisfy react-hooks/set-state-
      // in-effect (slice 223 global-search.tsx pattern).
      const microTimer = setTimeout(() => {
        if (cancelled) return;
        setResults([]);
        setLoading(false);
      }, 0);
      return () => {
        cancelled = true;
        clearTimeout(microTimer);
      };
    }
    const showLoading = setTimeout(() => {
      if (!cancelled) setLoading(true);
    }, 0);
    const timer = setTimeout(async () => {
      try {
        const hits = await searchAtlas(trimmed);
        if (!cancelled) {
          setResults(hits);
          setLoading(false);
        }
      } catch {
        if (!cancelled) {
          setResults([]);
          setLoading(false);
        }
      }
    }, DEBOUNCE_MS);
    return () => {
      cancelled = true;
      clearTimeout(showLoading);
      clearTimeout(timer);
    };
  }, [query, open]);

  const controls = results.filter((r) => r.type === "controls");
  const evidence = results.filter((r) => r.type === "evidence");

  function pickHit(hit: SearchHit): void {
    onPick({
      id: hit.id,
      type: hit.type as Citation["type"],
      title: hit.title,
    });
    setOpen(false);
    setQuery("");
    setResults([]);
  }

  return (
    <div ref={containerRef} className="relative inline-block">
      <button
        type="button"
        data-testid="citation-picker-open"
        onClick={() => setOpen((v) => !v)}
        className="text-xs text-primary hover:underline font-medium"
      >
        + Cite
      </button>
      {open ? (
        <div
          role="dialog"
          aria-label="Search controls and evidence"
          data-testid="citation-picker-popover"
          className="absolute left-0 top-full mt-1 w-96 max-h-96 overflow-y-auto rounded-md border border-border bg-popover text-popover-foreground shadow-lg z-50"
        >
          <div className="p-2 border-b border-border">
            <input
              ref={inputRef}
              type="text"
              data-testid="citation-picker-input"
              placeholder="Search controls and evidence…"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              className="w-full px-2 py-1 text-xs bg-muted/40 border border-border rounded focus:outline-none focus:ring-1 focus:ring-ring"
            />
          </div>
          <div className="max-h-72 overflow-y-auto">
            {query.trim().length < 2 ? (
              <div className="px-3 py-2 text-xs text-muted-foreground">
                Type at least 2 characters to search.
              </div>
            ) : loading ? (
              <div className="px-3 py-2 text-xs text-muted-foreground">
                Searching…
              </div>
            ) : results.length === 0 ? (
              <div className="px-3 py-2 text-xs text-muted-foreground">
                No matches.
              </div>
            ) : (
              <>
                {controls.length > 0 ? (
                  <Group
                    label="Controls"
                    type="controls"
                    rows={controls}
                    onPick={pickHit}
                  />
                ) : null}
                {evidence.length > 0 ? (
                  <Group
                    label="Evidence"
                    type="evidence"
                    rows={evidence}
                    onPick={pickHit}
                  />
                ) : null}
              </>
            )}
          </div>
        </div>
      ) : null}
    </div>
  );
}

function Group({
  label,
  type,
  rows,
  onPick,
}: {
  label: string;
  type: string;
  rows: SearchHit[];
  onPick: (hit: SearchHit) => void;
}) {
  return (
    <div
      data-testid={`citation-picker-group-${type}`}
      className="border-b border-border last:border-b-0"
    >
      <div className="px-3 pt-2 pb-1 text-[10px] uppercase tracking-wide text-muted-foreground">
        {label}
      </div>
      <ul className="pb-1">
        {rows.map((hit) => (
          <li key={hit.id}>
            <button
              type="button"
              data-testid={`citation-picker-row-${type}`}
              onClick={() => onPick(hit)}
              className="block w-full text-left px-3 py-1.5 text-xs hover:bg-accent/50"
            >
              <div className="font-medium truncate">{hit.title}</div>
              {hit.snippet && hit.snippet !== hit.title ? (
                <div className="text-[10px] text-muted-foreground truncate">
                  {hit.snippet}
                </div>
              ) : null}
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}

// CitationChips renders the attached-citation list below the textarea
// and emits an `onRemove(citation)` event when the operator clicks the
// chip's X. Stays a pure presentation component so the parent owns
// the actual PATCH-to-platform writeback.
export function CitationChips({
  citations,
  onRemove,
}: {
  citations: Citation[];
  onRemove: (c: Citation) => void;
}) {
  if (citations.length === 0) return null;
  return (
    <div data-testid="citation-chips" className="flex flex-wrap gap-2 mt-2">
      {citations.map((c) => (
        <span
          key={`${c.type}:${c.id}`}
          data-testid="citation-chip"
          className="inline-flex items-center gap-1 px-2 py-0.5 text-xs bg-muted/60 border border-border rounded-full"
        >
          <span className="font-mono text-[10px] uppercase text-muted-foreground">
            {c.type}
          </span>
          <span className="truncate max-w-48">{c.title}</span>
          <button
            type="button"
            data-testid="citation-chip-remove"
            onClick={() => onRemove(c)}
            aria-label={`Remove ${c.title}`}
            className="text-muted-foreground hover:text-destructive"
          >
            ×
          </button>
        </span>
      ))}
    </div>
  );
}
