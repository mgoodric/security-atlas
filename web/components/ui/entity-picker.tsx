"use client";

// #2086 — the one searchable entity picker, for framework versions, users
// and API keys. It replaces every form field that asked for a UUID.
//
// The list comes from the endpoint each kind already has: framework
// versions from `/api/frameworks?include=versions`, users and API keys
// from the `<Person>` directory (`/api/people`, shared query cache). A
// power user can still paste an ID: it is checked on entry
// (`parsePastedId`), selected when valid, and an inline error names what
// was wrong when it is not. A valid ID the list does not hold (another
// tenant's user, say) is accepted as the ID itself.
//
// The value is the selected ID, "" for none. The picker reports a new
// value through onChange only; it never submits a form.
//
// #2087 adds controls (`/api/controls-list`), risks (`/api/risks`, with
// roll-ups) and policies (`/api/policies`), and `<EntityMultiPicker>`:
// the same lists as a searchable checkbox list with a count and an
// optional cap. A list the caller may not read (403, a role that cannot
// see it) or that its module hides (404, the module switched off) shows
// "not available" in place, never an error page. The picker is UI only:
// the server still checks link caps and tenant ownership.
//
// #2432 adds evidence for the questionnaire citation picker: the
// tenant's newest records (`/api/evidence`) as suggestions, and once two
// characters are typed, a debounced server-side search of the whole
// ledger (`/api/search?types=evidence`). Every record list is cached under
// the session's tenant, so one tenant's titles never show under another.
// When a picker lists several kinds and the server refuses one (a viewer
// asking for evidence), the others still list and the refused one is
// named "not available"; nothing from its list is shown.

import { useQuery } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";

import {
  Combobox,
  ComboboxEmpty,
  ComboboxInput,
  ComboboxItem,
  ComboboxList,
  ComboboxPopup,
} from "@/components/ui/combobox";
import { useSessionTenant } from "@/components/session-tenant-context";
import { usePeopleDirectory } from "@/components/ui/person";
import { APIError } from "@/lib/api/base";
import { fetchTenantControlsList } from "@/lib/api/controls-list";
import { fetchEvidenceList, searchEvidence } from "@/lib/api/evidence";
import { fetchPoliciesList } from "@/lib/api/policies";
import { fetchRisksListWithRollUps } from "@/lib/api/risks";
import {
  controlOptions,
  credentialOptions,
  evidenceOptions,
  evidenceSearchOptions,
  frameworkOptions,
  KIND_PLURAL,
  matchesQuery,
  notListedError,
  parsePastedId,
  policyOptions,
  riskOptions,
  toggleSelection,
  uniqueOptions,
  userOptions,
  type EntityKind,
  type EntityOption,
  type FrameworkWithVersions,
} from "@/lib/entity-picker";

async function fetchFrameworkVersions(): Promise<FrameworkWithVersions[]> {
  const res = await fetch("/api/frameworks?include=versions", {
    cache: "no-store",
  });
  if (!res.ok) throw new Error(`${res.status}`);
  const body = (await res.json()) as { frameworks?: FrameworkWithVersions[] };
  return body.frameworks ?? [];
}

// A 403 (the role cannot read the list) or 404 (its module is switched
// off) means the list is not offered, not that loading failed.
function isUnavailable(err: unknown): boolean {
  return err instanceof APIError && (err.status === 403 || err.status === 404);
}

// The newest evidence records the picker suggests before a search: the
// endpoint's page cap.
const EVIDENCE_LIMIT = 200;
// Typed text searches evidence server-side once it is this long (the
// search endpoint's minimum), after this pause.
const SEARCH_MIN_CHARS = 2;
const SEARCH_DEBOUNCE_MS = 250;

async function fetchEvidenceForPicker() {
  return (await fetchEvidenceList({ limit: EVIDENCE_LIMIT })).evidence;
}

// useDebounced returns `value` once it has stopped changing for `ms`.
function useDebounced(value: string, ms: number): string {
  const [settled, setSettled] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setSettled(value), ms);
    return () => clearTimeout(t);
  }, [value, ms]);
  return settled;
}

// The evidence list holds only the newest records, so once the caller
// types, the whole ledger is searched server-side (searchEvidence).
function useEntityOptions(
  kinds: readonly EntityKind[],
  module?: string,
  query = "",
) {
  const wantsFrameworks = kinds.includes("framework");
  const wantsPeople = kinds.includes("user") || kinds.includes("credential");
  const wantsControls = kinds.includes("control");
  const wantsRisks = kinds.includes("risk");
  const wantsPolicies = kinds.includes("policy");
  const wantsEvidence = kinds.includes("evidence");
  // Record lists are keyed by tenant and not read while it is unknown.
  const tenantId = useSessionTenant() ?? "";
  const frameworks = useQuery({
    queryKey: ["entity-picker", "framework-versions"],
    queryFn: fetchFrameworkVersions,
    staleTime: 60_000,
    enabled: wantsFrameworks,
  });
  const people = usePeopleDirectory(module);
  // Refused lists are not retried: a 403 or 404 will not change.
  const retry = (n: number, err: unknown) => !isUnavailable(err) && n < 2;
  const controls = useQuery({
    queryKey: ["entity-picker", "controls", tenantId],
    queryFn: fetchTenantControlsList,
    staleTime: 30_000,
    enabled: wantsControls && tenantId !== "",
    retry,
  });
  const risks = useQuery({
    queryKey: ["entity-picker", "risks", tenantId],
    queryFn: fetchRisksListWithRollUps,
    staleTime: 30_000,
    enabled: wantsRisks && tenantId !== "",
    retry,
  });
  const policies = useQuery({
    queryKey: ["entity-picker", "policies", tenantId],
    queryFn: fetchPoliciesList,
    staleTime: 30_000,
    enabled: wantsPolicies && tenantId !== "",
    retry,
  });
  const evidence = useQuery({
    queryKey: ["entity-picker", "evidence", tenantId],
    queryFn: fetchEvidenceForPicker,
    staleTime: 30_000,
    enabled: wantsEvidence && tenantId !== "",
    retry,
  });
  const searchFor = useDebounced(query.trim(), SEARCH_DEBOUNCE_MS);
  const evidenceSearch = useQuery({
    queryKey: ["entity-picker", "evidence-search", tenantId, searchFor],
    queryFn: () => searchEvidence(searchFor),
    staleTime: 30_000,
    enabled:
      wantsEvidence &&
      tenantId !== "" &&
      searchFor.length >= SEARCH_MIN_CHARS &&
      // A refused list is not searched either.
      !isUnavailable(evidence.error),
    retry,
  });

  const options = useMemo(() => {
    const out: EntityOption[] = [];
    if (wantsFrameworks) out.push(...frameworkOptions(frameworks.data ?? []));
    if (wantsPeople && people.data) {
      const users = [...people.data.users.values()];
      if (kinds.includes("user")) out.push(...userOptions(users));
      if (kinds.includes("credential")) {
        out.push(
          ...credentialOptions([...people.data.apiKeys.values()], users),
        );
      }
    }
    if (wantsControls) out.push(...controlOptions(controls.data ?? []));
    if (wantsRisks) out.push(...riskOptions(risks.data?.risks ?? []));
    if (wantsPolicies) {
      out.push(...policyOptions(policies.data?.policies ?? []));
    }
    if (wantsEvidence) {
      out.push(...evidenceOptions(evidence.data ?? []));
      out.push(
        ...evidenceSearchOptions(
          evidenceSearch.data ?? [],
          evidenceSearch.data ? searchFor : "",
        ),
      );
    }
    return uniqueOptions(out);
  }, [
    kinds,
    wantsFrameworks,
    wantsPeople,
    wantsControls,
    wantsRisks,
    wantsPolicies,
    wantsEvidence,
    frameworks.data,
    people.data,
    controls.data,
    risks.data,
    policies.data,
    evidence.data,
    evidenceSearch.data,
    searchFor,
  ]);

  // Evidence is refused when either its list or its search is.
  const evidenceError =
    [evidence.error, evidenceSearch.error].find(isUnavailable) ??
    evidence.error ??
    evidenceSearch.error;
  const records = (
    [
      ["control", wantsControls, controls.isPending, controls.error],
      ["risk", wantsRisks, risks.isPending, risks.error],
      ["policy", wantsPolicies, policies.isPending, policies.error],
      ["evidence", wantsEvidence, evidence.isPending, evidenceError],
    ] as const
  ).filter(([, wanted]) => wanted);
  const isLoading =
    (wantsFrameworks && frameworks.isPending) ||
    (wantsPeople && people.isPending) ||
    records.some(([, , pending]) => pending);
  // The kinds the server refused (403 or 404). Decided from the answer,
  // never from the caller's role as the browser sees it.
  const unavailableKinds: EntityKind[] = records
    .filter(([, , , error]) => isUnavailable(error))
    .map(([kind]) => kind);
  const unavailable =
    records.length > 0 && unavailableKinds.length === records.length;
  // The people endpoint answers null (not an error) to a caller it refuses.
  const isError =
    !unavailable &&
    ((wantsFrameworks && frameworks.isError) ||
      (wantsPeople && !people.isPending && !people.data) ||
      records.some(([, , , error]) => error && !isUnavailable(error)));
  // A search is due (still debouncing) or on its way.
  const isSearching =
    wantsEvidence &&
    !unavailable &&
    query.trim().length >= SEARCH_MIN_CHARS &&
    (searchFor !== query.trim() || evidenceSearch.isFetching);
  return {
    options,
    isLoading,
    isError,
    unavailable,
    unavailableKinds,
    isSearching,
  };
}

export function EntityPicker({
  kinds,
  id,
  value,
  onChange,
  testId,
  error,
  errorId = `${id}-error`,
  placeholder,
  extraOptions,
  describedBy,
  module,
  onPick,
  listedOnly = false,
}: {
  // What the picker lists and which pasted IDs it accepts.
  kinds: readonly EntityKind[];
  // The input's id, for a <label htmlFor>.
  id: string;
  value: string;
  onChange: (id: string) => void;
  testId: string;
  // An error the form owns (a required field left empty).
  error?: string;
  errorId?: string;
  placeholder?: string;
  // Options listed ahead of the fetched ones (Activity's "You").
  extraOptions?: readonly EntityOption[];
  // Help text the input also points at.
  describedBy?: string;
  // #2174: the optional module the record belongs to. A people picker
  // then lists only the members who can work in it (the platform
  // filters `/v1/people?module=`).
  module?: string;
  // #2432: called with the listed option each time one is picked, for a
  // caller that needs more than its ID (the citation's kind and title).
  onPick?: (option: EntityOption) => void;
  // Accept only listed options: a pasted ID the list does not hold is
  // an inline error rather than a selection.
  listedOnly?: boolean;
}) {
  // What the caller has typed, which an evidence picker searches for.
  const [typed, setTyped] = useState("");
  const fetched = useEntityOptions(kinds, module, typed);
  const options = useMemo(
    () => [...(extraOptions ?? []), ...fetched.options],
    [extraOptions, fetched.options],
  );

  // The selected option. A valid pasted ID the list does not hold is
  // shown as the ID itself.
  const selected = useMemo<EntityOption | null>(() => {
    if (!value) return null;
    return options.find((o) => o.value === value) ?? { value, label: value };
  }, [options, value]);
  const items = useMemo(
    () =>
      selected && !options.some((o) => o.value === selected.value)
        ? [...options, selected]
        : options,
    [options, selected],
  );

  const [input, setInput] = useState(selected?.label ?? "");
  const [pasteError, setPasteError] = useState<string | null>(null);
  // Show the selection's label when it changes or its label arrives.
  // (State adjusted during render, not in an effect: react.dev "you might
  // not need an effect".)
  const selectedLabel = selected?.label ?? "";
  const [shownLabel, setShownLabel] = useState(selectedLabel);
  if (shownLabel !== selectedLabel) {
    setShownLabel(selectedLabel);
    setInput(selectedLabel);
  }

  function select(next: string) {
    setPasteError(null);
    const picked = next ? options.find((o) => o.value === next) : undefined;
    if (picked) onPick?.(picked);
    if (next !== value) onChange(next);
  }

  // commit resolves typed or pasted text to a selection: an option's
  // label or ID, else a valid pasted ID, else an inline error.
  function commit(text: string) {
    const t = text.trim();
    if (t === "") {
      select("");
      return;
    }
    if (selected && t === selected.label) return;
    const lower = t.toLowerCase();
    const match = options.find(
      (o) => o.label.toLowerCase() === lower || o.value === lower,
    );
    if (match) {
      select(match.value);
      setInput(match.label);
      return;
    }
    const parsed = parsePastedId(kinds, t);
    if (parsed.ok && !listedOnly) {
      select(parsed.id);
      return;
    }
    // An invalid entry clears the selection so the form cannot submit
    // the previous ID behind text that no longer names it.
    select("");
    setPasteError(parsed.ok ? notListedError(kinds, t) : parsed.error);
  }

  const shownError = pasteError ?? error;
  const describedByIds =
    [shownError ? errorId : null, describedBy].filter(Boolean).join(" ") ||
    undefined;

  return (
    <div data-testid={`${testId}-picker`}>
      <Combobox
        items={items}
        value={selected}
        onValueChange={(next: EntityOption | null) => select(next?.value ?? "")}
        inputValue={input}
        onInputValueChange={(next: string) => {
          setInput(next);
          setTyped(next);
          setPasteError(null);
        }}
        filter={(item: EntityOption, query: string) =>
          // The input shows the selection's label; list everything then.
          (selected !== null && query === selected.label) ||
          matchesQuery(item, query)
        }
        isItemEqualToValue={(a: EntityOption, b: EntityOption) =>
          a.value === b.value
        }
        itemToStringLabel={(o: EntityOption) => o.label}
        autoHighlight
      >
        <ComboboxInput
          id={id}
          data-testid={testId}
          placeholder={placeholder ?? "Search by name, or paste an ID"}
          spellCheck={false}
          autoComplete="off"
          aria-invalid={shownError ? true : undefined}
          aria-describedby={describedByIds}
          onPaste={(e) => {
            const text = e.clipboardData.getData("text");
            // A pasted search term filters; a pasted ID, or text that
            // matches nothing, is committed and checked now.
            if (
              parsePastedId(kinds, text).ok ||
              !options.some((o) => matchesQuery(o, text))
            ) {
              e.preventDefault();
              setInput(text.trim());
              commit(text);
            }
          }}
          onKeyDown={(e) => {
            if (e.key !== "Enter") return;
            if (!options.some((o) => matchesQuery(o, input))) {
              e.preventDefault();
              commit(input);
            }
          }}
          onBlur={() => commit(input)}
        />
        <ComboboxPopup>
          <ComboboxEmpty>
            {fetched.isLoading
              ? "Loading…"
              : fetched.isSearching
                ? "Searching…"
                : fetched.unavailable
                  ? unavailableText(kinds)
                  : fetched.isError
                    ? "The list could not be loaded. Paste an ID instead."
                    : "No matches. Paste an ID to use one not listed."}
          </ComboboxEmpty>
          <ComboboxList>
            {(o: EntityOption) => (
              <ComboboxItem
                key={o.value}
                value={o}
                data-testid={`${testId}-option`}
              >
                <span>{o.label}</span>
                {o.hint ? (
                  <span className="text-xs text-muted-foreground">
                    {o.hint}
                  </span>
                ) : null}
              </ComboboxItem>
            )}
          </ComboboxList>
        </ComboboxPopup>
      </Combobox>
      <PartlyUnavailable fetched={fetched} testId={testId} />
      {shownError ? (
        <p
          id={errorId}
          className="mt-1 text-sm text-destructive"
          data-testid={errorId}
          aria-live="polite"
        >
          {shownError}
        </p>
      ) : null}
    </div>
  );
}

function unavailableText(kinds: readonly EntityKind[]): string {
  const noun = kinds.map((k) => KIND_PLURAL[k]).join(" and ");
  // "Evidence" is a mass noun: "Evidence is", but "Controls are".
  const verb = kinds.length === 1 && kinds[0] === "evidence" ? "is" : "are";
  return `${noun.charAt(0).toUpperCase()}${noun.slice(
    1,
  )} ${verb} not available to you.`;
}

// PartlyUnavailable names the kinds the server refused when the picker
// still lists others. A picker refused everything says so in its list.
function PartlyUnavailable({
  fetched,
  testId,
}: {
  fetched: { unavailable: boolean; unavailableKinds: readonly EntityKind[] };
  testId: string;
}) {
  if (fetched.unavailable || fetched.unavailableKinds.length === 0) {
    return null;
  }
  return (
    <p
      className="mt-1 text-xs text-muted-foreground"
      data-testid={`${testId}-unavailable`}
    >
      {unavailableText(fetched.unavailableKinds)}
    </p>
  );
}

// EntityMultiPicker picks several records of one or more kinds: a search
// box over a checkbox list, a count ("2 selected", and "(2/50)" in the
// legend when there is a cap) and a "Clear selection" link. At the cap
// the unchecked rows are disabled. The value is the selected IDs in the
// order they were picked.
//
// Test ids hang off `testId`: the fieldset itself, `-search`, `-list`,
// `-option-<id>` (the row), `-checkbox-<id>`, `-summary`, `-clear`,
// `-cap`, `-loading`, `-empty`, `-error`, `-unavailable`.
export function EntityMultiPicker({
  kinds,
  value,
  onChange,
  legend,
  testId,
  max,
  required,
  description,
  searchPlaceholder,
  emptyHint,
  error,
  errorId = `${testId}-required-error`,
}: {
  kinds: readonly EntityKind[];
  value: readonly string[];
  onChange: (ids: string[]) => void;
  legend: string;
  testId: string;
  // The most the form accepts; the server enforces it too.
  max?: number;
  required?: boolean;
  // Help text under the legend.
  description?: React.ReactNode;
  searchPlaceholder?: string;
  // What to say when the tenant has none of these yet.
  emptyHint: React.ReactNode;
  // An error the form owns (a required selection left empty).
  error?: string;
  errorId?: string;
}) {
  const [filter, setFilter] = useState("");
  const fetched = useEntityOptions(kinds, undefined, filter);
  const filtered = useMemo(
    () => fetched.options.filter((o) => matchesQuery(o, filter)),
    [fetched.options, filter],
  );
  const atCap = max !== undefined && value.length >= max;
  const total = fetched.options.length;
  const placeholder =
    searchPlaceholder ??
    `Search ${kinds.map((k) => KIND_PLURAL[k]).join(" and ")}…`;
  const searchId = `${testId}-search`;

  return (
    <fieldset
      className="min-w-0 space-y-2 rounded-md border border-input p-3"
      data-testid={testId}
      data-entity-picker={kinds.join(",")}
      aria-describedby={error ? errorId : undefined}
    >
      <legend className="px-1 text-sm font-medium text-foreground">
        {legend}
        {required ? <span className="text-destructive"> *</span> : null}
        {max !== undefined ? (
          <span className="text-xs text-muted-foreground">
            {" "}
            ({value.length}/{max})
          </span>
        ) : null}
      </legend>
      {description ? (
        <p className="text-xs text-muted-foreground">{description}</p>
      ) : null}

      <input
        id={searchId}
        type="text"
        value={filter}
        onChange={(e) => setFilter(e.target.value)}
        placeholder={placeholder}
        aria-label={placeholder}
        spellCheck={false}
        autoComplete="off"
        className="h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
        data-testid={searchId}
      />

      <div className="flex items-center justify-between text-xs text-muted-foreground">
        <span data-testid={`${testId}-summary`} aria-live="polite">
          {value.length} selected
          {filter
            ? ` · ${filtered.length} of ${total} visible`
            : ` · ${total} total`}
        </span>
        {value.length > 0 ? (
          <button
            type="button"
            onClick={() => onChange([])}
            className="text-xs underline hover:text-foreground"
            data-testid={`${testId}-clear`}
          >
            Clear selection
          </button>
        ) : null}
      </div>

      <PartlyUnavailable fetched={fetched} testId={testId} />
      {fetched.isLoading ? (
        <p
          className="text-sm text-muted-foreground"
          data-testid={`${testId}-loading`}
        >
          Loading…
        </p>
      ) : fetched.unavailable ? (
        <p
          className="text-sm text-muted-foreground"
          data-testid={`${testId}-unavailable`}
        >
          {unavailableText(kinds)}
        </p>
      ) : fetched.isError ? (
        <p className="text-sm text-destructive" data-testid={`${testId}-error`}>
          The list could not be loaded. Reload the page to try again.
        </p>
      ) : total === 0 ? (
        <p
          className="rounded-md border border-input bg-muted/40 p-3 text-sm text-muted-foreground"
          data-testid={`${testId}-empty`}
        >
          {emptyHint}
        </p>
      ) : (
        <ul
          className="max-h-64 overflow-y-auto rounded-md border border-input"
          data-testid={`${testId}-list`}
        >
          {filtered.map((o) => {
            const checked = value.includes(o.value);
            const disabled = !checked && atCap;
            return (
              <li
                key={o.value}
                className="border-b border-input/60 last:border-b-0"
              >
                <label
                  className={
                    "flex min-w-0 items-start gap-3 px-3 py-2 text-sm " +
                    (disabled
                      ? "opacity-50"
                      : "cursor-pointer hover:bg-accent/40") +
                    (checked ? " bg-accent/30" : "")
                  }
                  data-testid={`${testId}-option-${o.value}`}
                >
                  <input
                    type="checkbox"
                    checked={checked}
                    disabled={disabled}
                    onChange={() =>
                      onChange(toggleSelection(value, o.value, max))
                    }
                    className="mt-1"
                    data-testid={`${testId}-checkbox-${o.value}`}
                  />
                  <span className="flex min-w-0 flex-col gap-0.5">
                    <span className="truncate font-medium text-foreground">
                      {o.label}
                    </span>
                    {o.hint ? (
                      <span className="text-xs text-muted-foreground">
                        {o.hint}
                      </span>
                    ) : null}
                  </span>
                </label>
              </li>
            );
          })}
          {filtered.length === 0 ? (
            <li className="p-3 text-sm text-muted-foreground">
              No matches for your search.
            </li>
          ) : null}
        </ul>
      )}

      {atCap ? (
        <p className="text-xs text-amber-600" data-testid={`${testId}-cap`}>
          Maximum of {max} reached.
        </p>
      ) : null}
      {error ? (
        <p
          id={errorId}
          className="text-sm text-destructive"
          data-testid={errorId}
        >
          {error}
        </p>
      ) : null}
    </fieldset>
  );
}
