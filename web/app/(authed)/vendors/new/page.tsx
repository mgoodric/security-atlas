"use client";

import { useRouter } from "next/navigation";

import { useToast } from "@/components/ui/toast";

import { VendorForm } from "../vendor-form";

import { createVendorFromCookieSession } from "../actions";

export default function NewVendorPage() {
  const router = useRouter();
  const { toast } = useToast();
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Add vendor</h1>
        <p className="text-sm text-muted-foreground">
          Capture the minimum the spreadsheet held: name, criticality, contract
          + DPA dates, review cadence, owner.
        </p>
      </div>
      <VendorForm
        onSubmit={async (body) => {
          const v = await createVendorFromCookieSession(body);
          toast("Vendor created");
          router.push(`/vendors/${v.id}`);
        }}
        submitLabel="Create vendor"
      />
    </div>
  );
}
