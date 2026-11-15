// #2103 — the scope-dimension picker's model.
//
// Pure, so the predicate it emits and the predicates it can load back are
// covered in the node vitest tier without a DOM (slice 069 P0-A3);
// `scope-picker.tsx` renders it.
//
// The picker produces a predicate in the grammar `internal/scope.Evaluate`
// accepts (see the package doc in internal/scope/expr.go):
//   * Everything        -> {"op":"true"}, the canonical always-true spelling
//                          (internal/eval/exception.go matchesEveryCell);
//   * choose by dimension -> one clause per built-in dimension with values
//                          picked (`eq` for one value, `in` for several),
//                          combined as all-of (`and`) when there are two or
//                          more.
//
// `internal/scope/testdata/picker-predicates.json` is shared with a Go test
// that runs every predicate through scope.Evaluate, so the two cannot drift.

/** The built-in dimensions, in display order (internal/scope BuiltinDimensions). */
export const BUILTIN_DIMENSIONS = [
  "business_unit",
  "environment",
  "geography",
  "cloud_account",
  "data_classification",
  "product_line",
] as const;

export type BuiltinDimension = (typeof BUILTIN_DIMENSIONS)[number];

export const DIMENSION_LABELS: Record<BuiltinDimension, string> = {
  business_unit: "Business unit",
  environment: "Environment",
  geography: "Geography",
  cloud_account: "Cloud account",
  data_classification: "Data classification",
  product_line: "Product line",
};

export type PickerState =
  | { mode: "everything" }
  | {
      mode: "dimensions";
      /** Picked values per built-in dimension. A missing or empty list means "any". */
      selections: Partial<Record<BuiltinDimension, string[]>>;
    };

/** A predicate node in the internal/scope grammar. */
export type Predicate =
  | { op: "true" }
  | { op: "eq"; dim: string; value: string }
  | { op: "in"; dim: string; values: string[] }
  | { op: "and" | "or"; args: Predicate[] }
  | { op: "not"; arg: Predicate };

export const EVERYTHING: Predicate = { op: "true" };

/**
 * The predicate for a picker state, or null when "choose by dimension" has
 * no values picked yet (an incomplete choice, not a silent Everything).
 */
export function toPredicate(state: PickerState): Predicate | null {
  if (state.mode === "everything") return EVERYTHING;
  const clauses: Predicate[] = [];
  for (const dim of BUILTIN_DIMENSIONS) {
    const values = state.selections[dim] ?? [];
    if (values.length === 1) {
      clauses.push({ op: "eq", dim, value: values[0] });
    } else if (values.length > 1) {
      clauses.push({ op: "in", dim, values: [...values] });
    }
  }
  if (clauses.length === 0) return null;
  if (clauses.length === 1) return clauses[0];
  return { op: "and", args: clauses };
}

export type LoadResult =
  | { kind: "picker"; state: PickerState }
  | { kind: "advanced"; reason: string };

function isBuiltin(dim: unknown): dim is BuiltinDimension {
  return (
    typeof dim === "string" &&
    (BUILTIN_DIMENSIONS as readonly string[]).includes(dim)
  );
}

function isPlainObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

// One eq/in clause on a built-in dimension, as [dim, values]; otherwise a
// reason the picker cannot show it.
function clauseOf(
  node: unknown,
): { dim: BuiltinDimension; values: string[] } | { reason: string } {
  if (!isPlainObject(node)) return { reason: "is not a predicate object" };
  const { op, dim } = node;
  if (op !== "eq" && op !== "in") {
    return { reason: `uses "${String(op)}", which the picker cannot show` };
  }
  if (!isBuiltin(dim)) {
    return { reason: `filters on "${String(dim)}", not a built-in dimension` };
  }
  if (op === "eq") {
    if (typeof node.value !== "string")
      return { reason: "has an eq without a value" };
    return { dim, values: [node.value] };
  }
  const values = node.values;
  if (!Array.isArray(values) || values.some((v) => typeof v !== "string")) {
    return { reason: "has an in without a list of values" };
  }
  if (values.length === 0) {
    return { reason: "has an empty value list, which matches nothing" };
  }
  return { dim, values: values as string[] };
}

/**
 * Loads an existing predicate back into the picker, or reports it as
 * Advanced-only (any-of, negation, nesting, a custom dimension, a dimension
 * named twice, or an empty value list). The always-true spellings the
 * evaluator accepts (null, {}, {"op":"true"}) load as Everything.
 */
export function fromPredicate(predicate: unknown): LoadResult {
  if (
    predicate === null ||
    predicate === undefined ||
    (isPlainObject(predicate) && Object.keys(predicate).length === 0)
  ) {
    return { kind: "picker", state: { mode: "everything" } };
  }
  if (!isPlainObject(predicate)) {
    return { kind: "advanced", reason: "is not a predicate object" };
  }
  if (predicate.op === "true") {
    return { kind: "picker", state: { mode: "everything" } };
  }
  const nodes =
    predicate.op === "and" && Array.isArray(predicate.args)
      ? predicate.args
      : [predicate];
  if (nodes.length === 0) {
    return { kind: "advanced", reason: "is an empty all-of" };
  }
  const selections: Partial<Record<BuiltinDimension, string[]>> = {};
  for (const n of nodes) {
    const c = clauseOf(n);
    if ("reason" in c) return { kind: "advanced", reason: c.reason };
    if (selections[c.dim]) {
      return {
        kind: "advanced",
        reason: `names "${c.dim}" more than once`,
      };
    }
    selections[c.dim] = c.values;
  }
  return { kind: "picker", state: { mode: "dimensions", selections } };
}

/** A dimension row as `GET /v1/scopes/dimensions` returns it. */
export type ScopeDimensionWire = {
  name: string;
  value_type: string;
  allowed_values: string[] | null;
  is_required: boolean;
  is_builtin: boolean;
};

/**
 * The values offered for each built-in dimension: its allowed values when
 * the tenant declared them, otherwise the values its scope cells use,
 * plus any value already selected so a loaded predicate never hides one.
 */
export function dimensionOptions(
  dimensions: ScopeDimensionWire[],
  cells: { dimensions: Record<string, string> }[],
  selected: Partial<Record<BuiltinDimension, string[]>> = {},
): Record<BuiltinDimension, string[]> {
  const out = {} as Record<BuiltinDimension, string[]>;
  for (const dim of BUILTIN_DIMENSIONS) {
    const declared = dimensions.find((d) => d.name === dim);
    const allowed = Array.isArray(declared?.allowed_values)
      ? declared.allowed_values
      : [];
    const base =
      allowed.length > 0
        ? [...allowed]
        : [
            ...new Set(
              cells
                .map((c) => c.dimensions[dim])
                .filter((v): v is string => typeof v === "string" && v !== ""),
            ),
          ].sort();
    for (const v of selected[dim] ?? []) {
      if (!base.includes(v)) base.push(v);
    }
    out[dim] = base;
  }
  return out;
}

/**
 * Toggles one value of one dimension, keeping the picked values in option
 * order so the emitted predicate is stable whatever order they were clicked.
 */
export function toggleValue(
  selections: Partial<Record<BuiltinDimension, string[]>>,
  dim: BuiltinDimension,
  value: string,
  options: string[],
): Partial<Record<BuiltinDimension, string[]>> {
  const current = selections[dim] ?? [];
  const next = current.includes(value)
    ? current.filter((v) => v !== value)
    : [...current, value];
  const rank = (v: string) => {
    const i = options.indexOf(v);
    return i === -1 ? options.length : i;
  };
  next.sort((a, b) => rank(a) - rank(b));
  return { ...selections, [dim]: next };
}
