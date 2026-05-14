// Slice 042 — per-evidence-record annotation form (AC-3, AC-7, P0-3).
//
// One annotation form per (sample, evidence_record) pair. The form's
// in-progress state lives in the AnnotationDraftProvider context (mounted
// above the workspace tabs), NOT in this component's local state — that
// is what makes a half-typed annotation survive a tab switch (AC-7 / P0-3).
//
// On submit it POSTs /v1/samples/{id}/annotations and invalidates the
// annotations query so the saved annotation list re-fetches. On success
// the draft is cleared from the store.

"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Alert, AlertDescription } from "@/components/ui/alert";
import {
  annotateSample,
  type AnnotationResult,
  type Annotation,
} from "@/lib/api/audit";
import { useAnnotationDrafts } from "@/components/audit/annotation-draft-store";

const RESULT_OPTIONS: AnnotationResult[] = [
  "passed",
  "failed",
  "not-applicable",
];

export function SampleAnnotation({
  sampleId,
  evidenceRecordId,
  existing,
}: {
  sampleId: string;
  evidenceRecordId: string;
  existing?: Annotation;
}) {
  const drafts = useAnnotationDrafts();
  const draft = drafts.getDraft(sampleId, evidenceRecordId);
  const queryClient = useQueryClient();

  const mutation = useMutation({
    mutationFn: () => {
      if (draft.result === "") {
        throw new Error("result is required");
      }
      return annotateSample(sampleId, {
        evidence_record_id: evidenceRecordId,
        result: draft.result,
        notes: draft.notes,
      });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: ["audit", "sample", sampleId, "annotations"],
      });
      drafts.clearDraft(sampleId, evidenceRecordId);
    },
  });

  const dirty = drafts.hasDraft(sampleId, evidenceRecordId);

  return (
    <div
      data-testid="sample-annotation"
      data-evidence-record-id={evidenceRecordId}
      className="grid gap-1.5 rounded-md border p-2.5"
    >
      <div className="flex items-center justify-between gap-2">
        <code className="truncate text-xs text-muted-foreground">
          {evidenceRecordId}
        </code>
        {existing ? (
          <span
            data-testid="annotation-saved-result"
            className="text-xs font-medium"
          >
            saved: {existing.result}
          </span>
        ) : dirty ? (
          <span
            data-testid="annotation-draft-indicator"
            className="text-xs text-muted-foreground"
          >
            unsaved draft
          </span>
        ) : null}
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <select
          data-testid="annotation-result-select"
          value={draft.result}
          onChange={(e) =>
            drafts.setDraft(sampleId, evidenceRecordId, {
              ...draft,
              result: e.target.value as AnnotationResult | "",
            })
          }
          className="h-8 rounded-lg border border-input bg-transparent px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50"
        >
          <option value="">result…</option>
          {RESULT_OPTIONS.map((r) => (
            <option key={r} value={r}>
              {r}
            </option>
          ))}
        </select>
        <Input
          type="text"
          value={draft.notes}
          onChange={(e) =>
            drafts.setDraft(sampleId, evidenceRecordId, {
              ...draft,
              notes: e.target.value,
            })
          }
          data-testid="annotation-notes-input"
          placeholder="testing notes"
          className="min-w-40 flex-1"
        />
        <Button
          type="button"
          size="sm"
          disabled={mutation.isPending || draft.result === ""}
          onClick={() => mutation.mutate()}
          data-testid="annotation-submit"
        >
          {mutation.isPending ? "Saving…" : existing ? "Update" : "Save"}
        </Button>
      </div>
      {mutation.error ? (
        <Alert variant="destructive" data-testid="annotation-error">
          <AlertDescription>
            {String(mutation.error.message)}
          </AlertDescription>
        </Alert>
      ) : null}
    </div>
  );
}
