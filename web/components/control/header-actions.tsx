"use client";

// Slice 255 — control-detail header actions + "last evaluated" timestamp.
//
// Mockup parity target: `Plans/_archive/mockups/control.html` lines 92-102. The
// top-right header well carries:
//
//   1. Actions (#2110): Evaluate now (POST /v1/controls/{id}/evaluate,
//      "Evaluating…" while pending), Download bundle (the control's
//      control.yaml as a tarball), Upload new version (a form over the
//      existing bundle upload) and the Request exception link. Run query
//      and Edit YAML were disabled placeholders; #2082 removed them and
//      Evaluate now / the bundle pair are what replaced them.
//   2. A sub-line: "last evaluated <relative-time>" with a status icon.
//
// JUDGMENT decisions (see docs/audit-log/255-header-actions-decisions.md):
//
//   D1: (retired by #2082) Run query + Edit YAML were disabled buttons
//       with tooltips; they are no longer rendered.
//
//   D2: Request exception renders as a LINK (shadcn outline size=sm) to
//       the request form, `/exceptions/new?control_id=<id>` (#2113),
//       which opens with this control pre-filled.
//
//   D3: relative-time formatter lives in `web/lib/relative-time.ts` (new
//       — see that file's preamble). The freshness-clock's `humanizeSince`
//       is intentionally separate because it produces a compact ring
//       readout ("8m"), not the operator-facing sentence form ("8
//       minutes ago") the header sub-line needs per the mockup.
//
// Data binding:
//   The "last evaluated" timestamp reads `state.last_observed_at` from
//   the most recent scope cell (AC-1) — the same source the right-rail
//   freshness clock uses, so the two readouts agree by construction.
//   Aggregation rule: most-recent across all entries (the freshest
//   signal). When state is undefined we render "—"; when state exists
//   but has no `last_observed_at` we render "never".
//
// Anti-criteria honored:
//   - P0-255-1/2: no Run query execution path and no YAML editor; no
//     entry point is rendered for either (#2082). Evaluate now runs the
//     platform's evaluation; the YAML is edited offline and uploaded.
//   - P0-255-3: no `<a href="#">` anywhere. Request exception links to a
//     real, existing route.
//
// Evaluate now writes the returned state into the ["control", id,
// "state"] query and invalidates the control's other reads, so the
// header sub-line, freshness clock and KPIs move together. Who may
// evaluate (owner or admin) and upload (admin) is the platform's check;
// a refusal shows inline rather than hiding the button.
//
// #2111: manually attested controls get an Attest link, and the header
// shows the control's rolled-up result. Back from an attestation
// (`?attested=<ingested_at>`), the result reads "Evaluating…" and the
// state is re-read every second until an evaluation at or after that
// instant lands. After 30 seconds it stops and says so instead; it never
// spins forever.

import Link from "next/link";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useId, useState } from "react";

import { Badge } from "@/components/ui/badge";
import { Button, buttonVariants } from "@/components/ui/button";
import {
  bundleDownloadHref,
  evaluateControl,
  uploadControlBundle,
} from "@/lib/api/control-actions";
import {
  fetchControlState,
  type ControlStateResponse,
  type ControlStateEntry,
} from "@/lib/api/control-detail";
import {
  ATTEST_EVALUATION_POLL_MS,
  ATTEST_EVALUATION_TIMEOUT_MS,
  ATTESTED_PARAM,
  evaluationLanded,
  isManualImplementation,
  rollupResult,
} from "@/lib/attest-evaluation";
import { relativeTimeOrNever } from "@/lib/relative-time";
import { cn } from "@/lib/utils";

// RESULT_VARIANT maps a rolled-up result to its badge tone.
const RESULT_VARIANT: Record<
  string,
  "pass" | "destructive" | "info" | "warning" | "outline"
> = {
  pass: "pass",
  fail: "destructive",
  excepted: "info",
  inconclusive: "warning",
  na: "outline",
};

// useAttestationEvaluation waits for the evaluation an attestation
// triggered. `attestedAt` is the record's ingested_at from the URL, or
// null when the page was not reached from the attest form. While waiting
// it re-reads the control state every ATTEST_EVALUATION_POLL_MS; once the
// new state lands it drops the parameter from the URL, and after
// ATTEST_EVALUATION_TIMEOUT_MS it stops polling and reports timedOut.
function useAttestationEvaluation(
  controlID: string,
  attestedAt: string | null,
  state: ControlStateResponse | undefined,
): { waiting: boolean; timedOut: boolean } {
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const [timedOut, setTimedOut] = useState(false);

  const landed =
    attestedAt === null || evaluationLanded(state?.states, attestedAt);
  const waiting = !landed && !timedOut;

  useQuery({
    queryKey: ["control", controlID, "state"],
    queryFn: () => fetchControlState(controlID),
    enabled: waiting,
    refetchInterval: waiting ? ATTEST_EVALUATION_POLL_MS : false,
  });

  useEffect(() => {
    if (attestedAt === null || landed) return;
    const id = setTimeout(
      () => setTimedOut(true),
      ATTEST_EVALUATION_TIMEOUT_MS,
    );
    return () => clearTimeout(id);
  }, [attestedAt, landed]);

  useEffect(() => {
    if (attestedAt === null || !landed) return;
    const params = new URLSearchParams(searchParams.toString());
    params.delete(ATTESTED_PARAM);
    const qs = params.toString();
    router.replace(qs ? `${pathname}?${qs}` : pathname, { scroll: false });
  }, [attestedAt, landed, pathname, router, searchParams]);

  return { waiting, timedOut: attestedAt !== null && !landed && timedOut };
}

// mostRecentObservedAt picks the freshest `last_observed_at` across all
// scope-cell entries — the same reduce-to-newest rule the freshness
// clock uses (`mostRecentObserved` in
// `web/components/control/freshness-clock.tsx`). Kept inline (not
// extracted to a shared helper) because the two readers want slightly
// different return shapes — the clock wants a `Date | null`, the
// header wants the raw ISO string so it can pass through to the
// `relativeTimeOrNever` formatter.
function mostRecentObservedAt(entries: ControlStateEntry[]): string | null {
  let latestISO: string | null = null;
  let latestMs = -Infinity;
  for (const e of entries) {
    if (!e.last_observed_at) continue;
    const ms = new Date(e.last_observed_at).getTime();
    if (Number.isNaN(ms)) continue;
    if (ms > latestMs) {
      latestMs = ms;
      latestISO = e.last_observed_at;
    }
  }
  return latestISO;
}

// useNow gives the page a client-side clock that refreshes every minute
// so the "8 minutes ago" text re-renders without a page reload.
//
// React 19 set-state-in-effect lint discipline (slice 063 — recorded in
// the page-level preamble): we do NOT call setState synchronously
// inside an effect. The hook returns `null` on the server / first
// client render and a real number after the first mount tick, and the
// caller is responsible for rendering a stable placeholder when `now`
// is `null`. This avoids the SSR / client-hydration drift in a way the
// lint can verify — the server-rendered HTML carries the placeholder,
// the client hydrates to the placeholder (identical), and the FIRST
// post-mount setState (inside `setInterval` — not synchronous in the
// effect body) flips to the real clock.
//
// The trade-off: there is a one-tick window where the relative-time
// reads "—" instead of "8 minutes ago". With `intervalMs = 60_000`
// the worst case is a one-minute warmup; we accept that to keep the
// lint clean (set-state-in-effect is a known React-19 footgun the
// codebase has been bitten by before — see slice 063's note).
function useNow(intervalMs = 60_000): number | null {
  const [now, setNow] = useState<number | null>(null);
  useEffect(() => {
    // First-tick: run the interval body once via a 0ms timeout so the
    // clock seeds without a synchronous setState in the effect body.
    // The 0ms timeout is microtask-ordered after the current render
    // commits — React-19-safe.
    const seed = setTimeout(() => setNow(Date.now()), 0);
    const id = setInterval(() => setNow(Date.now()), intervalMs);
    return () => {
      clearTimeout(seed);
      clearInterval(id);
    };
  }, [intervalMs]);
  return now;
}

export interface ControlHeaderActionsProps {
  controlID: string;
  // The state response may be undefined while the query is loading or if
  // it errored. The header sub-line renders "—" in that case rather
  // than a flash of "never".
  state: ControlStateResponse | undefined;
  // The control's implementation_type; manual controls get Attest (#2111).
  implementationType?: string;
}

export function ControlHeaderActions({
  controlID,
  state,
  implementationType,
}: ControlHeaderActionsProps) {
  const now = useNow();
  const router = useRouter();
  const searchParams = useSearchParams();
  const attestedAt = searchParams.get(ATTESTED_PARAM);
  const attestation = useAttestationEvaluation(controlID, attestedAt, state);
  const result = rollupResult(state?.states);
  const queryClient = useQueryClient();
  const fileInputID = useId();
  const [uploadOpen, setUploadOpen] = useState(false);
  const [file, setFile] = useState<File | null>(null);

  const evaluate = useMutation({
    mutationFn: () => evaluateControl(controlID),
    onSuccess: async (next) => {
      queryClient.setQueryData(["control", controlID, "state"], next);
      await queryClient.invalidateQueries({
        queryKey: ["control", controlID],
        predicate: (q) => q.queryKey[2] !== "state",
      });
    },
  });

  const upload = useMutation({
    mutationFn: (f: File) => uploadControlBundle(f),
    onSuccess: async (res) => {
      setFile(null);
      setUploadOpen(false);
      if (res.control_id && res.control_id !== controlID) {
        // The upload superseded this control: its new version has a new id.
        router.push(`/controls/${encodeURIComponent(res.control_id)}`);
        return;
      }
      await queryClient.invalidateQueries({ queryKey: ["control", controlID] });
    },
  });

  // Resolve the freshest `last_observed_at` across cells. Three branches:
  //   1. state is undefined        → undefined  → "—"
  //   2. state exists, no entries  → null       → "never"
  //   3. state exists, has entries → ISO string → "8 minutes ago"
  const latestISO: string | null | undefined = state
    ? mostRecentObservedAt(state.states)
    : undefined;

  // Render "—" until the client clock has seeded (`now === null`).
  // After the first 0ms timeout in `useNow` fires, `now` becomes a
  // real ms value and the relative-time string renders honestly.
  const display = now === null ? "—" : relativeTimeOrNever(latestISO, now);

  return (
    <div
      className="flex flex-col items-end gap-2"
      data-testid="control-header-actions"
    >
      <div
        className="flex items-center gap-2"
        data-testid="control-header-action-buttons"
      >
        {isManualImplementation(implementationType) && (
          <Link
            href={`/controls/${encodeURIComponent(controlID)}/attest`}
            className={cn(buttonVariants({ variant: "outline", size: "sm" }))}
            data-testid="control-action-attest"
          >
            Attest
          </Link>
        )}
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => evaluate.mutate()}
          disabled={evaluate.isPending}
          aria-busy={evaluate.isPending}
          data-testid="control-action-evaluate"
        >
          {evaluate.isPending ? "Evaluating…" : "Evaluate now"}
        </Button>
        <a
          href={bundleDownloadHref(controlID)}
          download
          className={cn(buttonVariants({ variant: "outline", size: "sm" }))}
          data-testid="control-action-download-bundle"
        >
          Download bundle
        </a>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => setUploadOpen((o) => !o)}
          aria-expanded={uploadOpen}
          data-testid="control-action-upload-toggle"
        >
          Upload new version
        </Button>
        <Link
          href={`/exceptions/new?control_id=${encodeURIComponent(controlID)}`}
          className={cn(buttonVariants({ variant: "outline", size: "sm" }))}
          data-testid="control-action-request-exception"
        >
          Request exception
        </Link>
      </div>

      {uploadOpen && (
        <form
          className="flex items-center gap-2 text-xs"
          data-testid="control-upload-form"
          onSubmit={(e) => {
            e.preventDefault();
            if (file) upload.mutate(file);
          }}
        >
          <label htmlFor={fileInputID} className="text-muted-foreground">
            Bundle (.tar.gz)
          </label>
          <input
            id={fileInputID}
            type="file"
            accept=".tar.gz,.tgz,application/gzip"
            onChange={(e) => setFile(e.target.files?.[0] ?? null)}
            data-testid="control-upload-file"
          />
          <Button
            type="submit"
            size="sm"
            disabled={!file || upload.isPending}
            data-testid="control-upload-submit"
          >
            {upload.isPending ? "Uploading…" : "Upload"}
          </Button>
        </form>
      )}

      {evaluate.isError && (
        <p
          role="alert"
          className="text-xs text-destructive"
          data-testid="control-evaluate-error"
        >
          {evaluate.error.message}
        </p>
      )}
      {upload.isError && (
        <p
          role="alert"
          className="text-xs text-destructive"
          data-testid="control-upload-error"
        >
          {upload.error.message}
        </p>
      )}

      <div
        className="flex items-center gap-1.5 text-xs"
        data-testid="control-state-result"
        aria-live="polite"
      >
        <span className="text-muted-foreground">Result</span>
        {attestation.waiting ? (
          <Badge
            variant="outline"
            aria-busy="true"
            data-testid="control-state-result-value"
            data-result="evaluating"
          >
            Evaluating…
          </Badge>
        ) : result ? (
          <Badge
            variant={RESULT_VARIANT[result] ?? "outline"}
            data-testid="control-state-result-value"
            data-result={result}
          >
            {result}
          </Badge>
        ) : (
          <span
            className="font-mono text-foreground"
            data-testid="control-state-result-value"
            data-result="none"
          >
            —
          </span>
        )}
      </div>
      {attestation.timedOut && (
        <p
          role="alert"
          className="text-xs text-destructive"
          data-testid="control-attest-evaluation-timeout"
        >
          The attestation is recorded, but its evaluation has not landed after
          30 seconds. Use Evaluate now, or check back after the next scheduled
          run.
        </p>
      )}

      <div
        className="flex items-center gap-1.5 text-xs text-muted-foreground"
        data-testid="control-last-evaluated"
      >
        <svg
          className="h-3.5 w-3.5"
          viewBox="0 0 20 20"
          fill="currentColor"
          aria-hidden="true"
        >
          <path
            fillRule="evenodd"
            d="M10 18a8 8 0 100-16 8 8 0 000 16zm.93-12.518A1 1 0 0010 4a1 1 0 00-.93.518L5.4 11.482A1 1 0 006.291 13H8v3a1 1 0 102 0v-3h1.71a1 1 0 00.89-1.518L10.93 5.482z"
            clipRule="evenodd"
          />
        </svg>
        <span>
          last evaluated{" "}
          <span
            className="font-mono text-foreground"
            data-testid="control-last-evaluated-value"
          >
            {display}
          </span>
        </span>
      </div>
    </div>
  );
}
