import { cookies } from "next/headers";
import { redirect } from "next/navigation";

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { AttestForm } from "@/components/attest/AttestForm";
import { getAttestForm, type AttestForm as AttestFormShape } from "@/lib/api";
import { SESSION_COOKIE } from "@/lib/auth";

// Slice 011 — server component that fetches the control's attestation
// form metadata from the platform, then renders the client form
// component. Authoritative validation runs on the server side; the
// frontend traversal is a convenience layer.

export default async function AttestPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = await params;
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) redirect(`/login?from=/controls/${id}/attest`);

  let form: AttestFormShape;
  try {
    form = await getAttestForm(bearer, id);
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return (
      <div className="container mx-auto max-w-3xl py-8">
        <Card>
          <CardHeader>
            <CardTitle>Attestation unavailable</CardTitle>
          </CardHeader>
          <CardContent>
            <p className="text-sm text-muted-foreground">
              {e.status === 404
                ? "Control not found, or your tenant cannot see it."
                : (e.message ?? "Failed to load attestation form.")}
            </p>
          </CardContent>
        </Card>
      </div>
    );
  }

  return (
    <div className="container mx-auto max-w-3xl py-8 space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>{form.title}</CardTitle>
        </CardHeader>
        <CardContent className="space-y-2 text-sm">
          <div>
            <span className="text-muted-foreground">Bundle:</span>{" "}
            <code className="font-mono">{form.bundle_id}</code>
          </div>
          <div className="flex items-center gap-2">
            <span className="text-muted-foreground">Type:</span>
            <Badge variant="secondary">{form.implementation_type}</Badge>
            <span className="text-muted-foreground">Owner role:</span>
            <Badge variant="outline">{form.owner_role}</Badge>
            {form.freshness_class ? (
              <>
                <span className="text-muted-foreground">Cadence:</span>
                <Badge>{form.freshness_class}</Badge>
              </>
            ) : null}
          </div>
          <p className="text-xs text-muted-foreground">
            Records a {form.platform_schema_kind}/{form.platform_schema_version}{" "}
            evidence record into the ledger upon submit. Audit log captures the
            attempt.
          </p>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Submit attestation</CardTitle>
        </CardHeader>
        <CardContent>
          <AttestForm form={form} />
        </CardContent>
      </Card>
    </div>
  );
}
