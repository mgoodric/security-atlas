"use client";

// #2341 — set a metric's target from its detail page.
//
// PUT /v1/metrics/{id}/target was API-only before this. The dialog is
// offered only to admins (`canSetMetricTarget`, the platform's
// `authz.IsTenantAdmin` guard); the platform refuses anyone else. On
// success the target query is refetched, so the badge, the chart overlays
// and the board card all grade against the new target.

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useId, useState } from "react";

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
import { Select } from "@/components/ui/select";
import {
  metricTargetError,
  metricTargetInputFromStored,
  metricTargetStoredFromInput,
  upsertMetricTarget,
  type MetricTarget,
} from "@/lib/api/metrics";
import { POSTURE_DIRECTIONS } from "@/lib/api/posture-targets";

type Fields = {
  target: string;
  warning: string;
  critical: string;
  direction: string;
  notes: string;
};

function initialFields(current: MetricTarget | null, unit: string): Fields {
  return {
    target: metricTargetInputFromStored(current?.target_value, unit),
    warning: metricTargetInputFromStored(current?.warning_threshold, unit),
    critical: metricTargetInputFromStored(current?.critical_threshold, unit),
    direction: current?.direction ?? "higher_is_better",
    notes: current?.notes ?? "",
  };
}

const num = (s: string) => (s.trim() === "" ? Number.NaN : Number(s));

export function MetricTargetDialog({
  metricID,
  metricName,
  unit,
  current,
}: {
  metricID: string;
  metricName: string;
  unit: string;
  current: MetricTarget | null;
}) {
  const [open, setOpen] = useState(false);
  const [fields, setFields] = useState<Fields>(() =>
    initialFields(current, unit),
  );
  const queryClient = useQueryClient();
  const ids = {
    target: useId(),
    warning: useId(),
    critical: useId(),
    direction: useId(),
    notes: useId(),
    hint: useId(),
  };

  const input = {
    target_value: num(fields.target),
    warning_threshold: num(fields.warning),
    critical_threshold: num(fields.critical),
    direction: fields.direction,
  };
  const problem = metricTargetError(input, unit);

  const mutation = useMutation({
    mutationFn: () =>
      upsertMetricTarget(metricID, {
        direction: input.direction,
        target_value: metricTargetStoredFromInput(input.target_value, unit),
        warning_threshold: metricTargetStoredFromInput(
          input.warning_threshold,
          unit,
        ),
        critical_threshold: metricTargetStoredFromInput(
          input.critical_threshold,
          unit,
        ),
        notes: fields.notes.trim() === "" ? undefined : fields.notes.trim(),
      }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({
        queryKey: ["metric-target", metricID],
      });
      setOpen(false);
    },
  });

  const set = (k: keyof Fields) => (v: string) =>
    setFields((f) => ({ ...f, [k]: v }));

  // A percent metric records fractions (0..1) but is entered as a
  // percentage; the helpers convert both ways.
  const percent = unit === "percent";
  const unitSuffix = percent ? " (%)" : "";

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        // Re-seed from the saved target each time the dialog opens.
        if (next) setFields(initialFields(current, unit));
        mutation.reset();
        setOpen(next);
      }}
    >
      <DialogTrigger
        render={(props) => (
          <Button
            {...props}
            variant="outline"
            size="sm"
            data-testid="metric-target-set"
          >
            {current ? "Edit target" : "Set target"}
          </Button>
        )}
      />
      <DialogPortal>
        <DialogContent data-testid="metric-target-dialog">
          <DialogHeader>
            <DialogTitle>Target: {metricName}</DialogTitle>
            <DialogDescription>
              The value your organisation aims for. The badge turns amber at the
              warning threshold and red at the critical one.
            </DialogDescription>
          </DialogHeader>

          <form
            onSubmit={(e) => {
              e.preventDefault();
              if (!problem) mutation.mutate();
            }}
            className="mt-4 space-y-3"
          >
            <div className="flex flex-col gap-1 text-sm">
              <label htmlFor={ids.direction} className="font-medium">
                Direction
              </label>
              <Select
                id={ids.direction}
                value={fields.direction}
                onChange={(e) => set("direction")(e.target.value)}
                data-testid="metric-target-direction"
              >
                {POSTURE_DIRECTIONS.map((d) => (
                  <option key={d.value} value={d.value}>
                    {d.label}
                  </option>
                ))}
              </Select>
            </div>
            {(
              [
                ["target", "Target"],
                ["warning", "Warning threshold"],
                ["critical", "Critical threshold"],
              ] as const
            ).map(([k, label]) => (
              <div key={k} className="flex flex-col gap-1 text-sm">
                <label htmlFor={ids[k]} className="font-medium">
                  {label}
                  {unitSuffix}
                </label>
                <Input
                  id={ids[k]}
                  type="number"
                  inputMode="decimal"
                  min={percent ? 0 : undefined}
                  max={percent ? 100 : undefined}
                  step="any"
                  value={fields[k]}
                  onChange={(e) => set(k)(e.target.value)}
                  required
                  aria-describedby={ids.hint}
                  data-testid={`metric-target-${k}`}
                />
              </div>
            ))}
            <p
              id={ids.hint}
              className="text-xs text-muted-foreground"
              data-testid="metric-target-hint"
            >
              {percent
                ? "Enter a percentage from 0 to 100, so 90 is 90%."
                : `Enter the value in the metric's unit (${unit}).`}
            </p>
            <div className="flex flex-col gap-1 text-sm">
              <label htmlFor={ids.notes} className="font-medium">
                Notes (optional)
              </label>
              <Input
                id={ids.notes}
                value={fields.notes}
                onChange={(e) => set("notes")(e.target.value)}
                data-testid="metric-target-notes"
              />
            </div>

            {problem ? (
              <p
                className="text-sm text-destructive"
                role="status"
                data-testid="metric-target-problem"
              >
                {problem}
              </p>
            ) : null}

            {mutation.isError ? (
              <Alert variant="destructive">
                <AlertTitle>Target not saved</AlertTitle>
                <AlertDescription>
                  {(mutation.error as Error)?.message ?? "Unknown error"}
                </AlertDescription>
              </Alert>
            ) : null}

            <DialogFooter>
              <DialogCancelButton />
              <Button
                type="submit"
                disabled={problem !== null || mutation.isPending}
                data-testid="metric-target-save"
              >
                {mutation.isPending ? "Saving…" : "Save target"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </DialogPortal>
    </Dialog>
  );
}
