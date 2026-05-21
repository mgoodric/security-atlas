// tenant-broadcast.ts — slice 199 cross-tab tenant-switch sync.
//
// Thin wrapper around the same-origin BroadcastChannel API. When the
// operator switches tenants in one tab, sibling tabs in the same
// origin receive a "go re-fetch" nudge so their <TenantSwitcher>
// renders the new active tenant without a manual refresh.
//
// CONSTITUTIONAL INVARIANTS HONORED:
//
//   - Invariant 6 (tenant isolation at DB layer): the receiving tab
//     does NOT trust the broadcast payload as authority. Its
//     onTenantSwitched callback re-fetches /api/me/tenants — the
//     JWT-middleware-gated, RLS-backed source of truth — and renders
//     from that result. The broadcast carries only an opaque
//     identifier; it does NOT grant or imply access.
//   - P0-199-1: graceful no-op when BroadcastChannel is undefined
//     (SSR, older browsers, Safari quirks). No ReferenceError. No
//     thrown exception.
//   - P0-199-3: channel name is the literal "atlas-tenant" — unique
//     to this app within the origin so it does not collide with
//     other products on the same host.
//   - P0-199-4: this module does NOT re-broadcast on receive. The
//     post path is callable only from outside (the <TenantSwitcher>
//     onPick handler). The listener never invokes post directly or
//     transitively. The two functions are independent surfaces; no
//     feedback edge exists between them.
//   - P0-199-5: onTenantSwitched returns an unsubscribe that closes
//     the channel. Callers MUST invoke the returned unsubscribe in
//     their useEffect cleanup to avoid leaks.

const CHANNEL_NAME = "atlas-tenant";
const MESSAGE_TYPE = "tenant-switched";

export interface TenantSwitchedMessage {
  type: typeof MESSAGE_TYPE;
  targetTenantId: string;
  ts: number;
}

// Type guard for narrowing untrusted message payloads coming off the
// channel. Returns true iff the input has the exact shape we expect.
// Anything else is dropped silently — the channel is shared per
// origin but the message namespace is ours; foreign-shape messages
// are not our concern (and could be from a future protocol version
// running in another tab, which we want to ignore rather than crash).
function isTenantSwitchedMessage(v: unknown): v is TenantSwitchedMessage {
  if (typeof v !== "object" || v === null) return false;
  const obj = v as Record<string, unknown>;
  if (obj.type !== MESSAGE_TYPE) return false;
  if (typeof obj.targetTenantId !== "string") return false;
  if (obj.targetTenantId.length === 0) return false;
  if (typeof obj.ts !== "number") return false;
  return true;
}

// hasBroadcastChannel checks whether the runtime exposes the
// BroadcastChannel constructor. Wrapped in a function so test files
// can stub `globalThis.BroadcastChannel` between tests and the
// check re-evaluates each call (no module-load-time capture).
function hasBroadcastChannel(): boolean {
  return typeof BroadcastChannel !== "undefined";
}

// postTenantSwitched broadcasts a tenant-switch nudge to sibling
// tabs. The channel is opened, the message is posted, the channel
// is closed — a fire-and-forget contract. Graceful no-op if
// BroadcastChannel is unavailable.
//
// CRITICAL: this is the ONLY post path in the module. It is callable
// only from outside (e.g., the <TenantSwitcher> onPick handler). The
// listener does not invoke this function — that would create the
// infinite loop P0-199-4 forbids.
export function postTenantSwitched(targetTenantId: string): void {
  if (!hasBroadcastChannel()) return;
  if (typeof targetTenantId !== "string" || targetTenantId.length === 0) {
    return;
  }
  let bc: BroadcastChannel | null = null;
  try {
    bc = new BroadcastChannel(CHANNEL_NAME);
    const msg: TenantSwitchedMessage = {
      type: MESSAGE_TYPE,
      targetTenantId,
      ts: Date.now(),
    };
    bc.postMessage(msg);
  } catch {
    // The BroadcastChannel constructor or postMessage can throw in
    // some Safari versions when the page is in a backgrounded
    // bfcache state. Treat as no-op — the sibling tab will sync via
    // its existing 60s periodic re-fetch (slice 192 D1) anyway.
  } finally {
    if (bc) {
      try {
        bc.close();
      } catch {
        // ignore
      }
    }
  }
}

// onTenantSwitched subscribes to tenant-switch nudges from sibling
// tabs. The supplied callback fires for every well-formed
// `tenant-switched` message. Returns an unsubscribe function the
// caller MUST invoke on teardown (e.g., useEffect cleanup) to close
// the channel.
//
// Graceful no-op if BroadcastChannel is unavailable — the returned
// unsubscribe is a function that does nothing, so callers can wire
// it into cleanup unconditionally.
export function onTenantSwitched(
  cb: (msg: TenantSwitchedMessage) => void,
): () => void {
  if (!hasBroadcastChannel()) {
    return () => {};
  }
  let bc: BroadcastChannel;
  try {
    bc = new BroadcastChannel(CHANNEL_NAME);
  } catch {
    return () => {};
  }
  const handler = (ev: MessageEvent) => {
    const data = ev.data;
    if (!isTenantSwitchedMessage(data)) return;
    cb(data);
  };
  bc.addEventListener("message", handler);
  return () => {
    try {
      bc.removeEventListener("message", handler);
      bc.close();
    } catch {
      // ignore — already closed or never opened
    }
  };
}

// Exported for tests. Not part of the public API — consumers should
// import the named function exports above.
export const __TEST_ONLY = {
  CHANNEL_NAME,
  MESSAGE_TYPE,
  isTenantSwitchedMessage,
};
