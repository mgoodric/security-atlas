// Slice 060 — /admin/api-keys (AC-4).
//
// #2088: the issue form builds a key's scope with pickers (evidence kinds,
// scope dimensions) and keeps raw JSON as a fallback for predicates the
// builder cannot show. Model in `scope-builder.ts`.
//
// Real bind to slice 034: list / issue / rotate / revoke. The bearer
// plaintext returned by issue + rotate is shown EXACTLY ONCE in a
// dismissible callout; subsequent renders of the page do not surface it.
// This is the slice-034 AC-9 write-once secret contract carried into
// the UI verbatim.
//
// P0 anti-criterion: the page does NOT show the API-key bearer token
// after the initial issue/rotate response. Component state holds it; if
// the user navigates away or refreshes, it's gone.

"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { ScopePicker } from "@/components/scope/scope-picker";
import { EVERYTHING } from "@/components/scope/scope-picker-model";
import { advancedText } from "@/app/(authed)/framework-scopes/wizard";
import { fetchEvidenceKinds } from "@/lib/api/evidence-kinds";
import {
  kindOptions,
  parseRaw,
  predicateBody,
  rawToPicker,
  toggleKind,
} from "./scope-builder";
import { RowActions } from "@/components/ui/row-actions";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  isAnyKind,
  kindsLabel,
} from "@/app/(authed)/settings/allowed-kinds-display";
import {
  AdminCredential,
  AdminCredentialIssueRequest,
  AdminCredentialIssueResponse,
  AdminCredentialListResponse,
  AdminCredentialRotateResponse,
} from "@/lib/api/admin";
import { PageShell } from "@/components/shell/page-shell";

async function fetchCreds(): Promise<AdminCredential[]> {
  const res = await fetch(`/api/admin/credentials`);
  if (!res.ok) throw new Error(`list: ${res.status}`);
  const body = (await res.json()) as AdminCredentialListResponse;
  return body.items ?? [];
}

async function issueCred(
  body: AdminCredentialIssueRequest,
): Promise<AdminCredentialIssueResponse> {
  const res = await fetch(`/api/admin/credentials`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error(`issue: ${res.status}`);
  return (await res.json()) as AdminCredentialIssueResponse;
}

async function rotateCred(id: string): Promise<AdminCredentialRotateResponse> {
  const res = await fetch(
    `/api/admin/credentials/${encodeURIComponent(id)}/rotate`,
    { method: "POST" },
  );
  if (!res.ok) throw new Error(`rotate: ${res.status}`);
  return (await res.json()) as AdminCredentialRotateResponse;
}

async function revokeCred(id: string): Promise<void> {
  const res = await fetch(
    `/api/admin/credentials/${encodeURIComponent(id)}/revoke`,
    { method: "POST" },
  );
  if (!res.ok) throw new Error(`revoke: ${res.status}`);
}

type FreshSecret =
  | { kind: "none" }
  | {
      kind: "issued";
      bearer: string;
      last4: string;
      issued_at: string;
      expires_at?: string;
    }
  | {
      kind: "rotated";
      bearer: string;
      last4: string;
      predecessor_expires_at: string;
    };

export default function APIKeysPage() {
  const qc = useQueryClient();
  const list = useQuery({ queryKey: ["admin-creds"], queryFn: fetchCreds });
  const [freshSecret, setFreshSecret] = useState<FreshSecret>({ kind: "none" });

  const issueMut = useMutation({
    mutationFn: issueCred,
    onSuccess: (out) => {
      setFreshSecret({
        kind: "issued",
        bearer: out.bearer_token,
        last4: out.last4,
        issued_at: out.issued_at,
        expires_at: out.expires_at,
      });
      qc.invalidateQueries({ queryKey: ["admin-creds"] });
    },
  });

  const rotateMut = useMutation({
    mutationFn: rotateCred,
    onSuccess: (out) => {
      setFreshSecret({
        kind: "rotated",
        bearer: out.bearer_token,
        last4: out.last4,
        predecessor_expires_at: out.predecessor_expires_at,
      });
      qc.invalidateQueries({ queryKey: ["admin-creds"] });
    },
  });

  const revokeMut = useMutation({
    mutationFn: revokeCred,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["admin-creds"] });
    },
  });

  return (
    <PageShell
      title="API keys"
      subtitle={
        <>
          Issue, rotate, and revoke bearer credentials for connectors and the
          CLI. The bearer plaintext is returned exactly once at issue and
          rotate; this UI shows it and discards it.
        </>
      }
    >
      <FreshSecretCallout
        value={freshSecret}
        onDismiss={() => setFreshSecret({ kind: "none" })}
      />

      <IssueForm
        onSubmit={(body) => issueMut.mutate(body)}
        submitting={issueMut.isPending}
        error={issueMut.error ? (issueMut.error as Error).message : null}
      />

      <Card>
        <CardHeader>
          <CardTitle>Active credentials</CardTitle>
          <CardDescription>
            Bearer plaintext is never shown for an existing credential — only
            the last 4 characters. Rotate to mint a successor; revoke kills it.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          {revokeMut.error ? (
            <Alert variant="destructive" aria-live="polite">
              <AlertTitle>Revoke failed</AlertTitle>
              <AlertDescription data-testid="cred-revoke-error">
                {(revokeMut.error as Error).message}
              </AlertDescription>
            </Alert>
          ) : null}
          {list.isLoading ? (
            <ListSkeleton />
          ) : list.error ? (
            <Alert variant="destructive">
              <AlertTitle>Could not load credentials</AlertTitle>
              <AlertDescription>
                {(list.error as Error).message}
              </AlertDescription>
            </Alert>
          ) : list.data && list.data.length > 0 ? (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>ID</TableHead>
                  <TableHead className="w-24">Last 4</TableHead>
                  <TableHead>Allowed kinds</TableHead>
                  <TableHead className="w-20">Admin</TableHead>
                  <TableHead className="w-40">Last used</TableHead>
                  <TableHead className="w-40 text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {list.data.map((c) => (
                  <TableRow key={c.id}>
                    <TableCell className="font-mono text-xs">
                      {c.id.slice(0, 12)}…
                    </TableCell>
                    <TableCell className="font-mono">{c.last4}</TableCell>
                    <TableCell className="text-xs">
                      {isAnyKind(c.allowed_kinds) ? (
                        <span className="text-muted-foreground">
                          {kindsLabel(c.allowed_kinds)}
                        </span>
                      ) : (
                        kindsLabel(c.allowed_kinds)
                      )}
                    </TableCell>
                    <TableCell>
                      {c.is_admin ? (
                        <Badge variant="secondary">admin</Badge>
                      ) : (
                        <Badge variant="outline">no</Badge>
                      )}
                    </TableCell>
                    <TableCell className="text-xs">
                      {c.last_used_at ?? (
                        <span className="text-muted-foreground">never</span>
                      )}
                    </TableCell>
                    <TableCell className="text-right">
                      {/* #2090: Rotate runs from the menu; Revoke is
                          irreversible and confirms inline. */}
                      <RowActions
                        label={`Actions for credential ending ${c.last4}`}
                        data-testid={`cred-actions-${c.id}`}
                        items={[
                          {
                            id: "rotate",
                            label: "Rotate",
                            disabledReason: rotateMut.isPending
                              ? "A rotation is already running."
                              : undefined,
                            onSelect: () => rotateMut.mutate(c.id),
                            "data-testid": `cred-rotate-${c.id}`,
                          },
                          {
                            id: "revoke",
                            label: "Revoke",
                            destructive: true,
                            confirmPrompt: `Revoke the credential ending ${
                              c.last4
                            }? Revocation is immediate: any connector using this bearer fails on its next push. Last used: ${
                              c.last_used_at ?? "never"
                            }.`,
                            confirmLabel: "Revoke now",
                            onSelect: () => revokeMut.mutate(c.id),
                            "data-testid": `cred-revoke-${c.id}`,
                          },
                        ]}
                      />
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          ) : (
            <p className="py-8 text-center text-sm text-muted-foreground">
              No active credentials. Use the form above to issue your first.
            </p>
          )}
        </CardContent>
      </Card>
    </PageShell>
  );
}

function FreshSecretCallout({
  value,
  onDismiss,
}: {
  value: FreshSecret;
  onDismiss: () => void;
}) {
  if (value.kind === "none") return null;
  const title =
    value.kind === "issued"
      ? "API key issued — copy it now"
      : "API key rotated — copy the new bearer now";
  return (
    <Alert variant="info" data-testid="fresh-secret-callout">
      <AlertTitle>{title}</AlertTitle>
      <AlertDescription className="space-y-2">
        <p className="font-medium">
          This is the only time you&apos;ll see this bearer. The platform does
          not store it in plaintext; if you lose it, rotate again.
        </p>
        <div className="flex flex-col gap-2 sm:flex-row">
          <code className="flex-1 break-all rounded bg-foreground/5 p-2 font-mono text-xs">
            {value.bearer}
          </code>
          <div className="flex gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() => navigator.clipboard?.writeText(value.bearer)}
            >
              Copy
            </Button>
            <Button variant="ghost" size="sm" onClick={onDismiss}>
              Dismiss
            </Button>
          </div>
        </div>
        {value.kind === "issued" ? (
          <p className="text-xs">
            Last 4: <code>{value.last4}</code> · Issued at{" "}
            <code>{value.issued_at}</code>
            {value.expires_at ? (
              <>
                {" "}
                · Expires <code>{value.expires_at}</code>
              </>
            ) : null}
          </p>
        ) : (
          <p className="text-xs">
            Successor last 4: <code>{value.last4}</code> · Predecessor retires
            at <code>{value.predecessor_expires_at}</code> — connectors using
            the old bearer have until this timestamp to switch over.
          </p>
        )}
      </AlertDescription>
    </Alert>
  );
}

function IssueForm({
  onSubmit,
  submitting,
  error,
}: {
  onSubmit: (body: AdminCredentialIssueRequest) => void;
  submitting: boolean;
  error: string | null;
}) {
  const [allowedKinds, setAllowedKinds] = useState<string[]>([]);
  const [predicate, setPredicate] = useState<unknown>(EVERYTHING);
  const [raw, setRaw] = useState(false);
  const [rawText, setRawText] = useState("");
  const [rawError, setRawError] = useState<string | null>(null);
  const [ttlDays, setTtlDays] = useState(90);
  const [isAdmin, setIsAdmin] = useState(false);

  // Slice 363 — form-error association. errorId points every input
  // (via aria-describedby) at the Alert when it's mounted; undefined
  // when the alert is absent so React strips the attribute cleanly.
  // See `web/components/ui/checkbox.tsx` for the convention.
  const errorId = error ? "issue-credential-error" : undefined;

  function openRaw() {
    setRawText(advancedText(predicate));
    setRawError(null);
    setRaw(true);
  }

  function closeRaw() {
    const r = rawToPicker(rawText);
    if (!r.ok) {
      setRawError(r.error);
      return;
    }
    setPredicate(r.predicate);
    setRaw(false);
  }

  // Under raw JSON the predicate is the editor's text as written, so a
  // predicate the builder cannot show can still be issued. Text that is
  // not a JSON object blocks submit, as does an unfinished picker choice.
  const rawParse = raw ? parseRaw(rawText) : null;
  const predicateToSend: unknown = rawParse
    ? rawParse.ok
      ? rawParse.predicate
      : null
    : predicate;

  function submit() {
    if (predicateToSend === null) {
      if (rawParse && !rawParse.ok) setRawError(rawParse.error);
      return;
    }
    onSubmit({
      scope_predicate: predicateBody(predicateToSend),
      allowed_kinds: allowedKinds,
      ttl_seconds: ttlDays * 86400,
      is_admin: isAdmin,
      is_approver: false,
      owner_roles: [],
    });
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Issue a new credential</CardTitle>
        <CardDescription>
          Choose which evidence kinds the bearer may push and which scope cells
          it may push for. TTL is in days; the bearer auto-expires.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <KindsPicker value={allowedKinds} onChange={setAllowedKinds} />

        <div className="space-y-2" data-testid="issue-scope">
          <div className="flex items-center justify-between gap-2">
            <span className="text-sm font-medium">Scope</span>
            <Button
              type="button"
              size="sm"
              variant="outline"
              aria-pressed={raw}
              // An unfinished picker choice has no JSON to open; opening
              // it as Everything would silently widen the key.
              disabled={!raw && predicate === null}
              onClick={raw ? closeRaw : openRaw}
              data-testid="issue-scope-raw-toggle"
            >
              {raw ? "Back to the builder" : "Raw JSON"}
            </Button>
          </div>
          {raw ? (
            <div className="space-y-1">
              <label htmlFor="issue-scope-predicate" className="text-sm">
                Scope predicate (JSON)
              </label>
              <textarea
                id="issue-scope-predicate"
                value={rawText}
                onChange={(e) => {
                  setRawText(e.target.value);
                  setRawError(null);
                }}
                rows={8}
                spellCheck={false}
                className="w-full rounded-md border bg-background p-2 font-mono text-xs"
                data-testid="issue-scope-json"
                aria-invalid={rawError ? true : undefined}
                aria-describedby={rawError ? "issue-scope-json-error" : errorId}
              />
              {rawError ? (
                <p
                  id="issue-scope-json-error"
                  className="text-xs text-rose-600"
                  role="alert"
                  data-testid="issue-scope-json-error"
                >
                  {rawError}
                </p>
              ) : null}
            </div>
          ) : (
            <ScopePicker value={predicate} onChange={setPredicate} />
          )}
        </div>

        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="TTL (days)" htmlFor="issue-ttl-days">
            <Input
              id="issue-ttl-days"
              type="number"
              min={1}
              max={3650}
              value={ttlDays}
              onChange={(e) => setTtlDays(Number(e.target.value))}
              aria-describedby={errorId}
            />
          </Field>
          <Field label="Admin credential">
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={isAdmin}
                onChange={(e) => setIsAdmin(e.target.checked)}
                className="h-4 w-4"
                aria-describedby={errorId}
              />
              Grant admin privileges on this credential
            </label>
          </Field>
        </div>
        <Button
          onClick={submit}
          // An unfinished picker choice says so under the picker; under raw
          // JSON a click shows why the text cannot be sent.
          disabled={submitting || (!raw && predicateToSend === null)}
          data-testid="issue-credential-submit"
        >
          {submitting ? "Issuing…" : "Issue credential"}
        </Button>
        {error ? (
          <Alert
            variant="destructive"
            id="issue-credential-error"
            aria-live="polite"
          >
            <AlertTitle>Issue failed</AlertTitle>
            <AlertDescription data-testid="issue-credential-error">
              {error}
            </AlertDescription>
          </Alert>
        ) : null}
      </CardContent>
    </Card>
  );
}

// The evidence kinds the key may push, from the tenant's schema registry.
// None picked means any kind. A kind the registry does not list (a
// connector's private kind not registered yet) can be added by name.
function KindsPicker({
  value,
  onChange,
}: {
  value: string[];
  onChange: (kinds: string[]) => void;
}) {
  const schemas = useQuery({
    queryKey: ["evidence-kinds"],
    queryFn: fetchEvidenceKinds,
    staleTime: 60_000,
  });
  const [filter, setFilter] = useState("");
  const [extra, setExtra] = useState("");
  const options = kindOptions(schemas.data?.schemas ?? [], value);
  const q = filter.trim().toLowerCase();
  const shown = q
    ? options.filter((k) => k.toLowerCase().includes(q))
    : options;

  function addExtra() {
    const k = extra.trim();
    if (k === "") return;
    if (!value.includes(k)) onChange([...value, k]);
    setExtra("");
  }

  return (
    <fieldset className="space-y-2" data-testid="issue-kinds">
      <legend className="text-sm font-medium">Evidence kinds</legend>
      <p className="text-xs text-muted-foreground">
        {value.length === 0
          ? "None picked: the key may push any kind."
          : `${value.length} picked: the key may push only these.`}
      </p>
      <Input
        aria-label="Filter evidence kinds"
        value={filter}
        onChange={(e) => setFilter(e.target.value)}
        placeholder="Filter kinds"
        data-testid="issue-kinds-filter"
      />
      {schemas.isPending ? (
        <p className="text-sm text-muted-foreground">Loading evidence kinds…</p>
      ) : schemas.isError ? (
        <p className="text-sm text-destructive" role="alert">
          Could not load evidence kinds: {schemas.error.message}. Add kinds by
          name below.
        </p>
      ) : null}
      <div className="max-h-48 space-y-1 overflow-auto rounded border p-2">
        {shown.length === 0 ? (
          <p className="text-xs text-muted-foreground">No kinds match.</p>
        ) : (
          shown.map((k) => (
            <label key={k} className="flex items-center gap-2 text-sm">
              <Checkbox
                checked={value.includes(k)}
                onCheckedChange={() => onChange(toggleKind(value, k, options))}
                data-testid={`issue-kind-${k}`}
              />
              <span className="font-mono text-xs">{k}</span>
            </label>
          ))
        )}
      </div>
      <div className="flex gap-2">
        <Input
          aria-label="Add an evidence kind by name"
          value={extra}
          onChange={(e) => setExtra(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              addExtra();
            }
          }}
          placeholder="Add a kind by name"
          data-testid="issue-kinds-add-input"
        />
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={addExtra}
          data-testid="issue-kinds-add"
        >
          Add
        </Button>
      </div>
    </fieldset>
  );
}

function Field({
  label,
  htmlFor,
  children,
}: {
  label: string;
  // The control this label names. Without it the label is a caption only
  // and the input has no accessible name (axe `label`).
  htmlFor?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <label htmlFor={htmlFor} className="text-sm font-medium">
        {label}
      </label>
      {children}
    </div>
  );
}

function ListSkeleton() {
  return (
    <div className="space-y-2">
      {Array.from({ length: 4 }).map((_, i) => (
        <Skeleton key={i} className="h-10 w-full" />
      ))}
    </div>
  );
}
