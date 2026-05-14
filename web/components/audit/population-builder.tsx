// Slice 042 — population builder (AC-3 entry point).
//
// Creates a Population(control, scope_predicate, time_window) for the
// current control via POST /v1/populations. The platform computes
// row_count under the frozen-evidence horizon. Once a population exists
// the SamplePullForm becomes available.
//
// scope_predicate is left as the platform default (`{"op":"true"}`) in
// v1 — a richer scope-cell picker is a follow-up. The auditor sets the
// time window, which the platform intersects with the frozen horizon.

"use client";

import { useState } from "react";
import { useMutation } from "@tanstack/react-query";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { createPopulation, type Population } from "@/lib/api/audit";

export function PopulationBuilder({
  controlId,
  onCreated,
}: {
  controlId: string;
  onCreated: (population: Population) => void;
}) {
  const [start, setStart] = useState("");
  const [end, setEnd] = useState("");
  const [clientError, setClientError] = useState<string | null>(null);

  const mutation = useMutation({
    mutationFn: () =>
      createPopulation({
        control_id: controlId,
        time_window_start: new Date(start).toISOString(),
        time_window_end: new Date(end).toISOString(),
      }),
    onSuccess: (population) => onCreated(population),
  });

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setClientError(null);
    if (!start || !end) {
      setClientError("both window start and end are required");
      return;
    }
    if (new Date(start) > new Date(end)) {
      setClientError("window start must be on or before window end");
      return;
    }
    mutation.mutate();
  }

  const errorMsg =
    clientError ?? (mutation.error ? String(mutation.error.message) : null);

  return (
    <Card data-testid="population-builder" size="sm">
      <CardHeader>
        <CardTitle className="text-sm">Build a population</CardTitle>
      </CardHeader>
      <CardContent>
        <form onSubmit={handleSubmit} className="grid gap-2">
          <div className="flex flex-wrap items-end gap-2">
            <label className="grid gap-1 text-xs text-muted-foreground">
              Window start
              <Input
                type="date"
                value={start}
                onChange={(e) => setStart(e.target.value)}
                data-testid="population-start"
                className="w-40"
              />
            </label>
            <label className="grid gap-1 text-xs text-muted-foreground">
              Window end
              <Input
                type="date"
                value={end}
                onChange={(e) => setEnd(e.target.value)}
                data-testid="population-end"
                className="w-40"
              />
            </label>
            <Button
              type="submit"
              disabled={mutation.isPending}
              data-testid="population-create"
            >
              {mutation.isPending ? "Building…" : "Build population"}
            </Button>
          </div>
          {errorMsg ? (
            <Alert variant="destructive" data-testid="population-error">
              <AlertDescription>{errorMsg}</AlertDescription>
            </Alert>
          ) : null}
        </form>
      </CardContent>
    </Card>
  );
}
