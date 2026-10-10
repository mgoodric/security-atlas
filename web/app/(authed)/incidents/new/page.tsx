"use client";

import { useMutation } from "@tanstack/react-query";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { FormEvent, useState } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button, buttonVariants } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import {
  createIncident,
  type IncidentAffectedSystem,
  type IncidentCreateInput,
  type IncidentSeverity,
} from "@/lib/api/incidents";

export default function NewIncidentPage() {
  const router = useRouter();
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [severity, setSeverity] = useState<IncidentSeverity>("p2");
  const [detectedAt, setDetectedAt] = useState("");
  const [systemName, setSystemName] = useState("");
  const [systemTier, setSystemTier] = useState("");
  const [controlIDs, setControlIDs] = useState("");
  const [riskIDs, setRiskIDs] = useState("");
  const [vendorIDs, setVendorIDs] = useState("");
  const [evidenceIDs, setEvidenceIDs] = useState("");

  const createM = useMutation({
    mutationFn: (body: IncidentCreateInput) => createIncident(body),
    onSuccess: (res) => {
      router.push(`/incidents/${encodeURIComponent(res.incident.record.id)}`);
    },
  });

  const submit = (event: FormEvent) => {
    event.preventDefault();
    const affected: IncidentAffectedSystem[] = systemName.trim()
      ? [
          {
            name: systemName.trim(),
            tier: systemTier.trim() || undefined,
          },
        ]
      : [];
    createM.mutate({
      title,
      description,
      severity,
      detected_at: detectedAt ? new Date(detectedAt).toISOString() : undefined,
      affected_system_tier: systemTier.trim() || undefined,
      affected_systems: affected,
      control_ids: splitIDs(controlIDs),
      risk_ids: splitIDs(riskIDs),
      vendor_ids: splitIDs(vendorIDs),
      evidence_ids: splitIDs(evidenceIDs),
    });
  };

  return (
    <div className="space-y-6" data-testid="incident-new">
      <Link
        href="/incidents"
        className={buttonVariants({ variant: "ghost", size: "sm" })}
      >
        Back to incidents
      </Link>

      <header className="space-y-1">
        <h1 className="text-2xl font-semibold tracking-tight">Log incident</h1>
        <p className="text-sm text-muted-foreground">
          Create the tenant-scoped incident record and initial timeline entry.
        </p>
      </header>

      <form className="space-y-4" onSubmit={submit}>
        <Card>
          <CardHeader className="border-b">
            <CardTitle>Incident</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="grid gap-4 md:grid-cols-2">
              <Label text="Title">
                <Input
                  value={title}
                  onChange={(e) => setTitle(e.target.value)}
                  required
                  data-testid="incident-new-title"
                />
              </Label>
              <Label text="Severity">
                <Select
                  value={severity}
                  onChange={(e) =>
                    setSeverity(e.target.value as IncidentSeverity)
                  }
                  data-testid="incident-new-severity"
                >
                  <option value="p3">P3</option>
                  <option value="p2">P2</option>
                  <option value="p1">P1</option>
                  <option value="p0">P0</option>
                </Select>
              </Label>
              <Label text="Detected at">
                <Input
                  type="datetime-local"
                  value={detectedAt}
                  onChange={(e) => setDetectedAt(e.target.value)}
                  data-testid="incident-new-detected-at"
                />
              </Label>
              <Label text="Affected system">
                <Input
                  value={systemName}
                  onChange={(e) => setSystemName(e.target.value)}
                  placeholder="api, auth, production cluster"
                  data-testid="incident-new-system"
                />
              </Label>
              <Label text="Affected system tier">
                <Select
                  value={systemTier}
                  onChange={(e) => setSystemTier(e.target.value)}
                  data-testid="incident-new-system-tier"
                >
                  <option value="">Unspecified</option>
                  <option value="low">low</option>
                  <option value="high">high</option>
                  <option value="critical">critical</option>
                </Select>
              </Label>
            </div>
            <Label text="Description" className="mt-4 block">
              <textarea
                className="min-h-24 w-full rounded-lg border bg-transparent px-3 py-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50"
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                data-testid="incident-new-description"
              />
            </Label>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="border-b">
            <CardTitle>Links</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="grid gap-4 md:grid-cols-2">
              <Label text="Control IDs">
                <Input
                  value={controlIDs}
                  onChange={(e) => setControlIDs(e.target.value)}
                  placeholder="comma-separated UUIDs"
                  data-testid="incident-new-control-ids"
                />
              </Label>
              <Label text="Risk IDs">
                <Input
                  value={riskIDs}
                  onChange={(e) => setRiskIDs(e.target.value)}
                  placeholder="comma-separated UUIDs"
                />
              </Label>
              <Label text="Vendor IDs">
                <Input
                  value={vendorIDs}
                  onChange={(e) => setVendorIDs(e.target.value)}
                  placeholder="comma-separated UUIDs"
                />
              </Label>
              <Label text="Evidence IDs">
                <Input
                  value={evidenceIDs}
                  onChange={(e) => setEvidenceIDs(e.target.value)}
                  placeholder="comma-separated UUIDs"
                />
              </Label>
            </div>
          </CardContent>
        </Card>

        {createM.error ? (
          <Alert variant="destructive" data-testid="incident-new-error">
            <AlertTitle>Could not log incident</AlertTitle>
            <AlertDescription>
              {(createM.error as Error).message}
            </AlertDescription>
          </Alert>
        ) : null}

        <div className="flex justify-end">
          <Button
            type="submit"
            disabled={createM.isPending}
            data-testid="incident-new-submit"
          >
            Log incident
          </Button>
        </div>
      </form>
    </div>
  );
}

function splitIDs(value: string): string[] {
  return value
    .split(/[,\s]+/)
    .map((v) => v.trim())
    .filter(Boolean);
}

function Label({
  text,
  children,
  className,
}: {
  text: string;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <label className={className ?? "block"}>
      <span className="mb-1 block text-xs uppercase text-muted-foreground">
        {text}
      </span>
      {children}
    </label>
  );
}
