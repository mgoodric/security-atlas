"use client";

// #2103 — the scope-dimension picker. Produces a scope predicate:
// Everything (always true), or values picked per built-in dimension,
// combined as all-of. The resulting predicate is shown read-only.
//
// Used by the exception request form and the framework-scope wizard. Pass
// `readOnly` for a record whose predicate is frozen (an activated or
// superseded framework-scope revision, #2227).
//
// Dimensions and their values come from the tenant's own endpoints
// (/api/scope-dimensions, /api/scope-cells) under the caller's bearer.
// The predicate logic lives in `scope-picker-model.ts`.

import { useQuery } from "@tanstack/react-query";
import { useId, useState } from "react";

import { Checkbox } from "@/components/ui/checkbox";
import { fetchScopeCells } from "@/lib/api/controls-list";
import { fetchScopeDimensions } from "@/lib/api/scope-dimensions";

import {
  BUILTIN_DIMENSIONS,
  DIMENSION_LABELS,
  dimensionOptions,
  fromPredicate,
  predicateKey,
  resyncFromValue,
  toPredicate,
  toggleValue,
  type LoadResult,
  type PickerState,
  type Predicate,
} from "./scope-picker-model";

function stateOf(loaded: LoadResult): PickerState {
  return loaded.kind === "picker" ? loaded.state : { mode: "everything" };
}

export function ScopePicker({
  value,
  onChange,
  readOnly = false,
}: {
  /**
   * The predicate to show; null or undefined shows Everything. When it
   * changes (say, the record loads after the picker mounted) the picker
   * re-derives from it, except when it is the picker's own last emission.
   */
  value?: unknown;
  /**
   * Called with the new predicate, or with null while "choose by
   * dimension" has no value picked yet. Null means incomplete: block
   * submit and never persist it, because the backend treats a stored null
   * predicate as matching every cell (internal/scope isEmptyExpr).
   */
  onChange?: (predicate: Predicate | null) => void;
  readOnly?: boolean;
}) {
  const [loaded, setLoaded] = useState(() => fromPredicate(value));
  const [state, setState] = useState<PickerState>(() => stateOf(loaded));
  const [valueKey, setValueKey] = useState(() => predicateKey(value));
  const [emittedKey, setEmittedKey] = useState<string | null>(null);
  const groupId = useId();

  // Adjust state during render when `value` changes (React's documented
  // alternative to an effect for deriving state from props).
  const resync = resyncFromValue(valueKey, emittedKey, value);
  if (resync) {
    setValueKey(resync.valueKey);
    if (resync.loaded) {
      setLoaded(resync.loaded);
      setState(stateOf(resync.loaded));
    }
  }

  const dims = useQuery({
    queryKey: ["scope-dimensions"],
    queryFn: fetchScopeDimensions,
  });
  const cells = useQuery({
    queryKey: ["scope-cells"],
    queryFn: fetchScopeCells,
  });

  if (loaded.kind === "advanced") {
    return (
      <div className="space-y-2" data-testid="scope-picker-advanced">
        <p className="text-sm">
          This scope is Advanced-only (it {loaded.reason}). The picker shows
          Everything or values chosen per dimension, so this one is shown as
          written.
        </p>
        <PredicateView predicate={value} />
      </div>
    );
  }

  const update = (next: PickerState) => {
    const p = toPredicate(next);
    setState(next);
    setEmittedKey(predicateKey(p));
    onChange?.(p);
  };

  const selections = state.mode === "dimensions" ? state.selections : {};
  const options = dimensionOptions(
    dims.data?.dimensions ?? [],
    cells.data?.cells ?? [],
    selections,
  );
  const predicate = toPredicate(state);

  return (
    <div className="space-y-4" data-testid="scope-picker">
      <fieldset className="space-y-2" disabled={readOnly}>
        <legend className="text-sm font-medium">
          Which scope cells does this cover?
        </legend>
        <label className="flex items-center gap-2 text-sm">
          <input
            type="radio"
            name={`${groupId}-mode`}
            checked={state.mode === "everything"}
            onChange={() => update({ mode: "everything" })}
            data-testid="scope-picker-everything"
          />
          Everything
        </label>
        <label className="flex items-center gap-2 text-sm">
          <input
            type="radio"
            name={`${groupId}-mode`}
            checked={state.mode === "dimensions"}
            onChange={() => update({ mode: "dimensions", selections: {} })}
            data-testid="scope-picker-dimensions"
          />
          Choose by dimension
        </label>
      </fieldset>

      {state.mode === "dimensions" && (
        <div className="space-y-3">
          {dims.isPending || cells.isPending ? (
            <p className="text-sm text-muted-foreground">Loading dimensions…</p>
          ) : dims.isError || cells.isError ? (
            <p className="text-sm text-destructive" role="alert">
              Could not load scope dimensions:{" "}
              {(dims.error ?? cells.error)?.message}
            </p>
          ) : null}
          {BUILTIN_DIMENSIONS.map((dim) => (
            <fieldset
              key={dim}
              className="space-y-1"
              data-testid={`scope-picker-dim-${dim}`}
            >
              <legend className="text-sm font-medium">
                {DIMENSION_LABELS[dim]}
              </legend>
              {options[dim].length === 0 ? (
                <p className="text-xs text-muted-foreground">
                  No values in use. Any value matches.
                </p>
              ) : (
                <div className="flex flex-wrap gap-x-4 gap-y-1">
                  {options[dim].map((v) => (
                    <label
                      key={v}
                      className="flex items-center gap-1.5 text-sm"
                    >
                      <Checkbox
                        checked={(selections[dim] ?? []).includes(v)}
                        disabled={readOnly}
                        onCheckedChange={() =>
                          update({
                            mode: "dimensions",
                            selections: toggleValue(
                              selections,
                              dim,
                              v,
                              options[dim],
                            ),
                          })
                        }
                        data-testid={`scope-picker-value-${dim}-${v}`}
                      />
                      {v}
                    </label>
                  ))}
                </div>
              )}
            </fieldset>
          ))}
          {predicate === null && !readOnly && (
            <p className="text-xs text-muted-foreground">
              Pick at least one value, or choose Everything.
            </p>
          )}
        </div>
      )}

      {predicate !== null && <PredicateView predicate={predicate} />}
    </div>
  );
}

function PredicateView({ predicate }: { predicate: unknown }) {
  return (
    <div className="space-y-1">
      <p className="text-xs font-medium text-muted-foreground">Predicate</p>
      <pre
        className="max-h-48 overflow-auto rounded border bg-muted p-2 font-mono text-xs"
        data-testid="scope-picker-predicate"
      >
        {JSON.stringify(predicate, null, 2)}
      </pre>
    </div>
  );
}
