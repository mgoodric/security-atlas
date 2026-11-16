"use client";

// #2145 — "Show onboarding hints again": clears the user's dismissed
// first-visit hints (components/help/onboarding-hint.tsx) from their
// server-side preferences, so each module's "New to <module>?" line
// returns on every browser.

import { useMutation, useQuery } from "@tanstack/react-query";

import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { useToast } from "@/components/ui/toast";

import { getMe, resetOnboardingHints } from "@/lib/api/me";

import { isCredentialBearer } from "../credential-bearer";

// The profile query key the Profile and Appearance tabs use, so the three
// share one GET /v1/me.
const PROFILE_QUERY_KEY = ["settings-me-profile"] as const;

export function OnboardingSection() {
  const { toast } = useToast();
  const profileQuery = useQuery({
    queryKey: PROFILE_QUERY_KEY,
    queryFn: getMe,
  });
  const reset = useMutation({
    mutationFn: resetOnboardingHints,
    onSuccess: () => toast("Onboarding hints will show again."),
    onError: () => toast("Couldn't reset the onboarding hints."),
  });

  // An API key or bootstrap credential has no user, so no hints to reset.
  if (isCredentialBearer(profileQuery.data)) return null;

  return (
    <Card id="onboarding" data-testid="settings-section-onboarding">
      <CardHeader>
        <CardTitle>Onboarding hints</CardTitle>
        <CardDescription>
          The first time you open a module, a line at the top links to how it
          works. Dismissed hints stay dismissed on every browser.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <Button
          type="button"
          variant="outline"
          data-testid="settings-onboarding-reset"
          disabled={reset.isPending}
          onClick={() => reset.mutate()}
        >
          Show onboarding hints again
        </Button>
      </CardContent>
    </Card>
  );
}
