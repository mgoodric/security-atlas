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
//
// #1794: a second tab holds the Statement of Applicability
// (components/framework-scopes/soa-tab.tsx).
// #1795: each revision in review shows what it changes against the
// activated revision (components/framework-scopes/review-diff.tsx).
//
// #2115: the lifecycle action lives in the shared header (DetailHeader,
// #2102), one for the working revision (`headerAction` in ../wizard.ts):
// Submit for approval, Approve, Activate, or, once only frozen revisions
// remain, New revision (a new draft copied from the activated one, never
// a change to it, #1965). With no revisions the header and the empty
// state link to the wizard (/framework-scopes/new). Approval is the
// platform's rule (#2231): the page calls the approve route and shows its
// refusal; it never decides who may approve.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { useState } from "react";
import { useParams } from "next/navigation";

import { DetailHeader } from "@/components/detail/detail-header";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { SelfApprovedBadge } from "@/components/self-approved-badge";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { ReviewDiff } from "@/components/framework-scopes/review-diff";
import { SoATab, useSoATitle } from "@/components/framework-scopes/soa-tab";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { invalidateApprovals } from "@/lib/api/approvals";
import { APIError } from "@/lib/api/base";
import {
  FrameworkScope,
  FrameworkScopeState,
} from "@/lib/api/framework-scopes";

import {
  errorMessage,
  headerAction,
  isFrozen,
  newScopeHref,
  parseAdvanced,
  type HeaderAction,
} from "../wizard";

type ListResp = { framework_scopes: FrameworkScope[] };

async function fetchScopes(fv: string): Promise<FrameworkScope[]> {
  const res = await fetch(
    `/api/framework-scopes?framework_version=${encodeURIComponent(fv)}`,
  );
  if (!res.ok) {
    throw new APIError(res.status, await errorMessage(res));
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
    throw new APIError(res.status, await errorMessage(res));
  }
  return (await res.json()) as {
    framework_scope: FrameworkScope;
    approval_invalidated: boolean;
  };
}

// #1965: an activated or superseded revision is immutable. A change starts
// as a new draft revision copied from it (predicate and decisions).
async function createRevision(fromID: string) {
  const res = await fetch(`/api/framework-scopes`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ from_scope_id: fromID }),
  });
  if (!res.ok) {
    throw new APIError(res.status, await errorMessage(res));
  }
  return (await res.json()) as { framework_scope: FrameworkScope };
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
    throw new APIError(res.status, await errorMessage(res));
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
  const [activateOpen, setActivateOpen] = useState(false);
  const [tab, setTab] = useState<"scopes" | "soa">("scopes");
  const soaTitle = useSoATitle(fv);

  const reset = () => {
    void invalidateApprovals(qc);
    return qc.invalidateQueries({ queryKey: ["framework-scopes", fv] });
  };

  const onTransitionError = (e: unknown) => setBanner((e as Error).message);
  const submitMut = useMutation({
    mutationFn: (id: string) => transition(id, "submit"),
    onSuccess: () => {
      setBanner(null);
      reset();
    },
    onError: onTransitionError,
  });
  // The approval rule is the platform's (#2231): a 403 is its refusal
  // (the submitter, or a machine credential), shown as it words it.
  const approveMut = useMutation({
    mutationFn: (id: string) => transition(id, "approve"),
    onSuccess: () => {
      setBanner(null);
      reset();
    },
    onError: onTransitionError,
  });
  const activateMut = useMutation({
    mutationFn: ({
      id,
      effective_from,
    }: {
      id: string;
      effective_from: string;
    }) => transition(id, "activate", { effective_from }),
    onSuccess: () => {
      setBanner(null);
      setActivateOpen(false);
      reset();
    },
    onError: onTransitionError,
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
    onError: (e: unknown) => {
      if (e instanceof APIError && e.status === 409) {
        setBanner(
          "This scope is frozen. Create a new revision to change its predicate.",
        );
      }
    },
  });
  const revisionMut = useMutation({
    mutationFn: (fromID: string) => createRevision(fromID),
    onSuccess: () => {
      setBanner(null);
      reset();
    },
    onError: (e: unknown) => {
      setBanner(
        e instanceof APIError && e.status === 403
          ? "You do not have permission to create a scope revision."
          : "Could not create a new revision: " + (e as Error).message,
      );
    },
  });

  const action = scopesQ.data ? headerAction(scopesQ.data) : undefined;
  const primaryAction = action
    ? headerPrimaryAction(action, fv, {
        submit: (id) => submitMut.mutate(id),
        approve: (id) => approveMut.mutate(id),
        activate: () => setActivateOpen(true),
        revise: (id) => revisionMut.mutate(id),
      })
    : undefined;

  return (
    <DetailHeader
      data-testid="framework-scope-page"
      title="Framework scope"
      primaryAction={primaryAction}
      subtitle={
        <p className="text-sm text-muted-foreground">
          One scope predicate per framework, versioned. Approval is the
          auditor&apos;s sign-off; activation is when it takes effect. Editing
          the predicate on a row in review or approved bounces it back to draft
          — the auditor sees every change. Activated and superseded rows are
          frozen; change one by creating a new revision from it.
        </p>
      }
    >
      {banner && (
        <Alert variant="destructive">
          <AlertTitle>Heads up</AlertTitle>
          <AlertDescription>{banner}</AlertDescription>
        </Alert>
      )}

      {action?.kind === "activate" && activateOpen && (
        <ActivatePanel
          scope={action.scope}
          pending={activateMut.isPending}
          onConfirm={(ef) =>
            activateMut.mutate({ id: action.scope.id, effective_from: ef })
          }
          onCancel={() => setActivateOpen(false)}
        />
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
        <div className="flex gap-1 border-b" role="tablist">
          {(
            [
              { key: "scopes", label: "Scopes" },
              { key: "soa", label: soaTitle },
            ] as const
          ).map((t) => (
            <button
              key={t.key}
              type="button"
              role="tab"
              aria-selected={tab === t.key}
              data-testid={`framework-scope-tab-${t.key}`}
              onClick={() => setTab(t.key)}
              className={`-mb-px border-b-2 px-3 py-1.5 text-sm transition-colors ${
                tab === t.key
                  ? "border-primary font-medium text-foreground"
                  : "border-transparent text-muted-foreground hover:text-foreground"
              }`}
            >
              {t.label}
            </button>
          ))}
        </div>
      )}

      {scopesQ.data && tab === "soa" && (
        <SoATab frameworkVersionID={fv} scopes={scopesQ.data} />
      )}

      {scopesQ.data && tab === "scopes" && (
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
              frameworkVersionId={fv}
              onPatchPredicate={(id, predicate) =>
                patchMut.mutate({ id, predicate })
              }
            />
          </CardContent>
        </Card>
      )}

      {scopesQ.data &&
        tab === "scopes" &&
        scopesQ.data
          .filter((s) => s.state === "review")
          .map((s) => <ReviewDiff key={s.id} scope={s} />)}
    </DetailHeader>
  );
}

function headerPrimaryAction(
  action: HeaderAction,
  fv: string,
  run: {
    submit: (id: string) => void;
    approve: (id: string) => void;
    activate: () => void;
    revise: (id: string) => void;
  },
) {
  switch (action.kind) {
    case "create":
      return {
        label: "New scope",
        href: newScopeHref(fv),
        "data-testid": "framework-scope-create",
      };
    case "submit":
      return {
        label: "Submit for approval",
        onClick: () => run.submit(action.scope.id),
        "data-testid": "framework-scope-submit",
      };
    case "approve":
      return {
        label: "Approve",
        onClick: () => run.approve(action.scope.id),
        "data-testid": "framework-scope-approve",
      };
    case "activate":
      return {
        label: "Activate",
        onClick: run.activate,
        "data-testid": "framework-scope-activate",
      };
    case "revise":
      return {
        label: "New revision",
        onClick: () => run.revise(action.scope.id),
        "data-testid": "framework-scope-new-revision",
      };
  }
}

function ActivatePanel({
  scope,
  pending,
  onConfirm,
  onCancel,
}: {
  scope: FrameworkScope;
  pending: boolean;
  onConfirm: (effectiveFrom: string) => void;
  onCancel: () => void;
}) {
  const [effectiveFrom, setEffectiveFrom] = useState(() =>
    new Date().toISOString().slice(0, 16),
  );
  return (
    <Card data-testid="framework-scope-activate-panel">
      <CardHeader>
        <CardTitle>Activate &ldquo;{scope.name}&rdquo;</CardTitle>
        <CardDescription>
          It replaces the current scope from the time below.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-wrap items-center gap-2">
        <label htmlFor="fs-effective-from" className="text-sm">
          Effective from
        </label>
        <input
          id="fs-effective-from"
          type="datetime-local"
          value={effectiveFrom}
          onChange={(e) => setEffectiveFrom(e.target.value)}
          className="rounded border bg-background p-1 text-xs"
        />
        <Button
          size="sm"
          disabled={pending}
          onClick={() => {
            // datetime-local omits the timezone; coerce to local-tz ISO.
            onConfirm(new Date(effectiveFrom).toISOString());
          }}
        >
          Confirm
        </Button>
        <Button size="sm" variant="ghost" onClick={onCancel}>
          Cancel
        </Button>
      </CardContent>
    </Card>
  );
}

function ScopeTable({
  scopes,
  frameworkVersionId,
  onPatchPredicate,
}: {
  scopes: FrameworkScope[];
  frameworkVersionId: string;
  onPatchPredicate: (id: string, predicate: unknown) => void;
}) {
  if (scopes.length === 0) {
    return (
      <div
        className="space-y-3 py-12 text-center"
        data-testid="framework-scope-empty"
      >
        <p className="text-sm text-muted-foreground">
          No scopes yet for this framework.
        </p>
        <Link
          href={newScopeHref(frameworkVersionId)}
          className="text-sm underline underline-offset-2"
          data-testid="framework-scope-empty-create"
        >
          Create a scope
        </Link>
      </div>
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
          <TableHead className="w-32">Status</TableHead>
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
              <span className="inline-flex items-center gap-1.5">
                <StateBadge state={s.state} />
                <SelfApprovedBadge
                  selfApproved={s.self_approved}
                  testId="framework-scope-self-approved"
                />
              </span>
            </TableCell>
            <TableCell>
              {isFrozen(s.state) ? (
                <pre
                  className="max-h-32 overflow-auto rounded border bg-muted p-2 font-mono text-xs"
                  data-testid="framework-scope-predicate-frozen"
                >
                  {JSON.stringify(s.predicate, null, 2)}
                </pre>
              ) : (
                <PredicateEditor
                  initial={s.predicate}
                  onSave={(predicate) => onPatchPredicate(s.id, predicate)}
                />
              )}
            </TableCell>
            <TableCell className="text-xs">
              {s.effective_from ?? (
                <span className="text-muted-foreground">—</span>
              )}
            </TableCell>
            <TableCell className="text-xs text-muted-foreground">
              {s.state === "activated"
                ? "Current scope"
                : s.state === "superseded"
                  ? "Superseded"
                  : null}
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
            // #2115: never a null predicate, which the platform reads as
            // every cell; parsed only, the platform validates the grammar.
            const r = parseAdvanced(text);
            if (r.ok) onSave(r.predicate);
            else setError(r.error);
          }}
        >
          Save predicate
        </Button>
        {error && <span className="text-xs text-destructive">{error}</span>}
      </div>
    </div>
  );
}
