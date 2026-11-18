// #2432 — an answer's citations: the picker that adds one and the chips
// that list them.
//
// The picker is the shared entity picker (`<EntityPicker>`) over the
// tenant's controls and evidence, in place of slice 263's own search
// palette. Evidence is searched server-side as the operator types, so a
// record older than the suggested newest ones can still be cited. Each pick adds one citation and clears the field for the
// next. It is list-only: a pasted ID it cannot find has no kind, so it
// cannot become a citation. When the server refuses the evidence list
// (a viewer, an auditor not assigned), controls still list and the field
// says evidence is not available.

"use client";

import { useState } from "react";

import { EntityPicker } from "@/components/ui/entity-picker";
import type { EntityKind, EntityOption } from "@/lib/entity-picker";

import type { Citation } from "./types";

const CITE_KINDS: readonly EntityKind[] = ["control", "evidence"];

/** The citation a picked option becomes; null for a kind it cannot cite. */
export function citationFromOption(option: EntityOption): Citation | null {
  if (option.kind === "control") {
    return { id: option.value, type: "controls", title: option.label };
  }
  if (option.kind === "evidence") {
    return { id: option.value, type: "evidence", title: option.label };
  }
  return null;
}

export function CitationPicker({
  id,
  onPick,
}: {
  id: string;
  onPick: (citation: Citation) => void;
}) {
  // A pick remounts the picker, so its input clears for the next one.
  // Focus goes back to the new input, where the keyboard user left it.
  const [generation, setGeneration] = useState(0);

  return (
    <div className="space-y-1">
      <label
        htmlFor={id}
        className="block text-xs font-medium text-muted-foreground"
      >
        Cite a control or evidence
      </label>
      <EntityPicker
        key={generation}
        kinds={CITE_KINDS}
        id={id}
        value=""
        onChange={() => {}}
        onPick={(option) => {
          const citation = citationFromOption(option);
          if (!citation) return;
          onPick(citation);
          setGeneration((g) => g + 1);
          requestAnimationFrame(() => document.getElementById(id)?.focus());
        }}
        listedOnly
        testId="citation-picker"
        errorId="citation-picker-error"
        placeholder="Search controls and evidence…"
      />
    </div>
  );
}

// CitationChips renders the attached-citation list and emits
// `onRemove(citation)` when the operator clicks a chip's X. The parent
// owns the PATCH that saves the change.
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
          <span className="text-[10px] uppercase text-muted-foreground">
            {c.type === "controls" ? "Control" : "Evidence"}
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
