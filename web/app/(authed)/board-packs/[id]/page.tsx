"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { use, useState } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
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
  APIError,
  approveBoardPackSection,
  BOARD_PACK_SECTION_KEYS,
  BoardPack,
  BoardPackSection,
  BoardPackSectionInputs,
  getBoardPack,
  publishBoardPack,
  updateBoardPackSection,
} from "@/lib/api";

// Slice 032 — quarterly board pack review + approve + publish.
//
// The detail page walks the FIXED section keys in canonical order. For a
// DRAFT pack, each section is editable: the operator can override the
// templated narrative and (for the operator-entered sections) supply
// structured inputs, then approve the section. Publish is enabled only
// once every section is approved (decision D6). A PUBLISHED pack renders
// read-only — every edit/approve control is hidden (AC-7 immutability).

const OPERATOR_INPUT_SECTIONS = new Set([
  "operational_metrics",
  "investment",
  "coverage_trend",
]);

export default function BoardPackDetailPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  const queryClient = useQueryClient();

  const packQuery = useQuery({
    queryKey: ["board-pack", id],
    queryFn: () => getBoardPack(id),
  });

  if (packQuery.isLoading) {
    return (
      <div className="mx-auto max-w-4xl space-y-4 p-8">
        <Skeleton className="h-10 w-72" />
        <Skeleton className="h-40 w-full" />
        <Skeleton className="h-40 w-full" />
      </div>
    );
  }
  if (packQuery.isError || !packQuery.data) {
    return (
      <div className="mx-auto max-w-4xl p-8">
        <Alert variant="destructive">
          <AlertTitle>Could not load the board pack</AlertTitle>
          <AlertDescription>
            {packQuery.error instanceof APIError
              ? packQuery.error.message
              : "Unexpected error."}
          </AlertDescription>
        </Alert>
        <Link
          href="/board-packs"
          className="mt-4 inline-block text-sm underline"
        >
          Back to board packs
        </Link>
      </div>
    );
  }

  const pack = packQuery.data;
  const isPublished = pack.status === "published";
  const allApproved = BOARD_PACK_SECTION_KEYS.every(
    (key) => pack.content.sections[key]?.approved,
  );

  return (
    <div className="mx-auto max-w-4xl space-y-6 p-8">
      <div className="flex items-start justify-between">
        <div>
          <Link
            href="/board-packs"
            className="text-sm text-slate-500 underline"
          >
            ← Board packs
          </Link>
          <h1 className="mt-1 text-3xl font-semibold tracking-tight">
            Quarterly board pack — {pack.period_end}
          </h1>
          <p className="mt-1 text-slate-600">
            Generated {pack.content.generated_at}
          </p>
        </div>
        <Badge
          className={
            isPublished
              ? "bg-emerald-100 text-emerald-800 hover:bg-emerald-100"
              : "bg-amber-100 text-amber-800 hover:bg-amber-100"
          }
        >
          {pack.status}
        </Badge>
      </div>

      {BOARD_PACK_SECTION_KEYS.map((key, i) => {
        const section = pack.content.sections[key];
        if (!section) return null;
        return (
          <SectionCard
            key={key}
            index={i + 1}
            packID={id}
            section={section}
            isPublished={isPublished}
            onMutated={(updated) =>
              queryClient.setQueryData(["board-pack", id], updated)
            }
          />
        );
      })}

      <PublishCard
        packID={id}
        isPublished={isPublished}
        allApproved={allApproved}
        publishedBy={pack.published_by}
        onPublished={(updated) =>
          queryClient.setQueryData(["board-pack", id], updated)
        }
      />

      <Card>
        <CardHeader>
          <CardTitle>Export</CardTitle>
          <CardDescription>
            Download the pack as Markdown (paste into the deck) or PDF.
          </CardDescription>
        </CardHeader>
        <CardContent className="flex gap-3">
          <a
            href={`/v1/board-packs/${id}.md`}
            className="text-sm font-medium text-slate-900 underline"
          >
            Download Markdown
          </a>
          <a
            href={`/v1/board-packs/${id}/pdf`}
            className="text-sm font-medium text-slate-900 underline"
          >
            Download PDF
          </a>
        </CardContent>
      </Card>
    </div>
  );
}

function SectionCard({
  index,
  packID,
  section,
  isPublished,
  onMutated,
}: {
  index: number;
  packID: string;
  section: BoardPackSection;
  isPublished: boolean;
  onMutated: (pack: BoardPack) => void;
}) {
  const [overrideText, setOverrideText] = useState(section.override_text);
  const [inputs, setInputs] = useState<BoardPackSectionInputs>({});

  const save = useMutation({
    mutationFn: () =>
      updateBoardPackSection(packID, section.key, {
        override_text: overrideText,
        inputs: Object.keys(inputs).length > 0 ? inputs : undefined,
      }),
    onSuccess: onMutated,
  });

  const approve = useMutation({
    mutationFn: () => approveBoardPackSection(packID, section.key),
    onSuccess: onMutated,
  });

  const showInputs = OPERATOR_INPUT_SECTIONS.has(section.key);
  const effectiveText = section.override_text || section.templated_text;

  return (
    <Card>
      <CardHeader>
        <div className="flex items-center justify-between">
          <CardTitle className="text-lg">
            <span className="mr-2 font-mono text-xs text-slate-400">
              § {String(index).padStart(2, "0")}
            </span>
            {section.title}
          </CardTitle>
          <Badge
            className={
              section.approved
                ? "bg-emerald-100 text-emerald-800 hover:bg-emerald-100"
                : "bg-slate-100 text-slate-600 hover:bg-slate-100"
            }
          >
            {section.approved ? "approved" : "not approved"}
          </Badge>
        </div>
      </CardHeader>
      <CardContent className="space-y-4">
        {isPublished ? (
          <p className="whitespace-pre-wrap text-sm text-slate-700">
            {effectiveText}
          </p>
        ) : (
          <>
            <div className="space-y-1">
              <label className="text-sm font-medium text-slate-700">
                Narrative
              </label>
              <p className="text-xs text-slate-500">
                Templated text (no AI). Edit to override before approving.
              </p>
              <textarea
                className="min-h-28 w-full rounded-md border border-slate-300 p-2 text-sm"
                value={overrideText}
                placeholder={section.templated_text}
                onChange={(e) => setOverrideText(e.target.value)}
              />
            </div>

            {showInputs && (
              <OperatorInputs
                sectionKey={section.key}
                inputs={inputs}
                setInputs={setInputs}
              />
            )}

            <div className="flex gap-2">
              <Button
                variant="outline"
                size="sm"
                disabled={save.isPending}
                onClick={() => save.mutate()}
              >
                {save.isPending ? "Saving…" : "Save section"}
              </Button>
              <Button
                size="sm"
                disabled={approve.isPending || section.approved}
                onClick={() => approve.mutate()}
              >
                {section.approved
                  ? "Approved"
                  : approve.isPending
                    ? "Approving…"
                    : "Approve section"}
              </Button>
            </div>
            {(save.isError || approve.isError) && (
              <Alert variant="destructive">
                <AlertTitle>Action failed</AlertTitle>
                <AlertDescription>
                  {save.error instanceof APIError
                    ? save.error.message
                    : approve.error instanceof APIError
                      ? approve.error.message
                      : "Unexpected error."}
                </AlertDescription>
              </Alert>
            )}
          </>
        )}
      </CardContent>
    </Card>
  );
}

function OperatorInputs({
  sectionKey,
  inputs,
  setInputs,
}: {
  sectionKey: string;
  inputs: BoardPackSectionInputs;
  setInputs: (i: BoardPackSectionInputs) => void;
}) {
  // Map each operator-entered section to the structured fields it accepts
  // (decisions D3 + D5). These have no automated data source for v1 — the
  // operator types them in.
  const fields: { key: keyof BoardPackSectionInputs; label: string }[] =
    sectionKey === "investment"
      ? [{ key: "spend_usd", label: "Security spend this quarter ($)" }]
      : sectionKey === "coverage_trend"
        ? [
            {
              key: "baseline_coverage_pct",
              label: "Prior-quarter coverage baseline (%)",
            },
          ]
        : [
            { key: "phishing_pass_rate_pct", label: "Phishing pass rate (%)" },
            { key: "p1_patch_median_days", label: "P1 patch median (days)" },
            { key: "incident_count", label: "Incident count" },
            { key: "vendor_reviews_on_time", label: "Vendor reviews on time" },
            { key: "vendor_reviews_total", label: "Vendor reviews total" },
          ];

  return (
    <div className="space-y-2 rounded-md bg-slate-50 p-3">
      <p className="text-xs font-medium text-slate-600">
        Operator-entered (no automated connector for v1)
      </p>
      <div className="grid grid-cols-2 gap-3">
        {fields.map((f) => (
          <div key={f.key} className="space-y-1">
            <label className="text-xs text-slate-600">{f.label}</label>
            <Input
              type="number"
              value={inputs[f.key] ?? ""}
              onChange={(e) => {
                const raw = e.target.value;
                const next = { ...inputs };
                if (raw === "") {
                  delete next[f.key];
                } else {
                  next[f.key] = Number(raw);
                }
                setInputs(next);
              }}
            />
          </div>
        ))}
      </div>
    </div>
  );
}

function PublishCard({
  packID,
  isPublished,
  allApproved,
  publishedBy,
  onPublished,
}: {
  packID: string;
  isPublished: boolean;
  allApproved: boolean;
  publishedBy?: string;
  onPublished: (pack: BoardPack) => void;
}) {
  const [publisher, setPublisher] = useState("");

  const publish = useMutation({
    mutationFn: () => publishBoardPack(packID, publisher),
    onSuccess: onPublished,
  });

  if (isPublished) {
    return (
      <Card>
        <CardHeader>
          <CardTitle>Published</CardTitle>
          <CardDescription>
            This pack is frozen and immutable. Published by{" "}
            <span className="font-medium">{publishedBy || "unknown"}</span>.
          </CardDescription>
        </CardHeader>
      </Card>
    );
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Publish</CardTitle>
        <CardDescription>
          Publishing freezes the pack as an immutable artifact. Every section
          must be approved first.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {!allApproved && (
          <Alert>
            <AlertTitle>Not ready to publish</AlertTitle>
            <AlertDescription>
              Approve every section above before publishing.
            </AlertDescription>
          </Alert>
        )}
        <form
          className="flex items-end gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            if (publisher) publish.mutate();
          }}
        >
          <div className="space-y-1">
            <label className="text-sm font-medium text-slate-700">
              Approver name
            </label>
            <Input
              value={publisher}
              placeholder="e.g. Sam Rivera (CISO)"
              onChange={(e) => setPublisher(e.target.value)}
              className="w-64"
            />
          </div>
          <Button
            type="submit"
            disabled={!allApproved || !publisher || publish.isPending}
          >
            {publish.isPending ? "Publishing…" : "Approve & publish"}
          </Button>
        </form>
        {publish.isError && (
          <Alert variant="destructive">
            <AlertTitle>Publish failed</AlertTitle>
            <AlertDescription>
              {publish.error instanceof APIError
                ? publish.error.message
                : "Unexpected error."}
            </AlertDescription>
          </Alert>
        )}
      </CardContent>
    </Card>
  );
}
