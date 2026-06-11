"use client";

// Slice 688 AC-5 — record a completed vendor review. A small client form
// that POSTs to the ledger BFF (`/api/vendors/{id}/reviews`); on success it
// routes back to the read-only detail page, where the new review appears in
// the timeline. Mirrors the slice-686 read/edit split: the write affordance
// lives on its own route, not bolted onto the read-only detail.
//
// NOTE: this slice does NOT touch any post-recording auto-refresh of the
// detail page beyond the explicit navigation back — the broader
// "recording a review doesn't refresh" concern (slice 424) is slice 691's
// job, not this slice's.

import { useRouter } from "next/navigation";
import Link from "next/link";
import { use, useState } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { VendorReviewOutcome, VendorReviewWrite } from "@/lib/api/vendors";

const OUTCOMES: { value: VendorReviewOutcome; label: string }[] = [
  { value: "pass", label: "Pass" },
  { value: "pass_with_findings", label: "Pass with findings" },
  { value: "fail", label: "Fail" },
  { value: "waived", label: "Waived" },
];

function todayISO(): string {
  return new Date().toISOString().slice(0, 10);
}

export default function RecordVendorReviewPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  const router = useRouter();

  const [reviewedAt, setReviewedAt] = useState(todayISO());
  const [reviewer, setReviewer] = useState("");
  const [outcome, setOutcome] = useState<VendorReviewOutcome>("pass");
  const [notes, setNotes] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    if (!reviewedAt) {
      setError("Review date is required.");
      return;
    }
    setSubmitting(true);
    const body: VendorReviewWrite = {
      reviewed_at: reviewedAt,
      reviewer: reviewer.trim(),
      outcome,
      notes,
    };
    try {
      const res = await fetch(
        `/api/vendors/${encodeURIComponent(id)}/reviews`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(body),
        },
      );
      if (res.status === 401) {
        router.push(`/login?from=/vendors/${id}/reviews/new`);
        return;
      }
      if (!res.ok) {
        const detail = (await res.json().catch(() => ({}))) as {
          error?: string;
        };
        throw new Error(detail.error ?? `${res.status} ${res.statusText}`);
      }
      router.push(`/vendors/${id}`);
    } catch (err) {
      setError((err as Error).message);
      setSubmitting(false);
    }
  }

  return (
    <div className="space-y-6" data-testid="vendor-record-review">
      <div className="text-sm">
        <Link
          href={`/vendors/${id}`}
          className="text-muted-foreground hover:underline"
          data-testid="vendor-record-review-back"
        >
          ← Vendor detail
        </Link>
      </div>
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Record review</h1>
        <p className="text-sm text-muted-foreground">
          Appends a completed review to this vendor&apos;s history. A recorded
          review is immutable.
        </p>
      </div>

      {error ? (
        <Alert variant="destructive" data-testid="vendor-record-review-error">
          <AlertTitle>Could not record review</AlertTitle>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      ) : null}

      <Card>
        <CardHeader>
          <CardTitle>Review details</CardTitle>
        </CardHeader>
        <CardContent>
          <form className="space-y-4" onSubmit={onSubmit}>
            <div>
              <label
                htmlFor="reviewed_at"
                className="text-[11px] uppercase tracking-wider text-muted-foreground"
              >
                Review date
              </label>
              <Input
                id="reviewed_at"
                type="date"
                value={reviewedAt}
                onChange={(e) => setReviewedAt(e.target.value)}
                data-testid="vendor-record-review-date"
                required
              />
            </div>
            <div>
              <label
                htmlFor="outcome"
                className="text-[11px] uppercase tracking-wider text-muted-foreground"
              >
                Outcome
              </label>
              <Select
                id="outcome"
                value={outcome}
                onChange={(e) =>
                  setOutcome(e.target.value as VendorReviewOutcome)
                }
                data-testid="vendor-record-review-outcome"
              >
                {OUTCOMES.map((o) => (
                  <option key={o.value} value={o.value}>
                    {o.label}
                  </option>
                ))}
              </Select>
            </div>
            <div>
              <label
                htmlFor="reviewer"
                className="text-[11px] uppercase tracking-wider text-muted-foreground"
              >
                Reviewer
              </label>
              <Input
                id="reviewer"
                type="text"
                placeholder="owner@example.com"
                value={reviewer}
                onChange={(e) => setReviewer(e.target.value)}
                data-testid="vendor-record-review-reviewer"
              />
            </div>
            <div>
              <label
                htmlFor="notes"
                className="text-[11px] uppercase tracking-wider text-muted-foreground"
              >
                Notes
              </label>
              <textarea
                id="notes"
                className="min-h-24 w-full rounded-lg border border-input bg-transparent px-2.5 py-1.5 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50"
                value={notes}
                onChange={(e) => setNotes(e.target.value)}
                data-testid="vendor-record-review-notes"
              />
            </div>
            <div className="flex items-center gap-2">
              <Button
                type="submit"
                disabled={submitting}
                data-testid="vendor-record-review-submit"
              >
                {submitting ? "Recording…" : "Record review"}
              </Button>
              <Link
                href={`/vendors/${id}`}
                className="text-sm text-muted-foreground hover:underline"
              >
                Cancel
              </Link>
            </div>
          </form>
        </CardContent>
      </Card>
    </div>
  );
}
