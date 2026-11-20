"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useRef, useState } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  ConfirmInline,
  useFocusAfterRemoval,
  useTriggerRefs,
} from "@/components/ui/row-actions";
import { Skeleton } from "@/components/ui/skeleton";

import { listMySessions, MeSession, revokeMySession } from "@/lib/api/me";

import { sessionLine } from "../session-line";

// --- Section 5: Active sessions ------------------------------------------

// Slice 108: sessions section is server-backed via GET /v1/me/sessions +
// DELETE /v1/me/sessions/{id}. The "current" flag depends on the atlas_session
// cookie reaching the platform; bearer-only requests (no cookie) leave every
// row unflagged — surfaced via an explanatory tooltip rather than a banner so
// the section UI matches the design.

export function SessionsSection() {
  const qc = useQueryClient();
  // #2280: Revoke signs another device out at once and can't be undone
  // from there, so it asks first.
  const [confirming, setConfirming] = useState<string | null>(null);
  const revokeTriggers = useTriggerRefs();
  const sessionsQuery = useQuery({
    queryKey: ["settings-me-sessions"],
    queryFn: listMySessions,
  });
  const revokeMut = useMutation({
    mutationFn: revokeMySession,
    onSuccess: () => {
      setConfirming(null);
      // Returned, so the mutation stays pending until the list is
      // refetched and focus moves only once the row has gone (#2682).
      return qc.invalidateQueries({ queryKey: ["settings-me-sessions"] });
    },
  });
  const headingRef = useRef<HTMLDivElement>(null);
  const removal = useFocusAfterRemoval(
    (sessionsQuery.data ?? []).map((s) => s.id),
    {
      get: revokeTriggers.get,
      fallback: () => headingRef.current,
      settled: !revokeMut.isPending,
    },
  );
  return (
    <Card id="sessions" data-testid="settings-section-sessions">
      <CardHeader>
        <CardTitle ref={headingRef} tabIndex={-1}>
          Active sessions
        </CardTitle>
        <CardDescription>Browsers currently signed in as you.</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {sessionsQuery.isLoading ? (
          <Skeleton className="h-24 w-full" />
        ) : sessionsQuery.error ? (
          <Alert variant="destructive">
            <AlertTitle>Could not load sessions</AlertTitle>
            <AlertDescription>
              {(sessionsQuery.error as Error).message}
            </AlertDescription>
          </Alert>
        ) : !sessionsQuery.data || sessionsQuery.data.length === 0 ? (
          <p
            className="py-6 text-center text-sm text-muted-foreground"
            data-testid="settings-sessions-empty"
          >
            No active OIDC sessions. Sessions appear here after sign-in via your
            IdP.
          </p>
        ) : (
          <div className="space-y-2">
            {sessionsQuery.data.map((s: MeSession) => {
              // Slice 162: build the augmented session line (UA · IP · geo).
              // sessionLine() returns "" when none of the fields are present,
              // so we conditionally render the second line to keep pre-
              // migration rows visually unchanged (P0-162-1: no fabrication).
              const metaLine = sessionLine(s);
              return (
                <div
                  key={s.id}
                  className="flex items-center justify-between gap-3 rounded-md border border-border p-3 text-sm"
                  data-testid="settings-session-row"
                >
                  <div className="min-w-0">
                    <div className="font-medium">
                      Session <code className="font-mono">…{s.last4}</code>
                    </div>
                    <div className="text-xs text-muted-foreground">
                      Created {s.created_at.slice(0, 10)}
                      {s.last_used_at
                        ? ` · last used ${s.last_used_at.slice(0, 10)}`
                        : null}
                    </div>
                    {metaLine !== "" ? (
                      <div
                        className="mt-0.5 truncate text-xs text-muted-foreground"
                        data-testid="settings-session-meta"
                        title={s.user_agent ?? undefined}
                      >
                        {metaLine}
                      </div>
                    ) : null}
                  </div>
                  <div className="flex items-center gap-2">
                    {s.is_current ? (
                      <Badge variant="outline">current</Badge>
                    ) : confirming === s.id ? (
                      <ConfirmInline
                        prompt="Revoke this session? That device is signed out immediately."
                        confirmLabel="Revoke session"
                        ariaLabel={`Confirm: revoke session …${s.last4}`}
                        busy={revokeMut.isPending}
                        onCancel={() => setConfirming(null)}
                        returnFocus={() => revokeTriggers.get(s.id)}
                        onConfirm={() => {
                          removal.arm(s.id);
                          revokeMut.mutate(s.id);
                        }}
                        data-testid="settings-session-revoke-confirm"
                      />
                    ) : (
                      <Button
                        size="sm"
                        variant="outline"
                        ref={revokeTriggers.ref(s.id)}
                        onClick={() => setConfirming(s.id)}
                        disabled={revokeMut.isPending}
                        data-testid="settings-session-revoke"
                      >
                        Revoke
                      </Button>
                    )}
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </CardContent>
    </Card>
  );
}
