// Slice 370 — vendor lite (slice 024), extracted from the former
// `web/lib/api.ts` god-file.

import { apiBaseURL, APIError } from "./base";
import { apiFetch } from "./_shared";

export type VendorCriticality = "low" | "medium" | "high";
export type VendorReviewCadence =
  | "monthly"
  | "quarterly"
  | "biannual"
  | "annual";

export type Vendor = {
  id: string;
  name: string;
  domain?: string | null;
  criticality: VendorCriticality;
  contract_start?: string | null;
  contract_end?: string | null;
  dpa_signed: boolean;
  dpa_signed_at?: string | null;
  review_cadence: VendorReviewCadence;
  last_review_date?: string | null;
  overdue: boolean;
  owner_user: string;
  linked_sow_uri?: string | null;
  notes: string;
  scope_cell_ids: string[];
  created_at: string;
  updated_at: string;
};

export type VendorWrite = {
  name: string;
  domain?: string | null;
  criticality: VendorCriticality;
  contract_start?: string | null;
  contract_end?: string | null;
  dpa_signed: boolean;
  dpa_signed_at?: string | null;
  review_cadence: VendorReviewCadence;
  last_review_date?: string | null;
  owner_user: string;
  linked_sow_uri?: string | null;
  notes: string;
  scope_cell_ids: string[];
};

// Slice 688 — vendor_reviews ledger. One row per completed review,
// append-only, newest-first from the read path.
export type VendorReviewOutcome =
  | "pass"
  | "pass_with_findings"
  | "fail"
  | "waived";

export type VendorReview = {
  id: string;
  vendor_id: string;
  reviewed_at: string;
  reviewer: string;
  outcome: VendorReviewOutcome;
  notes: string;
  created_at: string;
};

export type VendorReviewWrite = {
  reviewed_at: string;
  reviewer: string;
  outcome: VendorReviewOutcome;
  notes: string;
};

export type VendorBurndownBand = {
  criticality: string;
  total: number;
  overdue: number;
  on_time_fraction: number;
};

export type VendorBurndown = {
  as_of: string;
  bands: VendorBurndownBand[];
  total: VendorBurndownBand;
};

export type VendorListFilter = {
  criticality?: VendorCriticality;
  overdue?: boolean;
  as_of?: string;
};

function vendorQuery(filter?: VendorListFilter): string {
  if (!filter) return "";
  const qs = new URLSearchParams();
  if (filter.criticality) qs.set("criticality", filter.criticality);
  if (filter.overdue) qs.set("overdue", "true");
  if (filter.as_of) qs.set("as_of", filter.as_of);
  const s = qs.toString();
  return s ? `?${s}` : "";
}

export async function listVendors(
  bearer: string,
  filter?: VendorListFilter,
): Promise<Vendor[]> {
  const res = await apiFetch(`/v1/vendors${vendorQuery(filter)}`, bearer);
  const body = (await res.json()) as { vendors: Vendor[] };
  return body.vendors;
}

export async function getVendor(bearer: string, id: string): Promise<Vendor> {
  const res = await apiFetch(`/v1/vendors/${encodeURIComponent(id)}`, bearer);
  const body = (await res.json()) as { vendor: Vendor };
  return body.vendor;
}

export async function createVendor(
  bearer: string,
  body: VendorWrite,
): Promise<Vendor> {
  const res = await fetch(`${apiBaseURL()}/v1/vendors`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${bearer}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  }
  const decoded = (await res.json()) as { vendor: Vendor };
  return decoded.vendor;
}

export async function updateVendor(
  bearer: string,
  id: string,
  body: VendorWrite,
): Promise<Vendor> {
  const res = await fetch(
    `${apiBaseURL()}/v1/vendors/${encodeURIComponent(id)}`,
    {
      method: "PATCH",
      headers: {
        Authorization: `Bearer ${bearer}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify(body),
    },
  );
  if (!res.ok) {
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  }
  const decoded = (await res.json()) as { vendor: Vendor };
  return decoded.vendor;
}

export async function deleteVendor(bearer: string, id: string): Promise<void> {
  const res = await fetch(
    `${apiBaseURL()}/v1/vendors/${encodeURIComponent(id)}`,
    {
      method: "DELETE",
      headers: { Authorization: `Bearer ${bearer}` },
    },
  );
  // The platform returns 204 No Content on a successful delete (and on an
  // idempotent re-delete — Store.Delete is idempotent). Anything else is
  // an error the caller surfaces.
  if (!res.ok) {
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  }
}

// listVendorReviews fetches a vendor's review history, newest-first
// (slice 688 AC-3). RLS scopes the read upstream; a cross-tenant id yields
// an empty series.
export async function listVendorReviews(
  bearer: string,
  vendorId: string,
): Promise<VendorReview[]> {
  const res = await apiFetch(
    `/v1/vendors/${encodeURIComponent(vendorId)}/reviews`,
    bearer,
  );
  const body = (await res.json()) as { reviews: VendorReview[] };
  return body.reviews;
}

// recordVendorReview appends a completed review to the ledger (slice 688
// AC-5). The platform also keeps the vendor's last_review_date scalar
// consistent with the most-recent ledger row.
export async function recordVendorReview(
  bearer: string,
  vendorId: string,
  body: VendorReviewWrite,
): Promise<VendorReview> {
  const res = await fetch(
    `${apiBaseURL()}/v1/vendors/${encodeURIComponent(vendorId)}/reviews`,
    {
      method: "POST",
      headers: {
        Authorization: `Bearer ${bearer}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify(body),
    },
  );
  if (!res.ok) {
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  }
  const decoded = (await res.json()) as { review: VendorReview };
  return decoded.review;
}

export async function getVendorBurndown(
  bearer: string,
  filter?: VendorListFilter,
): Promise<VendorBurndown> {
  const res = await apiFetch(
    `/v1/vendors/burndown${vendorQuery(filter)}`,
    bearer,
  );
  return (await res.json()) as VendorBurndown;
}
