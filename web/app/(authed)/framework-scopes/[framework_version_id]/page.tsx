"use client";

// Slice 018 frontend — list + workflow surface for FrameworkScope rows
// keyed to a single framework_version.
//
// AC-14: list current + historical scopes for the framework; activated
//        row highlighted; draft / review badges shown.
// AC-15: predicate editor (JSON textarea); save submits PATCH; banner
//        surfaces approval_invalidated from upstream.
// AC-16: Approve modal — only renders for users in approver role; for v1
//        the page surfaces the action and reads back the 403 on upstream
//        denial. Slice 035 will gate by reading IsApprover from /me.
// AC-17: Activate action with effective_from picker (defaults to now()).

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useParams } from "next/navigation";

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
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { APIError, FrameworkScope, FrameworkScopeState } from "@/lib/api";

type ListResp = { framework_scopes: FrameworkScope[] };

async function fetchScopes(fv: string): Promise<FrameworkScope[]> {
  const res = await fetch(
    `/api/framework-scopes?framework_version=${encodeURIComponent(fv)}`,
  );
  if (!res.ok) {
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  }
  const body = (await res.json()) as ListResp;
  return body.framework_scopes;
}

async function patchPredicate(id: string, predicate: unknown) {
  const res = await fetch(`/api/framework-scopes/${encodeURIComponent(id)}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ predicate }),
  });
  if (!res.ok) {
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  }
  return (await res.json()) as {
    framework_scope: FrameworkScope;
    approval_invalidated: boolean;
  };
}

async function transition(
  id: string,
  t: "submit" | "approve" | "activate",
  body?: Record<string, unknown>,
) {
  const res = await fetch(
    `/api/framework-scopes/${encodeURIComponent(id)}/${t}`,
    {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: body ? JSON.stringify(body) : undefined,
    },
  );
  if (!res.ok) {
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  }
  return (await res.json()) as { framework_scope: FrameworkScope };
}

export default function FrameworkScopesPage() {
  const params = useParams<{ framework_version_id: string }>();
  const fv = params.framework_version_id;

  const qc = useQueryClient();
  const scopesQ = useQuery({
    queryKey: ["framework-scopes", fv],
    queryFn: () => fetchScopes(fv),
  });

  const [banner, setBanner] = useState<string | null>(null);

  const reset = () =>
    qc.invalidateQueries({ queryKey: ["framework-scopes", fv] });

  const submitMut = useMutation({
    mutationFn: (id: string) => transition(id, "submit"),
    onSuccess: reset,
  });
  const approveMut = useMutation({
    mutationFn: (id: string) => transition(id, "approve"),
    onSuccess: reset,
    onError: (e: unknown) => {
      if (e instanceof APIError && e.status === 403) {
        setBanner("Approver role required to approve scope changes.");
      }
    },
  });
  const activateMut = useMutation({
    mutationFn: ({
      id,
      effective_from,
    }: {
      id: string;
      effective_from: string;
    }) => transition(id, "activate", { effective_from }),
    onSuccess: reset,
  });
  const patchMut = useMutation({
    mutationFn: ({ id, predicate }: { id: string; predicate: unknown }) =>
      patchPredicate(id, predicate),
    onSuccess: (data) => {
      if (data.approval_invalidated) {
        setBanner(
          "Approval invalidated — predicate changed, resubmit for review.",
        );
      } else {
        setBanner(null);
      }
      reset();
    },
  });

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">
          Framework scope
        </h1>
        <p className="text-sm text-muted-foreground">
          One scope predicate per framework, versioned. Approval is the
          auditor&apos;s sign-off; activation is when it takes effect. Editing
          the predicate on a row in review or approved bounces it back to draft
          — the auditor sees every change.
        </p>
      </div>

      {banner && (
        <Alert variant="destructive">
          <AlertTitle>Heads up</AlertTitle>
          <AlertDescription>{banner}</AlertDescription>
        </Alert>
      )}

      {scopesQ.isLoading && <Skeleton className="h-32 w-full" />}
      {scopesQ.error && (
        <Alert variant="destructive">
          <AlertTitle>Could not load scopes</AlertTitle>
          <AlertDescription>
            {(scopesQ.error as Error).message}
          </AlertDescription>
        </Alert>
      )}

      {scopesQ.data && (
        <Card>
          <CardHeader>
            <CardTitle>All versions</CardTitle>
            <CardDescription>
              Activated row is highlighted. Historical rows preserved as audit
              evidence.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <ScopeTable
              scopes={scopesQ.data}
              onSubmit={(id) => submitMut.mutate(id)}
              onApprove={(id) => approveMut.mutate(id)}
              onActivate={(id, ef) =>
                activateMut.mutate({ id, effective_from: ef })
              }
              onPatchPredicate={(id, predicate) =>
                patchMut.mutate({ id, predicate })
              }
            />
          </CardContent>
        </Card>
      )}
    </div>
  );
}

function ScopeTable({
  scopes,
  onSubmit,
  onApprove,
  onActivate,
  onPatchPredicate,
}: {
  scopes: FrameworkScope[];
  onSubmit: (id: string) => void;
  onApprove: (id: string) => void;
  onActivate: (id: string, effectiveFrom: string) => void;
  onPatchPredicate: (id: string, predicate: unknown) => void;
}) {
  if (scopes.length === 0) {
    return (
      <p className="py-12 text-center text-sm text-muted-foreground">
        No scopes yet. Create one via POST /v1/framework-scopes (the new-scope
        UI lands in slice 020).
      </p>
    );
  }
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Name</TableHead>
          <TableHead className="w-28">State</TableHead>
          <TableHead>Predicate</TableHead>
          <TableHead className="w-44">Effective from</TableHead>
          <TableHead className="w-72">Actions</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {scopes.map((s) => (
          <TableRow
            key={s.id}
            className={
              s.state === "activated"
                ? "bg-emerald-50 dark:bg-emerald-950/30"
                : ""
            }
          >
            <TableCell className="font-medium">{s.name}</TableCell>
            <TableCell>
              <StateBadge state={s.state} />
            </TableCell>
            <TableCell>
              <PredicateEditor
                initial={s.predicate}
                onSave={(predicate) => onPatchPredicate(s.id, predicate)}
              />
            </TableCell>
            <TableCell className="text-xs">
              {s.effective_from ?? (
                <span className="text-muted-foreground">—</span>
              )}
            </TableCell>
            <TableCell>
              <ScopeActions
                scope={s}
                onSubmit={() => onSubmit(s.id)}
                onApprove={() => onApprove(s.id)}
                onActivate={(ef) => onActivate(s.id, ef)}
              />
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function StateBadge({ state }: { state: FrameworkScopeState }) {
  const variant =
    state === "activated"
      ? "default"
      : state === "approved"
        ? "secondary"
        : state === "review"
          ? "outline"
          : state === "superseded"
            ? "outline"
            : "outline";
  return <Badge variant={variant}>{state}</Badge>;
}

function PredicateEditor({
  initial,
  onSave,
}: {
  initial: unknown;
  onSave: (predicate: unknown) => void;
}) {
  const [text, setText] = useState(() => JSON.stringify(initial, null, 2));
  const [error, setError] = useState<string | null>(null);
  return (
    <div className="space-y-2">
      <textarea
        className="w-full rounded border bg-background p-2 font-mono text-xs"
        rows={4}
        value={text}
        onChange={(e) => {
          setText(e.target.value);
          setError(null);
        }}
      />
      <div className="flex items-center gap-2">
        <Button
          size="sm"
          variant="outline"
          onClick={() => {
            try {
              const parsed = JSON.parse(text);
              onSave(parsed);
            } catch (e) {
              setError("Invalid JSON: " + (e as Error).message);
            }
          }}
        >
          Save predicate
        </Button>
        {error && <span className="text-xs text-destructive">{error}</span>}
      </div>
    </div>
  );
}

function ScopeActions({
  scope,
  onSubmit,
  onApprove,
  onActivate,
}: {
  scope: FrameworkScope;
  onSubmit: () => void;
  onApprove: () => void;
  onActivate: (effectiveFrom: string) => void;
}) {
  const [activateOpen, setActivateOpen] = useState(false);
  const [effectiveFrom, setEffectiveFrom] = useState(() =>
    new Date().toISOString().slice(0, 16),
  );

  return (
    <div className="flex flex-wrap gap-2">
      {scope.state === "draft" && (
        <Button size="sm" variant="outline" onClick={onSubmit}>
          Submit for review
        </Button>
      )}
      {scope.state === "review" && (
        <Button size="sm" onClick={onApprove}>
          Approve
        </Button>
      )}
      {scope.state === "approved" && !activateOpen && (
        <Button size="sm" onClick={() => setActivateOpen(true)}>
          Activate
        </Button>
      )}
      {scope.state === "approved" && activateOpen && (
        <div className="flex flex-wrap items-center gap-2">
          <input
            type="datetime-local"
            value={effectiveFrom}
            onChange={(e) => setEffectiveFrom(e.target.value)}
            className="rounded border bg-background p-1 text-xs"
          />
          <Button
            size="sm"
            onClick={() => {
              // datetime-local omits the timezone; coerce to local-tz ISO.
              const iso = new Date(effectiveFrom).toISOString();
              onActivate(iso);
              setActivateOpen(false);
            }}
          >
            Confirm
          </Button>
          <Button
            size="sm"
            variant="ghost"
            onClick={() => setActivateOpen(false)}
          >
            Cancel
          </Button>
        </div>
      )}
      {(scope.state === "activated" || scope.state === "superseded") && (
        <span className="text-xs text-muted-foreground">
          {scope.state === "activated" ? "Current scope" : "Superseded"}
        </span>
      )}
    </div>
  );
}
