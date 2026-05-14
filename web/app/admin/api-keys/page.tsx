// Slice 060 — /admin/api-keys (AC-4).
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
import { Input } from "@/components/ui/input";
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
  AdminCredential,
  AdminCredentialIssueRequest,
  AdminCredentialIssueResponse,
  AdminCredentialListResponse,
  AdminCredentialRotateResponse,
} from "@/lib/api";

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
  const [revokeConfirm, setRevokeConfirm] = useState<AdminCredential | null>(
    null,
  );

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
      setRevokeConfirm(null);
      qc.invalidateQueries({ queryKey: ["admin-creds"] });
    },
  });

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">API keys</h1>
        <p className="text-sm text-muted-foreground">
          Issue, rotate, and revoke bearer credentials for connectors and the
          CLI. The bearer plaintext is returned exactly once at issue and
          rotate; this UI shows it and discards it.
        </p>
      </div>

      <FreshSecretCallout
        value={freshSecret}
        onDismiss={() => setFreshSecret({ kind: "none" })}
      />

      <IssueForm
        onSubmit={(body) => issueMut.mutate(body)}
        submitting={issueMut.isPending}
      />

      <Card>
        <CardHeader>
          <CardTitle>Active credentials</CardTitle>
          <CardDescription>
            Bearer plaintext is never shown for an existing credential — only
            the last 4 characters. Rotate to mint a successor; revoke kills it.
          </CardDescription>
        </CardHeader>
        <CardContent>
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
                      {c.allowed_kinds.length === 0 ? (
                        <span className="text-muted-foreground">any</span>
                      ) : (
                        c.allowed_kinds.join(", ")
                      )}
                    </TableCell>
                    <TableCell>
                      {c.is_admin ? (
                        <Badge variant="destructive">admin</Badge>
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
                      <div className="flex justify-end gap-2">
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() => rotateMut.mutate(c.id)}
                          disabled={rotateMut.isPending}
                        >
                          Rotate
                        </Button>
                        <Button
                          size="sm"
                          variant="destructive"
                          onClick={() => setRevokeConfirm(c)}
                        >
                          Revoke
                        </Button>
                      </div>
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

      {revokeConfirm ? (
        <RevokeConfirmModal
          cred={revokeConfirm}
          onCancel={() => setRevokeConfirm(null)}
          onConfirm={() => revokeMut.mutate(revokeConfirm.id)}
          submitting={revokeMut.isPending}
        />
      ) : null}
    </div>
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
    <Alert variant="destructive" data-testid="fresh-secret-callout">
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
}: {
  onSubmit: (body: AdminCredentialIssueRequest) => void;
  submitting: boolean;
}) {
  const [scopePredicate, setScopePredicate] = useState("");
  const [allowedKinds, setAllowedKinds] = useState("");
  const [ttlDays, setTtlDays] = useState(90);
  const [isAdmin, setIsAdmin] = useState(false);

  function submit() {
    const kinds = allowedKinds
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);
    onSubmit({
      scope_predicate: scopePredicate.trim() || "{}",
      allowed_kinds: kinds,
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
          Scope predicate narrows which evidence kinds and scope cells the
          bearer can push to. TTL is in days; the bearer auto-expires.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Scope predicate (JSON)">
            <Input
              value={scopePredicate}
              onChange={(e) => setScopePredicate(e.target.value)}
              placeholder='{"connector":"aws"}'
            />
          </Field>
          <Field label="Allowed kinds (comma-separated)">
            <Input
              value={allowedKinds}
              onChange={(e) => setAllowedKinds(e.target.value)}
              placeholder="aws.s3.encryption.v1, aws.iam.user.v1"
            />
          </Field>
          <Field label="TTL (days)">
            <Input
              type="number"
              min={1}
              max={3650}
              value={ttlDays}
              onChange={(e) => setTtlDays(Number(e.target.value))}
            />
          </Field>
          <Field label="Admin credential">
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={isAdmin}
                onChange={(e) => setIsAdmin(e.target.checked)}
                className="h-4 w-4"
              />
              Grant admin privileges on this credential
            </label>
          </Field>
        </div>
        <Button onClick={submit} disabled={submitting}>
          {submitting ? "Issuing…" : "Issue credential"}
        </Button>
      </CardContent>
    </Card>
  );
}

function RevokeConfirmModal({
  cred,
  onCancel,
  onConfirm,
  submitting,
}: {
  cred: AdminCredential;
  onCancel: () => void;
  onConfirm: () => void;
  submitting: boolean;
}) {
  return (
    <div
      role="dialog"
      aria-modal="true"
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
    >
      <Card className="w-full max-w-md">
        <CardHeader>
          <CardTitle>Revoke credential?</CardTitle>
          <CardDescription>
            Last 4 <code>{cred.last4}</code> · ID{" "}
            <code className="text-xs">{cred.id.slice(0, 12)}…</code>
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3 text-sm">
          <p>
            Revocation is immediate. Any connector currently using this bearer
            will start failing on its next push. Best-effort last-seen:{" "}
            <code>{cred.last_used_at ?? "never"}</code>.
          </p>
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={onCancel} disabled={submitting}>
              Cancel
            </Button>
            <Button
              variant="destructive"
              onClick={onConfirm}
              disabled={submitting}
            >
              {submitting ? "Revoking…" : "Revoke now"}
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

function Field({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <label className="text-sm font-medium">{label}</label>
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
