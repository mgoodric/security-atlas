"use client";

// #2399 — drops everything the browser cached for the previous session
// when the login page mounts. Signing in is a server action that
// redirects in place, so the tab's QueryClient (and the people directory
// <Person> resolves names against, lib/people-cache.ts) would otherwise
// survive a session expiry and a sign-in as someone else, or into
// another tenant. Clearing the whole client is a superset of
// forgetPeopleDirectory(): no query cached before this page answers the
// next session, whether or not its key carries a tenant.

import { useEffect } from "react";

import { getQueryClient } from "@/lib/queryClient";

export function ForgetPreviousSession() {
  useEffect(() => {
    getQueryClient().clear();
  }, []);
  return null;
}
