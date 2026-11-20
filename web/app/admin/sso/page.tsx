// Slice 063 — /admin/sso form save enabled.
//
// Slice 060 shipped this page with the form intentionally `disabled` —
// a stopgap because the backend `PATCH /v1/admin/sso` did not exist on
// main. Slice 062 landed that endpoint (admin BFF backend endpoints).
// This slice (063) flips the stopgap: `disabled` is removed, the form
// posts via the new BFF proxy at `web/app/api/admin/sso/route.ts`, and
// success/error states render inline.
//
// Anti-criteria P0 honored:
//   - client_secret stays write-only: input is `type="password"` +
//     `autoComplete="new-password"`. The GET response sans client_secret
//     never re-fetches the secret, and an empty submit means "leave
//     existing" per slice 062's handler contract.
//   - No auto-submit. Only the explicit Save button triggers a PATCH.
//   - Discovery preflight is read-only and never persists state.
//   - Backend contract unchanged — no Go file edits in this slice.

"use client";

import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogPortal,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import type { AdminSSOConfig, AdminSSODeleteResult } from "@/lib/api/admin";
import { PageShell } from "@/components/shell/page-shell";
import { Term } from "@/components/help/term";
import { upstreamErrorMessage } from "@/lib/api/_shared";

type Preflight =
  | { state: "idle" }
  | { state: "loading" }
  | {
      state: "ok";
      authorization_endpoint?: string;
      token_endpoint?: string;
      jwks_uri?: string;
      issuer?: string;
    }
  | { state: "error"; message: string };

// FormState holds the controlled inputs. client_secret is intentionally
// separate from the GET-derived state because it is never read back from
// the server — see anti-criterion P0 above.
type FormState = {
  issuer_url: string;
  client_id: string;
  client_secret: string;
  redirect_url: string;
  allowed_email_domains: string;
  trust_unverified_email_for_link: boolean;
};

const EMPTY_FORM: FormState = {
  issuer_url: "",
  client_id: "",
  client_secret: "",
  redirect_url: "",
  allowed_email_domains: "",
  trust_unverified_email_for_link: false,
};

function configToForm(c: AdminSSOConfig | null): FormState {
  if (!c) return EMPTY_FORM;
  return {
    issuer_url: c.issuer_url ?? "",
    client_id: c.client_id ?? "",
    client_secret: "", // never re-populated from server
    redirect_url: c.redirect_url ?? "",
    allowed_email_domains: (c.allowed_email_domains ?? []).join(", "),
    trust_unverified_email_for_link: c.trust_unverified_email_for_link ?? false,
  };
}

export default function SSOConfigPage() {
  const queryClient = useQueryClient();
  const [form, setForm] = useState<FormState>(EMPTY_FORM);
  const [showSuccess, setShowSuccess] = useState(false);
  const [issuerURL, setIssuerURL] = useState("");
  const [preflight, setPreflight] = useState<Preflight>({ state: "idle" });
  // #2760: the last delete's counts. Held here, not in the delete card,
  // because the card unmounts once the config is gone.
  const [deleteResult, setDeleteResult] = useState<AdminSSODeleteResult | null>(
    null,
  );
  // Tracks the last server payload we synced into local form state.
  // When this differs from the current query data the next render
  // re-seeds the form. This is the React 19 "store the previous value
  // in state" pattern (https://react.dev/reference/react/useState#storing-information-from-previous-renders)
  // and avoids the `react-hooks/set-state-in-effect` lint by syncing
  // during render, not in an effect.
  const [seededFrom, setSeededFrom] = useState<AdminSSOConfig | null | "init">(
    "init",
  );

  // GET the current config. The BFF returns { config: null } when no
  // config is set, so the form renders empty rather than erroring.
  const { data, isLoading } = useQuery({
    queryKey: ["admin", "sso"],
    queryFn: async () => {
      const res = await fetch("/api/admin/sso");
      if (!res.ok) {
        throw new Error(`failed to load SSO config: ${res.status}`);
      }
      const body = (await res.json()) as { config: AdminSSOConfig | null };
      return body.config;
    },
  });

  // Seed the form whenever the GET resolves to a different config than
  // we last seeded from. client_secret stays empty (write-only).
  // Sync-during-render is safe here because we only call setState on
  // identity change, which converges in one extra render.
  if (data !== undefined && data !== seededFrom) {
    setSeededFrom(data ?? null);
    setForm(configToForm(data ?? null));
  }

  const mutation = useMutation({
    mutationFn: async (body: FormState) => {
      const allowed = body.allowed_email_domains
        .split(",")
        .map((s) => s.trim())
        .filter((s) => s.length > 0);
      // Slice 062 PATCH wire shape. client_secret omitted means "leave
      // existing"; the BFF strips an empty string for the same effect.
      const payload: Record<string, unknown> = {
        issuer_url: body.issuer_url,
        client_id: body.client_id,
        redirect_url: body.redirect_url,
        allowed_email_domains: allowed,
        trust_unverified_email_for_link: body.trust_unverified_email_for_link,
      };
      if (body.client_secret) {
        payload.client_secret = body.client_secret;
      }
      const res = await fetch("/api/admin/sso", {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      if (!res.ok) {
        const msg = await upstreamErrorMessage(res);
        throw new Error(msg);
      }
      return (await res.json()) as { config: AdminSSOConfig };
    },
    onSuccess: () => {
      // Wipe the just-typed secret so a second submit without re-entering
      // it sends an empty string (which the backend interprets as
      // "leave existing"). This is also a UX cue that the secret was
      // accepted and is no longer visible in plaintext anywhere.
      setForm((f) => ({ ...f, client_secret: "" }));
      setShowSuccess(true);
      // Re-fetch the GET so the form mirrors the persisted state.
      void queryClient.invalidateQueries({ queryKey: ["admin", "sso"] });
    },
  });

  // Auto-dismiss the success banner after ~3s.
  useEffect(() => {
    if (!showSuccess) return;
    const t = setTimeout(() => setShowSuccess(false), 3000);
    return () => clearTimeout(t);
  }, [showSuccess]);

  // Submit-button state machine — derived from the mutation state so
  // every render reflects truth without an extra state variable.
  const submitState: "idle" | "submitting" | "success" | "error" =
    mutation.isPending
      ? "submitting"
      : mutation.isError
        ? "error"
        : showSuccess
          ? "success"
          : "idle";

  function onSubmit(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setShowSuccess(false);
    mutation.mutate(form);
  }

  async function runPreflight() {
    if (!issuerURL.trim()) {
      setPreflight({ state: "error", message: "issuer URL is required" });
      return;
    }
    setPreflight({ state: "loading" });
    try {
      const base = issuerURL.replace(/\/+$/, "");
      const url = `${base}/.well-known/openid-configuration`;
      const res = await fetch(url, { method: "GET" });
      if (!res.ok) {
        setPreflight({
          state: "error",
          message: `discovery returned ${res.status}`,
        });
        return;
      }
      const body = (await res.json()) as {
        issuer?: string;
        authorization_endpoint?: string;
        token_endpoint?: string;
        jwks_uri?: string;
      };
      setPreflight({
        state: "ok",
        issuer: body.issuer,
        authorization_endpoint: body.authorization_endpoint,
        token_endpoint: body.token_endpoint,
        jwks_uri: body.jwks_uri,
      });
    } catch (err) {
      setPreflight({
        state: "error",
        message: (err as Error).message || "discovery fetch failed",
      });
    }
  }

  return (
    <PageShell
      title="SSO"
      subtitle={
        <>
          Configure a per-tenant <Term id="sso">OIDC identity provider</Term>.
          The platform is a relying party only — it never issues identity
          itself.
        </>
      }
    >
      <Card>
        <CardHeader>
          <CardTitle>Discovery preflight</CardTitle>
          <CardDescription>
            Hits <code>/.well-known/openid-configuration</code> at the given
            issuer URL and shows the endpoints the platform will use. Runs
            entirely in your browser — nothing is saved.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <label htmlFor="preflight-issuer" className="text-sm font-medium">
            Issuer URL
          </label>
          <div className="flex flex-col gap-2 sm:flex-row">
            <Input
              id="preflight-issuer"
              type="url"
              placeholder="https://accounts.google.com"
              value={issuerURL}
              onChange={(e) => setIssuerURL(e.target.value)}
              className="flex-1"
            />
            <Button
              type="button"
              onClick={runPreflight}
              disabled={preflight.state === "loading"}
            >
              {preflight.state === "loading" ? "Checking…" : "Run preflight"}
            </Button>
          </div>
          <PreflightResult value={preflight} />
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>OIDC configuration</CardTitle>
          <CardDescription>
            Provider issuer, client ID, redirect URL, and allowed email domains.
            The client secret is write-only — saved values never re-render in
            this form. Leave the secret field blank to keep the current value.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form
            onSubmit={onSubmit}
            className="space-y-4"
            data-testid="sso-config-form"
          >
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="Issuer URL">
                <Input
                  id="sso-issuer-url"
                  type="url"
                  placeholder="https://accounts.google.com"
                  value={form.issuer_url}
                  onChange={(e) =>
                    setForm({ ...form, issuer_url: e.target.value })
                  }
                  disabled={isLoading || mutation.isPending}
                />
              </Field>
              <Field label="Client ID">
                <Input
                  id="sso-client-id"
                  placeholder="opaque IdP-issued identifier"
                  value={form.client_id}
                  onChange={(e) =>
                    setForm({ ...form, client_id: e.target.value })
                  }
                  disabled={isLoading || mutation.isPending}
                />
              </Field>
              <Field label="Client secret (write-only)">
                <Input
                  id="sso-client-secret"
                  type="password"
                  placeholder={
                    data ? "leave blank to keep current" : "••••••••"
                  }
                  autoComplete="new-password"
                  value={form.client_secret}
                  onChange={(e) =>
                    setForm({ ...form, client_secret: e.target.value })
                  }
                  disabled={isLoading || mutation.isPending}
                />
              </Field>
              <Field label="Redirect URL">
                <Input
                  id="sso-redirect-url"
                  type="url"
                  placeholder="https://your-deployment.example/auth/oidc/callback"
                  value={form.redirect_url}
                  onChange={(e) =>
                    setForm({ ...form, redirect_url: e.target.value })
                  }
                  disabled={isLoading || mutation.isPending}
                />
              </Field>
              <Field
                label="Allowed email domains (comma-separated)"
                className="sm:col-span-2"
              >
                <Input
                  id="sso-allowed-domains"
                  placeholder="example.com, sub.example.com"
                  value={form.allowed_email_domains}
                  onChange={(e) =>
                    setForm({
                      ...form,
                      allowed_email_domains: e.target.value,
                    })
                  }
                  disabled={isLoading || mutation.isPending}
                />
              </Field>
              <div className="space-y-1.5 sm:col-span-2">
                <div className="flex items-start gap-2">
                  <Checkbox
                    id="sso-trust-unverified-email"
                    data-testid="sso-trust-unverified-email"
                    checked={form.trust_unverified_email_for_link}
                    onCheckedChange={(checked) =>
                      setForm({
                        ...form,
                        trust_unverified_email_for_link: checked === true,
                      })
                    }
                    disabled={isLoading || mutation.isPending}
                    aria-describedby="sso-trust-unverified-email-help"
                  />
                  <label
                    htmlFor="sso-trust-unverified-email"
                    className="text-sm font-medium"
                  >
                    Link SCIM users when the IdP does not report email_verified
                  </label>
                </div>
                <p
                  id="sso-trust-unverified-email-help"
                  className="text-xs text-muted-foreground"
                >
                  A first sign-in claims a SCIM-provisioned user by email only
                  when the IdP marks the email verified. Turn this on for IdPs
                  that never send <code>email_verified</code>, such as Microsoft
                  Entra ID. An IdP that reports the email unverified is still
                  refused. With this on, an email with no{" "}
                  <code>email_verified</code> claim also counts as verified for
                  the allowed email domains above, so turn it on only for an IdP
                  you trust to send real addresses.
                </p>
              </div>
            </div>

            {submitState === "success" ? (
              <Alert data-testid="sso-save-success">
                <AlertTitle>Saved</AlertTitle>
                <AlertDescription>
                  SSO configuration updated. The form has been re-loaded from
                  the server (the secret is never re-displayed).
                </AlertDescription>
              </Alert>
            ) : null}

            {submitState === "error" ? (
              <Alert variant="destructive" data-testid="sso-save-error">
                <AlertTitle>Save failed</AlertTitle>
                <AlertDescription>
                  {mutation.error instanceof Error
                    ? mutation.error.message
                    : "unknown error"}
                </AlertDescription>
              </Alert>
            ) : null}

            <Button
              type="submit"
              disabled={isLoading || mutation.isPending}
              className="w-full sm:w-auto"
              data-testid="sso-save-button"
            >
              {submitState === "submitting" ? "Saving…" : "Save"}
            </Button>
          </form>
        </CardContent>
      </Card>

      {deleteResult ? (
        <Alert data-testid="sso-delete-success">
          <AlertTitle>SSO configuration deleted</AlertTitle>
          <AlertDescription>
            Removed {plural(deleteResult.mapping_count, "mapping", "mappings")}{" "}
            and revoked{" "}
            {plural(deleteResult.revoked_role_count, "role", "roles")} from{" "}
            {plural(deleteResult.affected_user_count, "user", "users")}.
          </AlertDescription>
        </Alert>
      ) : null}

      {data ? (
        <DeleteSSOCard config={data} onDeleted={setDeleteResult} />
      ) : null}
    </PageShell>
  );
}

function plural(n: number, one: string, many: string): string {
  return `${n} ${n === 1 ? one : many}`;
}

// #2760: delete the tenant's IdP config. The confirm shows what goes with it:
// the group-role mappings scoped to it, and the group-derived roles only it
// derived (a role another source also derives, or the user holds manually,
// stays). The DELETE fires only from the dialog's confirm button.
function DeleteSSOCard({
  config,
  onDeleted,
}: {
  config: AdminSSOConfig;
  onDeleted: (r: AdminSSODeleteResult) => void;
}) {
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const mappings = config.mapping_count ?? 0;
  const roles = config.derived_role_count ?? 0;

  const del = useMutation({
    mutationFn: async () => {
      const res = await fetch("/api/admin/sso", { method: "DELETE" });
      if (!res.ok) {
        throw new Error(await upstreamErrorMessage(res));
      }
      return (await res.json()) as AdminSSODeleteResult;
    },
    onSuccess: (r) => {
      setOpen(false);
      onDeleted(r);
      void queryClient.invalidateQueries({ queryKey: ["admin", "sso"] });
    },
  });

  return (
    <Card data-testid="sso-delete-card">
      <CardHeader>
        <CardTitle>Delete SSO configuration</CardTitle>
        <CardDescription>
          Users can no longer sign in through this identity provider. Its
          group-role mappings are deleted, and roles granted only through them
          are revoked. Roles a user also gets from SCIM, another provider, or a
          manual assignment stay.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <Button
          type="button"
          variant="outline"
          onClick={() => {
            del.reset();
            setOpen(true);
          }}
          data-testid="sso-delete-trigger"
        >
          Delete SSO configuration
        </Button>
      </CardContent>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogPortal>
          <DialogContent data-testid="sso-delete-dialog">
            <DialogHeader>
              <DialogTitle>Delete the SSO configuration?</DialogTitle>
              <DialogDescription>
                This deletes the configuration for{" "}
                <span className="font-medium">{config.issuer_url}</span>. This
                cannot be undone.
              </DialogDescription>
            </DialogHeader>
            <ul className="list-disc space-y-1 pl-5 text-sm">
              <li data-testid="sso-delete-mapping-count">
                {plural(mappings, "group-role mapping", "group-role mappings")}{" "}
                will be deleted.
              </li>
              <li data-testid="sso-delete-role-count">
                {plural(roles, "group-derived role", "group-derived roles")}{" "}
                will be revoked.
              </li>
            </ul>
            {del.isError ? (
              <Alert variant="destructive" data-testid="sso-delete-error">
                <AlertTitle>Could not delete</AlertTitle>
                <AlertDescription>
                  {del.error instanceof Error
                    ? del.error.message
                    : "unknown error"}
                </AlertDescription>
              </Alert>
            ) : null}
            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                disabled={del.isPending}
                onClick={() => setOpen(false)}
              >
                Cancel
              </Button>
              <Button
                type="button"
                variant="destructive"
                disabled={del.isPending}
                onClick={() => del.mutate()}
                data-testid="sso-delete-confirm"
              >
                {del.isPending ? "Deleting..." : "Delete"}
              </Button>
            </DialogFooter>
          </DialogContent>
        </DialogPortal>
      </Dialog>
    </Card>
  );
}

function Field({
  label,
  children,
  className,
}: {
  label: string;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={"space-y-1.5 " + (className ?? "")}>
      <label className="text-sm font-medium">{label}</label>
      {children}
    </div>
  );
}

function PreflightResult({ value }: { value: Preflight }) {
  if (value.state === "idle") return null;
  if (value.state === "loading") {
    return (
      <p className="text-sm text-muted-foreground">
        Fetching discovery document…
      </p>
    );
  }
  if (value.state === "error") {
    return (
      <Alert variant="destructive">
        <AlertTitle>Discovery failed</AlertTitle>
        <AlertDescription>{value.message}</AlertDescription>
      </Alert>
    );
  }
  return (
    <Alert>
      <AlertTitle>Discovery OK</AlertTitle>
      <AlertDescription className="space-y-1 text-xs">
        {value.issuer ? (
          <div>
            <span className="font-medium">issuer:</span>{" "}
            <code>{value.issuer}</code>
          </div>
        ) : null}
        {value.authorization_endpoint ? (
          <div>
            <span className="font-medium">authorization_endpoint:</span>{" "}
            <code>{value.authorization_endpoint}</code>
          </div>
        ) : null}
        {value.token_endpoint ? (
          <div>
            <span className="font-medium">token_endpoint:</span>{" "}
            <code>{value.token_endpoint}</code>
          </div>
        ) : null}
        {value.jwks_uri ? (
          <div>
            <span className="font-medium">jwks_uri:</span>{" "}
            <code>{value.jwks_uri}</code>
          </div>
        ) : null}
      </AlertDescription>
    </Alert>
  );
}
