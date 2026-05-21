// /oauth/device — slice 191 device approval UI.
//
// The CLI's `atlas login` command prints a URL pointing at this
// page along with a short user_code (e.g., ABCD-2345). The user
// opens the URL in a browser, authenticates via the OIDC RP, and
// arrives here with an authenticated session. The page renders
// the user_code (pre-filled from `?user_code=` when present) plus
// Approve/Deny buttons.
//
// Approve POSTs to `/oauth/device_authorization/approve`; Deny
// POSTs to `/oauth/device_authorization/deny`. The Go handlers
// (internal/api/oauth/device_approve.go) read the OIDC session
// from the request context, write the approving user's identity
// snapshot onto the `oauth_device_codes` row, and the CLI's next
// poll succeeds.
//
// AUTHENTICATION: requires an authenticated session. The route is
// NOT in the JWT-bypass list (see internal/api/httpserver.go
// `jwtBypass` narrowing in slice 191) — unauthenticated visits
// return 401 from the underlying approve/deny endpoints and the
// page surfaces that as a sign-in CTA.
//
// SECURITY NOTES:
//   - The user_code is short (8 chars, alphabet ABCDEFGHJKLMNPQRSTUVWXYZ23456789);
//     brute-force is bounded by the 15-minute TTL + per-client_id
//     rate limit on the underlying /oauth/device_authorization
//     endpoint, plus the per-device_code poll throttle on
//     /oauth/token.
//   - The page does NOT echo any device_code; the long secret
//     never leaves the CLI.

"use client";

import { useEffect, useState } from "react";

interface ApprovalState {
  status: "idle" | "submitting" | "approved" | "denied" | "error";
  message: string;
}

export default function DeviceApprovalPage() {
  const [userCode, setUserCode] = useState("");
  const [state, setState] = useState<ApprovalState>({
    status: "idle",
    message: "",
  });

  // Pre-fill the user_code field from `?user_code=` if present —
  // the verification_uri_complete shortcut sends the user here
  // with the code already in the URL. The user only needs to
  // click Approve.
  useEffect(() => {
    if (typeof window === "undefined") return;
    const params = new URLSearchParams(window.location.search);
    const fromURL = params.get("user_code") || "";
    if (fromURL) {
      setUserCode(fromURL.toUpperCase());
    }
  }, []);

  async function post(action: "approve" | "deny") {
    if (!userCode) {
      setState({
        status: "error",
        message: "Enter the code from your terminal.",
      });
      return;
    }
    setState({ status: "submitting", message: "" });
    try {
      const resp = await fetch(`/oauth/device_authorization/${action}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({ user_code: userCode }),
      });
      if (!resp.ok) {
        const text = await resp.text();
        setState({
          status: "error",
          message: `Server returned ${resp.status}: ${text.slice(0, 200)}`,
        });
        return;
      }
      setState({
        status: action === "approve" ? "approved" : "denied",
        message:
          action === "approve"
            ? "Device approved. Return to your terminal — `atlas login` will complete shortly."
            : "Device denied. You can close this tab.",
      });
    } catch (err) {
      setState({
        status: "error",
        message: `Network error: ${
          err instanceof Error ? err.message : String(err)
        }`,
      });
    }
  }

  const disabled =
    state.status === "submitting" ||
    state.status === "approved" ||
    state.status === "denied";

  return (
    <main
      style={{
        maxWidth: "32rem",
        margin: "4rem auto",
        padding: "2rem",
        fontFamily: "system-ui, sans-serif",
      }}
    >
      <h1 style={{ fontSize: "1.5rem", marginBottom: "1rem" }}>
        Approve device sign-in
      </h1>
      <p style={{ color: "#555", marginBottom: "1.5rem" }}>
        A command-line tool is requesting permission to act on your behalf.
        Confirm the code below matches the one shown in your terminal, then
        click <strong>Approve</strong>.
      </p>

      <label
        htmlFor="user-code-input"
        style={{ display: "block", marginBottom: "0.5rem", fontWeight: 600 }}
      >
        Device code
      </label>
      <input
        id="user-code-input"
        type="text"
        value={userCode}
        onChange={(e) => setUserCode(e.target.value.toUpperCase())}
        placeholder="ABCD-2345"
        disabled={disabled}
        style={{
          width: "100%",
          padding: "0.75rem",
          fontSize: "1.25rem",
          fontFamily: "monospace",
          letterSpacing: "0.1em",
          border: "1px solid #ccc",
          borderRadius: "0.375rem",
          marginBottom: "1.5rem",
        }}
      />

      <div style={{ display: "flex", gap: "0.75rem" }}>
        <button
          type="button"
          onClick={() => post("approve")}
          disabled={disabled || !userCode}
          style={{
            flex: 1,
            padding: "0.75rem",
            fontSize: "1rem",
            fontWeight: 600,
            background: disabled ? "#ccc" : "#0d9488",
            color: "white",
            border: 0,
            borderRadius: "0.375rem",
            cursor: disabled ? "not-allowed" : "pointer",
          }}
        >
          Approve
        </button>
        <button
          type="button"
          onClick={() => post("deny")}
          disabled={disabled || !userCode}
          style={{
            flex: 1,
            padding: "0.75rem",
            fontSize: "1rem",
            fontWeight: 600,
            background: "transparent",
            color: "#dc2626",
            border: "1px solid #dc2626",
            borderRadius: "0.375rem",
            cursor: disabled ? "not-allowed" : "pointer",
          }}
        >
          Deny
        </button>
      </div>

      {state.message ? (
        <p
          style={{
            marginTop: "1.5rem",
            padding: "0.75rem",
            background: state.status === "error" ? "#fef2f2" : "#f0fdf4",
            border: `1px solid ${
              state.status === "error" ? "#fecaca" : "#bbf7d0"
            }`,
            borderRadius: "0.375rem",
            color: state.status === "error" ? "#991b1b" : "#166534",
          }}
        >
          {state.message}
        </p>
      ) : null}
    </main>
  );
}
