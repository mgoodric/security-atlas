"use client";

import { useRouter } from "next/navigation";

import { PageShell } from "@/components/shell/page-shell";
import { useToast } from "@/components/ui/toast";

import { VendorForm } from "../vendor-form";

import { createVendorFromCookieSession } from "../actions";

export default function NewVendorPage() {
  const router = useRouter();
  const { toast } = useToast();
  return (
    <PageShell
      title="Add vendor"
      subtitle="Capture the minimum the spreadsheet held: name, criticality, contract + DPA dates, review cadence, owner."
      width="reading"
    >
      <VendorForm
        onSubmit={async (body) => {
          const v = await createVendorFromCookieSession(body);
          toast("Vendor created");
          router.push(`/vendors/${v.id}`);
        }}
        submitLabel="Create vendor"
      />
    </PageShell>
  );
}
