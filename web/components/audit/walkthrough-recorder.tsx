// Slice 042 — walkthrough recorder (AC-4).
//
// Two-step recorder matching slice 027's contract:
//   1. Create the walkthrough with a narrative (POST /v1/walkthroughs,
//      status=draft). The platform returns the record + canonical_hash.
//   2. Optionally attach a file (POST /v1/walkthroughs/{id}/attachments,
//      multipart). The platform re-hashes over the new attachment set.
//
// ROLE TENSION (surfaced to orchestrator): slice 027's handler gates
// writes on `IsAdmin OR grc_engineer`. Canvas §8.3 says a walkthrough is
// an "auditor OR owner" recorded explanation — so a pure `auditor`
// credential receives a 403 here. That is a backend role gap, not a
// frontend bug. ISC-35: this component surfaces the upstream 403 as a
// clear, actionable message rather than a raw error.

"use client";

import { useState } from "react";
import { useMutation } from "@tanstack/react-query";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Alert,
  AlertDescription,
  AlertTitle,
} from "@/components/ui/alert";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  createWalkthrough,
  uploadWalkthroughAttachment,
  AuditAPIError,
  type Walkthrough,
} from "@/lib/api/audit";

function describeError(err: unknown): { title: string; body: string } {
  if (err instanceof AuditAPIError && err.status === 403) {
    return {
      title: "Walkthrough authoring is not permitted for this role",
      body:
        "The platform currently restricts walkthrough creation to admin / grc_engineer credentials. " +
        "Canvas §8.3 expects auditors to author walkthroughs too — this is a tracked backend role gap. " +
        "Ask a GRC engineer to record this walkthrough, or use an admin credential.",
    };
  }
  const msg =
    err instanceof Error ? err.message : "unexpected error saving walkthrough";
  return { title: "Could not save walkthrough", body: msg };
}

export function WalkthroughRecorder({
  controlId,
  auditPeriodId,
}: {
  controlId: string;
  auditPeriodId: string;
}) {
  const [narrative, setNarrative] = useState("");
  const [walkthrough, setWalkthrough] = useState<Walkthrough | null>(null);
  const [file, setFile] = useState<File | null>(null);

  const createMutation = useMutation({
    mutationFn: () =>
      createWalkthrough({
        control_id: controlId,
        audit_period_id: auditPeriodId,
        narrative,
      }),
    onSuccess: (wt) => {
      setWalkthrough(wt);
      setNarrative("");
    },
  });

  const attachMutation = useMutation({
    mutationFn: () => {
      if (!walkthrough) throw new Error("create the walkthrough first");
      if (!file) throw new Error("choose a file to attach");
      return uploadWalkthroughAttachment(walkthrough.id, file);
    },
    onSuccess: (wt) => {
      setWalkthrough(wt);
      setFile(null);
    },
  });

  return (
    <Card data-testid="walkthrough-recorder" size="sm">
      <CardHeader>
        <CardTitle className="text-sm">Walkthrough</CardTitle>
      </CardHeader>
      <CardContent className="grid gap-3">
        {!walkthrough ? (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              if (narrative.trim() === "") return;
              createMutation.mutate();
            }}
            className="grid gap-2"
          >
            <textarea
              value={narrative}
              onChange={(e) => setNarrative(e.target.value)}
              data-testid="walkthrough-narrative"
              placeholder="Describe the walkthrough — what the control owner demonstrated, what you observed…"
              rows={4}
              className="w-full rounded-lg border border-input bg-transparent px-2.5 py-1.5 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50"
            />
            <div>
              <Button
                type="submit"
                disabled={
                  createMutation.isPending || narrative.trim() === ""
                }
                data-testid="walkthrough-save"
              >
                {createMutation.isPending ? "Saving…" : "Record walkthrough"}
              </Button>
            </div>
            {createMutation.error ? (
              <WalkthroughError err={createMutation.error} />
            ) : null}
          </form>
        ) : (
          <div className="grid gap-3">
            <div className="grid gap-1 rounded-md border p-2.5 text-sm">
              <span className="text-xs text-muted-foreground">
                Walkthrough recorded — status {walkthrough.status}
              </span>
              <div className="flex justify-between gap-4">
                <span className="text-muted-foreground">canonical hash</span>
                <code
                  data-testid="walkthrough-hash"
                  className="truncate text-xs"
                >
                  {walkthrough.canonical_hash}
                </code>
              </div>
              {walkthrough.attachments &&
              walkthrough.attachments.length > 0 ? (
                <span
                  data-testid="walkthrough-attachment-count"
                  className="text-xs text-muted-foreground"
                >
                  {walkthrough.attachments.length} attachment(s)
                </span>
              ) : null}
            </div>
            <div className="grid gap-2">
              <label className="text-xs text-muted-foreground">
                Attach a screen capture or transcript
              </label>
              <div className="flex flex-wrap items-center gap-2">
                <Input
                  type="file"
                  data-testid="walkthrough-file"
                  onChange={(e) =>
                    setFile(e.target.files?.[0] ?? null)
                  }
                  className="max-w-xs"
                />
                <Button
                  type="button"
                  size="sm"
                  disabled={attachMutation.isPending || !file}
                  onClick={() => attachMutation.mutate()}
                  data-testid="walkthrough-attach"
                >
                  {attachMutation.isPending ? "Uploading…" : "Attach"}
                </Button>
              </div>
              {attachMutation.error ? (
                <WalkthroughError err={attachMutation.error} />
              ) : null}
            </div>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function WalkthroughError({ err }: { err: unknown }) {
  const { title, body } = describeError(err);
  return (
    <Alert variant="destructive" data-testid="walkthrough-error">
      <AlertTitle>{title}</AlertTitle>
      <AlertDescription>{body}</AlertDescription>
    </Alert>
  );
}
