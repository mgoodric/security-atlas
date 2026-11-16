"use client";

// #2085 — the Sign out form. Signing out is a server action that clears
// the session cookie and redirects in place, so the browser QueryClient
// survives it; the people directory is dropped on submit so the next
// session never resolves names against this one's tenant.

import type { ReactNode } from "react";

import { forgetPeopleDirectory } from "@/lib/people-cache";
import { getQueryClient } from "@/lib/queryClient";

export function SignOutForm({
  action,
  children,
}: {
  action: () => Promise<void>;
  children: ReactNode;
}) {
  return (
    <form
      action={action}
      onSubmit={() => forgetPeopleDirectory(getQueryClient())}
    >
      {children}
    </form>
  );
}
