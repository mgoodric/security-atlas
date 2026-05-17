"use client";

// Slice 097 — manual-input modal (AC-11).
//
// Renders a shadcn Dialog with a numeric `value` field, optional notes,
// and submits via POST `/v1/metrics/{id}/inputs`. Admin-gated trigger
// (decision D3): the parent only renders this component when the
// session probe (`getSessionMe().is_admin`) returns true. On success
// the parent's observation query is invalidated so the new value
// appears in the series.

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogCancelButton,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogPortal,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { submitInput } from "@/lib/api/metrics";

export function ManualInputModal({
  metricID,
  metricName,
  unit,
}: {
  metricID: string;
  metricName: string;
  unit: string;
}) {
  const [open, setOpen] = useState(false);
  const [value, setValue] = useState("");
  const [notes, setNotes] = useState("");
  const queryClient = useQueryClient();

  const mutation = useMutation({
    mutationFn: () =>
      submitInput(metricID, {
        numeric_value: Number(value),
        notes: notes || undefined,
      }),
    onSuccess: () => {
      // Refetch the per-metric observations + the dashboard sparkline.
      queryClient.invalidateQueries({
        queryKey: ["metric-observations", metricID],
      });
      // Reset form + close.
      setValue("");
      setNotes("");
      setOpen(false);
    },
  });

  const numericValid = value !== "" && Number.isFinite(Number(value));

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger
        render={(props) => (
          <Button {...props} data-testid={`manual-input-trigger-${metricID}`}>
            Submit value
          </Button>
        )}
      />
      <DialogPortal>
        <DialogContent data-testid={`manual-input-modal-${metricID}`}>
          <DialogHeader>
            <DialogTitle>Submit value — {metricName}</DialogTitle>
            <DialogDescription>
              The value will be appended to the audit-trail of manual inputs for
              this metric. Unit: <code className="font-mono">{unit}</code>.
            </DialogDescription>
          </DialogHeader>

          <form
            onSubmit={(e) => {
              e.preventDefault();
              if (numericValid) mutation.mutate();
            }}
            className="mt-4 space-y-3"
          >
            <label className="flex flex-col gap-1 text-sm">
              <span className="font-medium">Value</span>
              <Input
                type="number"
                step="any"
                value={value}
                onChange={(e) => setValue(e.target.value)}
                required
                data-testid={`manual-input-value-${metricID}`}
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              <span className="font-medium">Notes (optional)</span>
              <Input
                type="text"
                value={notes}
                onChange={(e) => setNotes(e.target.value)}
                data-testid={`manual-input-notes-${metricID}`}
              />
            </label>

            {mutation.isError ? (
              <Alert variant="destructive">
                <AlertTitle>Submit failed</AlertTitle>
                <AlertDescription>
                  {(mutation.error as Error)?.message ?? "Unknown error"}
                </AlertDescription>
              </Alert>
            ) : null}

            <DialogFooter>
              <DialogCancelButton />
              <Button
                type="submit"
                disabled={!numericValid || mutation.isPending}
                data-testid={`manual-input-submit-${metricID}`}
              >
                {mutation.isPending ? "Submitting…" : "Submit"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </DialogPortal>
    </Dialog>
  );
}
