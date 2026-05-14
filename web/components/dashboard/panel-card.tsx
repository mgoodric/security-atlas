"use client";

// Slice 040 — shared panel chrome for the program dashboard.
//
// Every dashboard panel is a Card with a header (title + description +
// optional right-side action) and a body that renders one of three
// states from its own TanStack Query:
//   - loading  -> a Skeleton sized to the panel
//   - error    -> an inline destructive Alert with a Retry button
//   - data     -> the panel's content
//
// Each panel owns its own query, so a slow or failing endpoint degrades
// only that panel — the page never blocks on a single API (AC-7, and
// anti-criterion P0-2). This wrapper centralizes that contract so each
// panel file stays a thin data-to-markup mapping.

import type { ReactNode } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";

export type PanelState = {
  isLoading: boolean;
  isError: boolean;
  error: unknown;
  refetch: () => void;
};

export function PanelCard({
  title,
  description,
  action,
  state,
  skeletonClassName = "h-40 w-full",
  testid,
  children,
}: {
  title: string;
  description?: string;
  action?: ReactNode;
  state: PanelState;
  skeletonClassName?: string;
  testid: string;
  children: ReactNode;
}) {
  return (
    <Card data-testid={testid}>
      <CardHeader className="border-b">
        <CardTitle>{title}</CardTitle>
        {description ? <CardDescription>{description}</CardDescription> : null}
        {action ? <CardAction>{action}</CardAction> : null}
      </CardHeader>
      <CardContent>
        {state.isLoading ? (
          <Skeleton
            className={skeletonClassName}
            data-testid={`${testid}-loading`}
          />
        ) : state.isError ? (
          <Alert variant="destructive" data-testid={`${testid}-error`}>
            <AlertTitle>Could not load this panel</AlertTitle>
            <AlertDescription>
              {(state.error as Error)?.message ?? "Unknown error"}
              <div className="mt-2">
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => state.refetch()}
                  data-testid={`${testid}-retry`}
                >
                  Retry
                </Button>
              </div>
            </AlertDescription>
          </Alert>
        ) : (
          children
        )}
      </CardContent>
    </Card>
  );
}

// MissingEndpointPanel renders a panel whose backing endpoint does not
// exist on main yet. It names the missing endpoint explicitly and the
// follow-up backend slice it is tracked under, per the slice 041 / 060
// precedent. It never fabricates data (anti-criterion P0-1).
export function MissingEndpointPanel({
  title,
  description,
  endpoint,
  detail,
  testid,
  children,
}: {
  title: string;
  description?: string;
  endpoint: string;
  detail: string;
  testid: string;
  children?: ReactNode;
}) {
  return (
    <Card data-testid={testid}>
      <CardHeader className="border-b">
        <CardTitle>{title}</CardTitle>
        {description ? <CardDescription>{description}</CardDescription> : null}
      </CardHeader>
      <CardContent>
        <Alert data-testid={`${testid}-placeholder`}>
          <AlertTitle>Not yet wired</AlertTitle>
          <AlertDescription>
            This panel binds to <span className="font-mono">{endpoint}</span>,
            which does not exist on main yet. {detail} No data is shown until
            the endpoint ships — the dashboard never fabricates rows.
          </AlertDescription>
        </Alert>
        {children}
      </CardContent>
    </Card>
  );
}
