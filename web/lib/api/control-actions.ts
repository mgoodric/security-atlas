// #2110 — browser-side calls behind the control-detail header actions:
// Evaluate now, Download bundle and Upload new version. Each goes through
// a BFF route under /api/controls, which forwards the httpOnly bearer.

import type { ControlStateResponse } from "@/lib/api/control-detail";

/** The bundle upload's response (POST /v1/controls:upload-bundle). */
export interface BundleUploadResult {
  control_id: string;
  bundle_id: string;
  version: number;
  superseded_id?: string;
  is_new_bundle: boolean;
  gate_warning?: string;
  anchor_warning?: string;
}

/** ControlActionError carries the platform's status and message. */
export class ControlActionError extends Error {
  constructor(
    message: string,
    readonly status: number,
    /** Seconds from Retry-After on a 429, when the platform sent one. */
    readonly retryAfterSeconds?: number,
  ) {
    super(message);
    this.name = "ControlActionError";
  }
}

/** The integer-seconds form of Retry-After; anything else is undefined. */
export function parseRetryAfter(raw: string | null): number | undefined {
  if (!raw || !/^\d+$/.test(raw.trim())) return undefined;
  return Number(raw.trim());
}

async function errorFrom(res: Response, fallback: string): Promise<never> {
  let message = fallback;
  try {
    const body = (await res.json()) as { error?: string };
    if (body?.error) message = body.error;
  } catch {
    // keep the fallback
  }
  throw new ControlActionError(
    message,
    res.status,
    parseRetryAfter(res.headers.get("Retry-After")),
  );
}

/** bundleDownloadHref is the Download bundle link for a control. */
export function bundleDownloadHref(controlID: string): string {
  return `/api/controls/${encodeURIComponent(controlID)}/bundle`;
}

/** evaluateControl runs evaluation now and returns the control's new state. */
export async function evaluateControl(
  controlID: string,
): Promise<ControlStateResponse> {
  const res = await fetch(
    `/api/controls/${encodeURIComponent(controlID)}/evaluate`,
    { method: "POST" },
  );
  if (!res.ok) return errorFrom(res, "Evaluation failed");
  return (await res.json()) as ControlStateResponse;
}

/** uploadControlBundle posts a bundle.tar.gz to the existing bundle upload. */
export async function uploadControlBundle(
  file: Blob,
): Promise<BundleUploadResult> {
  const form = new FormData();
  form.append("bundle.tar.gz", file, "bundle.tar.gz");
  const res = await fetch("/api/controls/upload-bundle", {
    method: "POST",
    body: form,
  });
  if (!res.ok) return errorFrom(res, "Upload failed");
  return (await res.json()) as BundleUploadResult;
}
