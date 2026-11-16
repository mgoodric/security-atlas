"use client";

// #2085 — renders an actor reference (a user ID, a `user:` subject or a
// credential ID) as a person, through the one resolver
// (`resolvePerson`, `lib/person.ts`).
//
// The tenant's directory is fetched once per page through `/api/people`
// and shared by every <Person> on the page via the query cache. A caller
// the endpoint refuses, or a failed fetch, resolves every reference to
// "Unknown"; the ID stays in the tooltip and never becomes visible text.
// Names render as React text only.

import { useQuery } from "@tanstack/react-query";

import {
  buildDirectory,
  resolvePerson,
  type DirectoryApiKey,
  type DirectoryUser,
  type PersonDirectory,
} from "@/lib/person";

interface PeopleResponse {
  users?: DirectoryUser[];
  api_keys?: DirectoryApiKey[];
}

export const PEOPLE_QUERY_KEY = ["people"] as const;

async function fetchPeopleDirectory(): Promise<PersonDirectory | null> {
  const res = await fetch("/api/people", { cache: "no-store" });
  if (!res.ok) return null;
  const body = (await res.json()) as PeopleResponse;
  return buildDirectory(body.users ?? [], body.api_keys ?? []);
}

export function usePeopleDirectory() {
  return useQuery({
    queryKey: PEOPLE_QUERY_KEY,
    queryFn: fetchPeopleDirectory,
    // One fetch per page render; names do not change under a viewer.
    staleTime: Infinity,
    retry: false,
  });
}

export function Person({
  id,
  avatar = false,
  className,
  testId,
}: {
  id: string | null | undefined;
  // Show the initials badge beside a resolved user's name.
  avatar?: boolean;
  className?: string;
  testId?: string;
}) {
  const { data, isPending } = usePeopleDirectory();

  if (!id) {
    return (
      <span className={className} data-testid={testId}>
        —
      </span>
    );
  }
  if (isPending) {
    return (
      <span
        className={className}
        data-testid={testId}
        data-person-kind="loading"
        aria-busy="true"
      >
        …
      </span>
    );
  }

  const p = resolvePerson(id, data);
  return (
    <span
      className={
        "inline-flex items-center gap-1.5" + (className ? ` ${className}` : "")
      }
      title={p.title}
      data-testid={testId}
      data-person-kind={p.kind}
    >
      {avatar && p.initials ? (
        <span
          aria-hidden="true"
          className="inline-flex size-5 shrink-0 items-center justify-center rounded-full bg-muted text-[10px] font-medium text-foreground"
        >
          {p.initials}
        </span>
      ) : null}
      <span>{p.text}</span>
    </span>
  );
}
