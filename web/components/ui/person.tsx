"use client";

// #2085 — renders an actor reference (a user ID, a `user:` subject or a
// credential ID) as a person, through the one resolver
// (`resolvePerson`, `lib/person.ts`).
//
// The tenant's directory is fetched once per page visit through
// `/api/people` and shared by every <Person> on the page via the query
// cache, which is dropped on a tenant switch or sign-out. A caller
// the endpoint refuses, or a failed fetch, resolves every reference to
// "Unknown"; the ID stays in the tooltip and never becomes visible text.
// Names render as React text only.

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";

import { useSessionTenant } from "@/components/session-tenant-context";
import {
  PEOPLE_STALE_MS,
  forgetPeopleOnTenantSwitch,
  peopleDirectoryQuery,
} from "@/lib/people-cache";
import {
  buildDirectory,
  isActorReference,
  resolvePerson,
  typedName,
  type DirectoryApiKey,
  type DirectoryUser,
  type PersonDirectory,
} from "@/lib/person";

interface PeopleResponse {
  users?: DirectoryUser[];
  api_keys?: DirectoryApiKey[];
}

async function fetchPeopleDirectory(
  module?: string,
): Promise<PersonDirectory | null> {
  const qs = module ? `?module=${encodeURIComponent(module)}` : "";
  const res = await fetch(`/api/people${qs}`, { cache: "no-store" });
  if (!res.ok) return null;
  const body = (await res.json()) as PeopleResponse;
  return buildDirectory(body.users ?? [], body.api_keys ?? []);
}

/**
 * The tenant's people directory. With `module` (#2174), only the members
 * who can work in that optional module: the platform filters, so an
 * assignee picker never offers someone who could not open the record.
 * The module-filtered list is cached under its own key, below the shared
 * one, so a tenant switch drops it too.
 *
 * #2399: every entry is keyed by the session's tenant; with the tenant
 * unknown nothing is read, and the directory stays pending.
 */
export function usePeopleDirectory(module?: string) {
  const qc = useQueryClient();
  const tenantId = useSessionTenant();
  // A tenant switch in any tab drops the directory (lib/people-cache.ts).
  useEffect(() => forgetPeopleOnTenantSwitch(qc), [qc]);
  return useQuery({
    ...peopleDirectoryQuery(tenantId, module),
    queryFn: () => fetchPeopleDirectory(module),
    staleTime: PEOPLE_STALE_MS,
    retry: false,
  });
}

export function Person({
  id,
  avatar = false,
  freeText = false,
  directory,
  className,
  testId,
}: {
  id: string | null | undefined;
  // Show the initials badge beside a resolved user's name.
  avatar?: boolean;
  // #2385: the field may hold a typed name instead of a reference (a
  // board pack's `published_by`, an evidence actor). A value that is not
  // reference-shaped (isActorReference) shows as written.
  freeText?: boolean;
  // #2385: resolve against this directory instead of the tenant's. For a
  // page whose people span tenants (admin super-admins), which
  // `/v1/people` cannot list by design.
  directory?: PersonDirectory;
  className?: string;
  testId?: string;
}) {
  const shared = usePeopleDirectory();
  const data = directory ?? shared.data;
  const isPending = directory ? false : shared.isPending;

  if (!id) {
    return (
      <span className={className} data-testid={testId}>
        —
      </span>
    );
  }
  if (freeText && !isActorReference(id)) {
    // #2684: italics + tooltip mark a typed name apart from a resolved
    // user, so it cannot pass for a verified identity.
    const typed = typedName(id);
    return (
      <span
        className={"italic" + (className ? ` ${className}` : "")}
        title={typed.title}
        data-testid={testId}
        data-person-kind="text"
      >
        {typed.text}
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
