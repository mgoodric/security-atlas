"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo, useReducer, useState } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";

import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
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
} from "@/lib/api/admin";

import { isAnyKind, kindsLabel } from "../allowed-kinds-display";

import { initialState, reduce } from "../token-state";

// --- BFF wrappers ---------------------------------------------------------

async function fetchCreds(): Promise<AdminCredential[]> {
  const res = await fetch(`/api/admin/credentials`);
  if (res.status === 403) {
    // Non-admin -- surfaced to the section via the empty-array path.
    return [];
  }
  if (!res.ok) {
    throw new Error(`list credentials: ${res.status}`);
  }
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
  if (!res.ok) {
    throw new Error(`issue credential: ${res.status}`);
  }
  return (await res.json()) as AdminCredentialIssueResponse;
}

async function revokeCred(id: string): Promise<void> {
  const res = await fetch(
    `/api/admin/credentials/${encodeURIComponent(id)}/revoke`,
    { method: "POST" },
  );
  if (!res.ok) {
    throw new Error(`revoke credential: ${res.status}`);
  }
}

// Slice 163: rotateCred mirrors revokeCred but hits the
// already-shipped /api/admin/credentials/:id/rotate BFF route (slice
// 060). The successor's bearer plaintext is returned ONCE and is the
// caller's only chance to capture it -- the reducer holds it for the
// duration of the callout, then DISMISS clears it.
async function rotateCred(id: string): Promise<AdminCredentialRotateResponse> {
  const res = await fetch(
    `/api/admin/credentials/${encodeURIComponent(id)}/rotate`,
    { method: "POST" },
  );
  if (!res.ok) {
    throw new Error(`rotate credential: ${res.status}`);
  }
  return (await res.json()) as AdminCredentialRotateResponse;
}

// --- Section 4: API tokens ------------------------------------------------

export function ApiTokensSection({ isAdmin }: { isAdmin: boolean }) {
  const qc = useQueryClient();
  const list = useQuery({
    queryKey: ["settings-creds"],
    queryFn: fetchCreds,
    enabled: isAdmin,
  });
  const [freshSecret, dispatch] = useReducer(reduce, initialState);
  const [issueOpen, setIssueOpen] = useState(false);
  const [revokeConfirm, setRevokeConfirm] = useState<AdminCredential | null>(
    null,
  );
  // Slice 163: a second confirm modal for Rotate. Same shape as
  // revokeConfirm -- when set, the modal renders for that credential
  // and the modal's onConfirm fires the rotateMut.
  const [rotateConfirm, setRotateConfirm] = useState<AdminCredential | null>(
    null,
  );

  const issueMut = useMutation({
    mutationFn: issueCred,
    onSuccess: (out) => {
      dispatch({
        kind: "ISSUED",
        bearer: out.bearer_token,
        last4: out.last4,
        issued_at: out.issued_at,
      });
      setIssueOpen(false);
      qc.invalidateQueries({ queryKey: ["settings-creds"] });
    },
  });

  const revokeMut = useMutation({
    mutationFn: revokeCred,
    onSuccess: () => {
      setRevokeConfirm(null);
      qc.invalidateQueries({ queryKey: ["settings-creds"] });
    },
  });

  // Slice 163: rotateMut dispatches ROTATED on success. The predecessor's
  // last4 is captured from the modal's row at click-time (passed as the
  // mutation variable) so the callout can render "rotated from ...XXXX"
  // without re-querying the list. The bearer plaintext flows through
  // state ONCE and is GC'd on DISMISS (P0-163-1).
  const rotateMut = useMutation({
    mutationFn: (args: { id: string; predecessor_last4: string }) =>
      rotateCred(args.id),
    onSuccess: (out, args) => {
      dispatch({
        kind: "ROTATED",
        bearer: out.bearer_token,
        last4: out.last4,
        predecessor_last4: args.predecessor_last4,
        predecessor_expires_at: out.predecessor_expires_at,
      });
      setRotateConfirm(null);
      qc.invalidateQueries({ queryKey: ["settings-creds"] });
    },
  });

  // Slice 163: derive the predecessor -> successor link map from the
  // list. The slice 062 wire shape carries `rotated_from` on the
  // SUCCESSOR; to surface the forward direction on a predecessor row's
  // badge ("rotated -> ...succ") we invert -- for each row with a
  // rotated_from, the row pointed-to-by-rotated_from has THIS row as
  // its successor. Memoised on list.data so the inversion does not
  // re-run on unrelated re-renders (modal open/close, mutation
  // pending-state flips).
  const successorByPredecessorId = useMemo(() => {
    const m = new Map<string, { id: string; last4: string }>();
    for (const c of list.data ?? []) {
      if (c.rotated_from) {
        m.set(c.rotated_from, { id: c.id, last4: c.last4 });
      }
    }
    return m;
  }, [list.data]);

  if (!isAdmin) {
    return (
      <Card id="tokens" data-testid="settings-section-tokens-non-admin">
        <CardHeader>
          <CardTitle>Personal API tokens</CardTitle>
          <CardDescription>
            For CLI use (<code>security-atlas evidence push</code>).
          </CardDescription>
        </CardHeader>
        <CardContent>
          <Alert>
            <AlertTitle>Admin role required</AlertTitle>
            <AlertDescription>
              Issuing personal API tokens currently requires the{" "}
              <strong>admin</strong> role. Contact your tenant administrator, or
              visit{" "}
              <a href="/admin/api-keys" className="underline">
                /admin/api-keys
              </a>{" "}
              if you have admin access in another session. User-scoped token
              issuance is not available yet.
            </AlertDescription>
          </Alert>
        </CardContent>
      </Card>
    );
  }

  return (
    <Card id="tokens" data-testid="settings-section-tokens">
      <CardHeader>
        <div className="flex items-center justify-between gap-3">
          <div>
            <CardTitle>Personal API tokens</CardTitle>
            <CardDescription>
              For CLI use (<code>security-atlas evidence push</code>). Token
              last-4 shown; plaintext never re-displayed.
            </CardDescription>
          </div>
          <Button
            size="sm"
            onClick={() => setIssueOpen(true)}
            data-testid="settings-token-issue-button"
          >
            Issue token
          </Button>
        </div>
      </CardHeader>
      <CardContent className="space-y-4">
        {freshSecret.kind === "issued" ? (
          <FreshTokenCallout
            variant="issued"
            bearer={freshSecret.bearer}
            last4={freshSecret.last4}
            issuedAt={freshSecret.issued_at}
            onDismiss={() => dispatch({ kind: "DISMISS" })}
          />
        ) : freshSecret.kind === "rotated" ? (
          <FreshTokenCallout
            variant="rotated"
            bearer={freshSecret.bearer}
            last4={freshSecret.last4}
            predecessorLast4={freshSecret.predecessor_last4}
            predecessorExpiresAt={freshSecret.predecessor_expires_at}
            onDismiss={() => dispatch({ kind: "DISMISS" })}
          />
        ) : null}

        {issueOpen ? (
          <IssueTokenForm
            submitting={issueMut.isPending}
            onCancel={() => setIssueOpen(false)}
            onSubmit={(body) => issueMut.mutate(body)}
          />
        ) : null}

        {list.isLoading ? (
          <Skeleton
            className="h-32 w-full"
            data-testid="settings-tokens-loading"
          />
        ) : list.error ? (
          <Alert variant="destructive">
            <AlertTitle>Could not load tokens</AlertTitle>
            <AlertDescription>{(list.error as Error).message}</AlertDescription>
          </Alert>
        ) : list.data && list.data.length > 0 ? (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-24">Last 4</TableHead>
                <TableHead>Allowed kinds</TableHead>
                <TableHead>Scope</TableHead>
                <TableHead className="w-32">Issued</TableHead>
                <TableHead className="w-32">Last used</TableHead>
                <TableHead className="w-32 text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.data.map((c) => {
                // Slice 163: row id is prefixed with `token-row-` so the
                // predecessor badge's `href="#token-row-{successor.id}"`
                // cannot collide with an unrelated element on the page.
                const rowAnchor = `token-row-${c.id}`;
                const successor = successorByPredecessorId.get(c.id);
                return (
                  <TableRow
                    key={c.id}
                    id={rowAnchor}
                    data-testid="settings-token-row"
                  >
                    <TableCell className="font-mono text-xs">
                      {c.last4}
                      {successor ? (
                        <a
                          href={`#token-row-${successor.id}`}
                          className="ml-2 inline-flex items-center rounded bg-muted px-1.5 py-0.5 text-[10px] font-normal text-muted-foreground hover:bg-muted-foreground/10"
                          data-testid="settings-token-rotated-to-link"
                          title={`Rotated to successor ending in ${successor.last4}`}
                        >
                          rotated {"->"} …{successor.last4}
                        </a>
                      ) : null}
                    </TableCell>
                    <TableCell className="text-xs">
                      {isAnyKind(c.allowed_kinds) ? (
                        <span className="text-muted-foreground">any</span>
                      ) : (
                        kindsLabel(c.allowed_kinds)
                      )}
                    </TableCell>
                    <TableCell className="font-mono text-[10px] text-muted-foreground">
                      {c.scope_predicate || "{}"}
                    </TableCell>
                    <TableCell className="font-mono text-xs">
                      {c.issued_at.slice(0, 10)}
                    </TableCell>
                    <TableCell className="font-mono text-xs">
                      {c.last_used_at ? (
                        c.last_used_at.slice(0, 10)
                      ) : (
                        <span className="text-muted-foreground">never</span>
                      )}
                    </TableCell>
                    <TableCell className="text-right">
                      <div className="flex justify-end gap-2">
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() => setRotateConfirm(c)}
                          data-testid="settings-token-rotate-button"
                        >
                          Rotate
                        </Button>
                        <Button
                          size="sm"
                          variant="destructive"
                          onClick={() => setRevokeConfirm(c)}
                          data-testid="settings-token-revoke-button"
                        >
                          Revoke
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        ) : (
          <p className="py-6 text-center text-sm text-muted-foreground">
            No active tokens. Click <strong>Issue token</strong> above to mint
            one.
          </p>
        )}
      </CardContent>

      {revokeConfirm ? (
        <RevokeConfirmModal
          cred={revokeConfirm}
          submitting={revokeMut.isPending}
          onCancel={() => setRevokeConfirm(null)}
          onConfirm={() => revokeMut.mutate(revokeConfirm.id)}
        />
      ) : null}

      {rotateConfirm ? (
        <RotateConfirmModal
          cred={rotateConfirm}
          submitting={rotateMut.isPending}
          onCancel={() => setRotateConfirm(null)}
          onConfirm={() =>
            rotateMut.mutate({
              id: rotateConfirm.id,
              predecessor_last4: rotateConfirm.last4,
            })
          }
        />
      ) : null}
    </Card>
  );
}

// Slice 103 / Slice 163 -- one-shot plaintext callout used by both the
// ISSUED and ROTATED reducer paths. The variant prop selects the copy
// (title, "issued at" vs "rotated -- predecessor retires at") without
// duplicating the surrounding callout chrome. The bearer flows in as a
// string and is rendered into a <code> element inside the callout's
// JSX -- the moment the parent component dispatches DISMISS, the
// callout unmounts and the bearer reference goes out of scope. There
// is no DOM persistence across re-renders, no localStorage write, no
// hidden duplicate element.
type FreshTokenCalloutProps =
  | {
      variant: "issued";
      bearer: string;
      last4: string;
      issuedAt: string;
      onDismiss: () => void;
    }
  | {
      variant: "rotated";
      bearer: string;
      last4: string;
      predecessorLast4: string;
      predecessorExpiresAt: string;
      onDismiss: () => void;
    };

function FreshTokenCallout(props: FreshTokenCalloutProps) {
  const title =
    props.variant === "issued"
      ? "API token issued -- copy it now"
      : "API token rotated -- copy the new bearer now";
  const helperParagraph =
    props.variant === "issued"
      ? "This is the only time you'll see this token. The platform does not store it in plaintext; if you lose it, issue a new one."
      : `This is the only time you'll see this token. The predecessor ending in ${props.predecessorLast4} keeps working until the timestamp below; rotate again or revoke it once your clients have switched over.`;
  return (
    <Alert variant="destructive" data-testid="settings-fresh-token-callout">
      <AlertTitle data-testid="settings-fresh-token-title">{title}</AlertTitle>
      <AlertDescription className="space-y-2">
        <p className="font-medium">{helperParagraph}</p>
        <div className="flex flex-col gap-2 sm:flex-row">
          <code
            className="flex-1 break-all rounded bg-foreground/5 p-2 font-mono text-xs"
            data-testid="settings-fresh-token-bearer"
          >
            {props.bearer}
          </code>
          <div className="flex gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() => {
                if (typeof navigator !== "undefined") {
                  navigator.clipboard?.writeText(props.bearer);
                }
              }}
            >
              Copy
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={props.onDismiss}
              data-testid="settings-fresh-token-dismiss"
            >
              Dismiss
            </Button>
          </div>
        </div>
        {props.variant === "issued" ? (
          <p className="text-xs">
            Last 4: <code>{props.last4}</code> &middot; Issued at{" "}
            <code>{props.issuedAt}</code>
          </p>
        ) : (
          <p
            className="text-xs"
            data-testid="settings-fresh-token-rotated-meta"
          >
            Successor last 4: <code>{props.last4}</code> &middot; Rotated from{" "}
            <code>…{props.predecessorLast4}</code> &middot; Predecessor retires
            at <code>{props.predecessorExpiresAt}</code>
          </p>
        )}
      </AlertDescription>
    </Alert>
  );
}

function IssueTokenForm({
  submitting,
  onCancel,
  onSubmit,
}: {
  submitting: boolean;
  onCancel: () => void;
  onSubmit: (body: AdminCredentialIssueRequest) => void;
}) {
  const [scopePredicate, setScopePredicate] = useState("");
  const [allowedKinds, setAllowedKinds] = useState("");
  const [ttlDays, setTtlDays] = useState(90);

  function submit() {
    const kinds = allowedKinds
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);
    onSubmit({
      scope_predicate: scopePredicate.trim() || "{}",
      allowed_kinds: kinds,
      ttl_seconds: ttlDays * 86400,
      is_admin: false,
      is_approver: false,
      owner_roles: [],
    });
  }

  return (
    <Card data-testid="settings-token-issue-form">
      <CardHeader>
        <CardTitle className="text-base">Issue a new personal token</CardTitle>
        <CardDescription>
          Scope narrows which evidence kinds and scope cells the bearer can push
          to. TTL is in days.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="grid gap-3 sm:grid-cols-2">
          <label className="space-y-1.5 text-sm">
            <span className="font-medium">Scope predicate (JSON)</span>
            <Input
              value={scopePredicate}
              onChange={(e) => setScopePredicate(e.target.value)}
              placeholder='{"connector":"aws"}'
            />
          </label>
          <label className="space-y-1.5 text-sm">
            <span className="font-medium">Allowed kinds (comma-separated)</span>
            <Input
              value={allowedKinds}
              onChange={(e) => setAllowedKinds(e.target.value)}
              placeholder="aws.s3.encryption.v1"
            />
          </label>
          <label className="space-y-1.5 text-sm">
            <span className="font-medium">TTL (days)</span>
            <Input
              type="number"
              min={1}
              max={3650}
              value={ttlDays}
              onChange={(e) => setTtlDays(Number(e.target.value))}
            />
          </label>
        </div>
        <div className="flex justify-end gap-2">
          <Button variant="outline" onClick={onCancel} disabled={submitting}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={submitting}>
            {submitting ? "Issuing..." : "Issue token"}
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

function RevokeConfirmModal({
  cred,
  submitting,
  onCancel,
  onConfirm,
}: {
  cred: AdminCredential;
  submitting: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  return (
    <div
      role="dialog"
      aria-modal="true"
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
      data-testid="settings-token-revoke-modal"
    >
      <Card className="w-full max-w-md">
        <CardHeader>
          <CardTitle>Revoke token?</CardTitle>
          <CardDescription>
            Last 4 <code>{cred.last4}</code>
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3 text-sm">
          <p>
            Revocation is immediate. Any client using this bearer will start
            failing on its next call. Last seen:{" "}
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
              {submitting ? "Revoking..." : "Revoke now"}
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

// Slice 163: RotateConfirmModal is a parallel of RevokeConfirmModal
// with rotate-specific copy. Rotation produces a NEW plaintext bearer
// for the successor row; the predecessor row stays visible with a
// muted "rotated -> ...last4" badge until the user separately revokes
// it (slice 062 D-062-3). The modal copy makes this explicit so the
// user is not surprised by the predecessor row sticking around.
function RotateConfirmModal({
  cred,
  submitting,
  onCancel,
  onConfirm,
}: {
  cred: AdminCredential;
  submitting: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  return (
    <div
      role="dialog"
      aria-modal="true"
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
      data-testid="settings-token-rotate-modal"
    >
      <Card className="w-full max-w-md">
        <CardHeader>
          <CardTitle>Rotate token?</CardTitle>
          <CardDescription>
            Last 4 <code>{cred.last4}</code>
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3 text-sm">
          <p>
            Rotation mints a successor with the same scope and allowed kinds,
            and returns a fresh bearer plaintext. The predecessor keeps working
            for a short grace window so clients can switch over -- it stays
            visible in this list with a muted &ldquo;rotated&rdquo; badge until
            you revoke it.
          </p>
          <p>
            You&apos;ll see the new bearer EXACTLY ONCE. Have a place to paste
            it before continuing.
          </p>
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={onCancel} disabled={submitting}>
              Cancel
            </Button>
            <Button onClick={onConfirm} disabled={submitting}>
              {submitting ? "Rotating..." : "Rotate now"}
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
