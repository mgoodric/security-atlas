"use client";

// #2385 — an evidence record's source, `<actor_type> · <actor>`, with the
// actor rendered through <Person>: a person or API key reads by name, a
// connector or service-account name as written. Same shape as
// `sourceSummary` (format.ts), which stays the plain-text form.

import { Person } from "@/components/ui/person";

export function SourceAttribution({
  source,
  fallbackActor,
}: {
  source: Record<string, unknown> | null | undefined;
  // Shown as the actor when the source names none (the credential that
  // pushed the record).
  fallbackActor?: string | null;
}) {
  const actorType =
    typeof source?.actor_type === "string" ? source.actor_type : "";
  const actorId = typeof source?.actor_id === "string" ? source.actor_id : "";
  const actor = actorId || (actorType ? "" : fallbackActor || "");

  if (!actorType && !actor) return <>—</>;
  return (
    <>
      {actorType}
      {actorType && actor ? " · " : null}
      {actor ? <Person id={actor} freeText /> : null}
    </>
  );
}
