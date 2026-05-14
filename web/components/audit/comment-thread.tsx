// Slice 042 — Audit Hub comment thread on a control (AC-5, P0-2).
//
// Renders the threaded notes for a (scope_type=control, scope_id, period)
// anchor via GET /v1/audit-notes/thread, and a compose form that POSTs
// /v1/audit-notes.
//
// P0-2 (auditee cannot read auditor's private notes): the platform
// filters `auditor_only` notes to their author AT THE QUERY LAYER. This
// component renders EXACTLY what the thread endpoint returns — there is
// NO client-side visibility filter. A note that should be hidden never
// arrives in the response, so there is nothing to filter. The
// `auditor_only` vs `shared` styling here is purely a visual cue for the
// viewer; it is not a security boundary.
//
// AC-5 also requires auditor vs auditee comments to be visually
// distinguished. We mark a note as "yours" when its author matches the
// caller (passed in via `callerUserId`) and otherwise show the author id;
// combined with the visibility badge this distinguishes the two sides of
// the thread.

"use client";

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";
import {
  createNote,
  getNoteThread,
  type AuditNote,
  type NoteVisibility,
} from "@/lib/api/audit";

export function CommentThread({
  auditPeriodId,
  controlId,
  callerUserId,
}: {
  auditPeriodId: string;
  controlId: string;
  callerUserId?: string;
}) {
  const queryClient = useQueryClient();
  const [body, setBody] = useState("");
  const [visibility, setVisibility] = useState<NoteVisibility>("shared");

  const threadKey = ["audit", "notes", auditPeriodId, "control", controlId];

  const thread = useQuery({
    queryKey: threadKey,
    queryFn: () =>
      getNoteThread({
        audit_period_id: auditPeriodId,
        scope_type: "control",
        scope_id: controlId,
      }),
  });

  const mutation = useMutation({
    mutationFn: () =>
      createNote({
        audit_period_id: auditPeriodId,
        scope_type: "control",
        scope_id: controlId,
        body,
        visibility,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: threadKey });
      setBody("");
    },
  });

  return (
    <div data-testid="comment-thread" className="grid gap-3">
      <p className="text-xs font-medium tracking-wide text-muted-foreground uppercase">
        Audit Hub — control thread
      </p>

      {thread.isLoading ? (
        <div className="grid gap-2">
          <Skeleton className="h-12 w-full" />
          <Skeleton className="h-12 w-full" />
        </div>
      ) : thread.error ? (
        <Alert variant="destructive" data-testid="comment-thread-error">
          <AlertDescription>
            {String((thread.error as Error).message)}
          </AlertDescription>
        </Alert>
      ) : thread.data && thread.data.length > 0 ? (
        <ul className="grid gap-2">
          {thread.data.map((note) => (
            <CommentItem
              key={note.id}
              note={note}
              callerUserId={callerUserId}
            />
          ))}
        </ul>
      ) : (
        <p
          data-testid="comment-thread-empty"
          className="text-sm text-muted-foreground"
        >
          No comments on this control yet.
        </p>
      )}

      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (body.trim() === "") return;
          mutation.mutate();
        }}
        className="grid gap-2"
      >
        <textarea
          value={body}
          onChange={(e) => setBody(e.target.value)}
          data-testid="comment-body"
          placeholder="Comment on this control…"
          rows={3}
          className="w-full rounded-lg border border-input bg-transparent px-2.5 py-1.5 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50"
        />
        <div className="flex flex-wrap items-center gap-2">
          <label className="flex items-center gap-1.5 text-xs text-muted-foreground">
            Visibility
            <select
              data-testid="comment-visibility"
              value={visibility}
              onChange={(e) => setVisibility(e.target.value as NoteVisibility)}
              className="h-8 rounded-lg border border-input bg-transparent px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50"
            >
              <option value="shared">shared (auditee can see)</option>
              <option value="auditor_only">
                auditor only (private testing note)
              </option>
            </select>
          </label>
          <Button
            type="submit"
            size="sm"
            disabled={mutation.isPending || body.trim() === ""}
            data-testid="comment-submit"
          >
            {mutation.isPending ? "Posting…" : "Post comment"}
          </Button>
        </div>
        {mutation.error ? (
          <Alert variant="destructive" data-testid="comment-error">
            <AlertDescription>
              {String(mutation.error.message)}
            </AlertDescription>
          </Alert>
        ) : null}
      </form>
    </div>
  );
}

function CommentItem({
  note,
  callerUserId,
}: {
  note: AuditNote;
  callerUserId?: string;
}) {
  const mine =
    callerUserId !== undefined && note.author_user_id === callerUserId;
  const privateNote = note.visibility === "auditor_only";
  return (
    <li
      data-testid="comment-item"
      data-visibility={note.visibility}
      data-mine={mine ? "true" : "false"}
      className={cn(
        "grid gap-1 rounded-md border p-2.5 text-sm",
        // Visual distinction: the caller's own notes sit flush-right-tinted;
        // the other side of the thread sits plain. Private (auditor_only)
        // notes get a dashed border + amber tint as a "not shared" cue.
        mine ? "bg-primary/5" : "bg-muted/30",
        privateNote && "border-dashed border-amber-500/50 bg-amber-500/5",
      )}
      style={{ marginLeft: note.depth ? note.depth * 16 : 0 }}
    >
      <div className="flex items-center gap-2">
        <span className="text-xs font-medium">
          {mine ? "You" : note.author_user_id}
        </span>
        {privateNote ? (
          <Badge variant="outline" data-testid="comment-private-badge">
            auditor only
          </Badge>
        ) : (
          <Badge variant="secondary" data-testid="comment-shared-badge">
            shared
          </Badge>
        )}
        <span className="text-xs text-muted-foreground tabular-nums">
          {note.created_at}
        </span>
      </div>
      <p className="whitespace-pre-wrap">{note.body}</p>
    </li>
  );
}
