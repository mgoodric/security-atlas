"use client";

// #2465 — the caller's own pending policy acknowledgments, above the
// policy list: how many published policies ask them to acknowledge and
// have not been acknowledged for this version within the year, each
// linking to its detail page where the Acknowledge button is. Renders
// nothing when the list is empty or cannot be read; the list itself does
// not depend on it.

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";

import { fetchMyAcknowledgments, MY_ACKS_QUERY_KEY } from "@/lib/api/policies";

export function MyAcksCallout() {
  const { data } = useQuery({
    queryKey: MY_ACKS_QUERY_KEY,
    queryFn: fetchMyAcknowledgments,
    retry: false,
  });
  const pending = data?.pending ?? [];
  if (pending.length === 0) return null;
  return (
    <div
      className="border-b px-5 py-2 text-sm"
      data-testid="policies-my-acks"
      role="status"
    >
      <span className="font-medium" data-testid="policies-my-acks-count">
        {pending.length === 1
          ? "1 policy waits for your acknowledgment:"
          : `${pending.length} policies wait for your acknowledgment:`}
      </span>{" "}
      {pending.map((p, i) => (
        <span key={p.policy_id}>
          {i > 0 ? ", " : null}
          <Link
            href={`/policies/${encodeURIComponent(p.policy_id)}`}
            className="text-primary hover:underline"
            data-testid="policies-my-acks-link"
          >
            {p.title}
          </Link>
        </span>
      ))}
    </div>
  );
}
