// Slice 060 — /admin/sso (AC-2).
//
// BACKEND GAP: as of slice 060 there is no `/v1/admin/sso` HTTP endpoint
// for CRUD on `oidc_idp_configs`. Slice 034 ships the table + the OIDC
// callback handlers, but exposes no admin REST surface. The follow-up
// slice (tentatively 060.5) ships the endpoint; this page lands the UX
// shell so the admin overview renders consistently.
//
// What this page DOES today:
//   - Renders the OIDC config form (visual scaffold)
//   - Runs a client-side discovery preflight (fetch IdP's
//     .well-known/openid-configuration directly) — this is independent of
//     the backend gap and useful to wire-test an IdP URL today.
//   - Surfaces the gap prominently so an operator doesn't waste time
//     filling the form before save lands.
//
// Anti-criterion P0: when save DOES land, the `client_secret` field is
// write-only on the API. The audit log records "SSO config changed" but
// not the secret value. This UI keeps the secret in a password-type
// input and never re-fetches it.

"use client";

import { useState } from "react";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

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

export default function SSOConfigPage() {
  const [issuerURL, setIssuerURL] = useState("");
  const [preflight, setPreflight] = useState<Preflight>({ state: "idle" });

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
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">SSO</h1>
        <p className="text-sm text-muted-foreground">
          Configure a per-tenant OIDC identity provider. The platform is a
          relying party only — it never issues identity itself.
        </p>
      </div>

      <Alert>
        <AlertTitle>Backend save endpoint not yet shipped</AlertTitle>
        <AlertDescription>
          As of slice 060, the <code>/v1/admin/sso</code> CRUD endpoint is not
          on main. The OIDC config table (<code>oidc_idp_configs</code>) and the
          runtime callback handlers ship in slice 034; the admin REST surface
          ships in the follow-up slice. You can preflight an IdP discovery URL
          below today, and configure SSO via direct DB insert as a stopgap.
        </AlertDescription>
      </Alert>

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
          <label htmlFor="issuer" className="text-sm font-medium">
            Issuer URL
          </label>
          <div className="flex flex-col gap-2 sm:flex-row">
            <Input
              id="issuer"
              type="url"
              placeholder="https://accounts.google.com"
              value={issuerURL}
              onChange={(e) => setIssuerURL(e.target.value)}
              className="flex-1"
            />
            <Button
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
          <CardTitle>OIDC configuration (form scaffold)</CardTitle>
          <CardDescription>
            Form layout that the save endpoint will bind to. Provider, client
            ID, redirect URL, and allowed email domains. The client secret field
            is write-only and never re-fetched.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Provider name">
              <Input placeholder="e.g. google, okta, custom" disabled />
            </Field>
            <Field label="Client ID">
              <Input placeholder="opaque IdP-issued identifier" disabled />
            </Field>
            <Field label="Client secret (write-only)">
              <Input
                type="password"
                placeholder="••••••••"
                autoComplete="new-password"
                disabled
              />
            </Field>
            <Field label="Redirect URL">
              <Input
                placeholder="https://your-deployment.example/auth/oidc/callback"
                disabled
              />
            </Field>
            <Field
              label="Allowed email domains (comma-separated)"
              className="sm:col-span-2"
            >
              <Input placeholder="example.com, sub.example.com" disabled />
            </Field>
          </div>
          <Button disabled className="w-full sm:w-auto">
            Save (backend pending)
          </Button>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>HITL review note</CardTitle>
          <CardDescription>
            This page is one of three flagged for human review on the slice 060
            PR.
          </CardDescription>
        </CardHeader>
        <CardContent className="text-sm">
          <Badge variant="outline">awaiting human review</Badge> See{" "}
          <code>docs/audit-log/admin-ui-review.md</code> for the SSO callback
          URL preflight sign-off line.
        </CardContent>
      </Card>
    </div>
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
