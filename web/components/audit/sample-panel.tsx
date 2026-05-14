// Slice 042 — sample panel (AC-3): population summary + sample-pull +
// pulled samples with per-record annotation forms.
//
// Composition:
//   PopulationBuilder -> on create -> PopulationSummary + SamplePullForm
//   SamplePullForm    -> on draw   -> a Sample card per drawn sample
//   each Sample card  -> one SampleAnnotation per evidence_record_id
//
// The drawn samples live in this component's local state list. The
// per-record annotation DRAFTS, however, live in the AnnotationDraft
// context above the workspace tabs — so they survive a tab switch
// (AC-7 / P0-3). This component only owns "which samples have been
// drawn", which is cheap to re-derive and not user-typed content.

"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import {
  listAnnotations,
  type Population,
  type Sample,
  type Annotation,
} from "@/lib/api/audit";
import { PopulationBuilder } from "@/components/audit/population-builder";
import { PopulationSummary } from "@/components/audit/population-summary";
import { SamplePullForm } from "@/components/audit/sample-pull-form";
import { SampleAnnotation } from "@/components/audit/sample-annotation";

export function SamplePanel({ controlId }: { controlId: string }) {
  const [population, setPopulation] = useState<Population | null>(null);
  const [samples, setSamples] = useState<Sample[]>([]);

  return (
    <div data-testid="sample-panel" className="grid gap-4">
      {!population ? (
        <PopulationBuilder controlId={controlId} onCreated={setPopulation} />
      ) : (
        <>
          <PopulationSummary population={population} />
          <Card size="sm">
            <CardHeader>
              <CardTitle className="text-sm">Pull a sample</CardTitle>
            </CardHeader>
            <CardContent>
              <SamplePullForm
                populationId={population.id}
                onSampleDrawn={(s) => setSamples((prev) => [s, ...prev])}
              />
            </CardContent>
          </Card>
          {samples.map((sample) => (
            <SampleCard key={sample.id} sample={sample} />
          ))}
        </>
      )}
    </div>
  );
}

function SampleCard({ sample }: { sample: Sample }) {
  // Re-fetch saved annotations so an annotated record shows its saved
  // result; the draft store handles the unsaved-in-progress state.
  const annotations = useQuery({
    queryKey: ["audit", "sample", sample.id, "annotations"],
    queryFn: () => listAnnotations(sample.id),
  });

  const savedByRecord = new Map<string, Annotation>();
  for (const a of annotations.data ?? []) {
    savedByRecord.set(a.evidence_record_id, a);
  }

  return (
    <Card data-testid="sample-card" data-sample-id={sample.id} size="sm">
      <CardHeader>
        <div className="flex flex-wrap items-center gap-2">
          <CardTitle className="text-sm">Sample</CardTitle>
          <Badge variant="outline">n={sample.n}</Badge>
          <Badge variant="outline">seed: {sample.seed}</Badge>
        </div>
      </CardHeader>
      <CardContent className="grid gap-2">
        {sample.evidence_record_ids.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No evidence records in this sample.
          </p>
        ) : (
          sample.evidence_record_ids.map((recordId) => (
            <SampleAnnotation
              key={recordId}
              sampleId={sample.id}
              evidenceRecordId={recordId}
              existing={savedByRecord.get(recordId)}
            />
          ))
        )}
      </CardContent>
    </Card>
  );
}
