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

import { useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";

import {
  Combobox,
  ComboboxEmpty,
  ComboboxInput,
  ComboboxItem,
  ComboboxList,
  ComboboxPopup,
} from "@/components/ui/combobox";
import { usePeopleDirectory } from "@/components/ui/person";
import {
  credentialOptions,
  frameworkOptions,
  matchesQuery,
  parsePastedId,
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

function useEntityOptions(kinds: readonly EntityKind[]) {
  const wantsFrameworks = kinds.includes("framework");
  const wantsPeople = kinds.includes("user") || kinds.includes("credential");
  const frameworks = useQuery({
    queryKey: ["entity-picker", "framework-versions"],
    queryFn: fetchFrameworkVersions,
    staleTime: 60_000,
    enabled: wantsFrameworks,
  });
  const people = usePeopleDirectory();

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
    return out;
  }, [kinds, wantsFrameworks, wantsPeople, frameworks.data, people.data]);

  const isLoading =
    (wantsFrameworks && frameworks.isPending) ||
    (wantsPeople && people.isPending);
  // The people endpoint answers null (not an error) to a caller it refuses.
  const isError =
    (wantsFrameworks && frameworks.isError) ||
    (wantsPeople && !people.isPending && !people.data);
  return { options, isLoading, isError };
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
}) {
  const fetched = useEntityOptions(kinds);
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
    if (parsed.ok) {
      select(parsed.id);
      return;
    }
    // An invalid entry clears the selection so the form cannot submit
    // the previous ID behind text that no longer names it.
    select("");
    setPasteError(parsed.error);
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
