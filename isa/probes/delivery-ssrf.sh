#!/usr/bin/env bash
#
# delivery-ssrf.sh — falsifier for isa/assessor-delivery.md ISC-5 (D3 · the
# egress boundary): "No delivery request reaches a private, loopback,
# link-local or metadata address... at registration time OR at delivery
# time. Both are required: a registration-time check alone loses to DNS
# rebinding."
#
# There is no assessor-delivery code yet (the epic is phase: specified,
# progress: 0 — see isa/assessor-delivery.md). What already exists, and what
# the epic's own Constraints commit its egress path to reusing verbatim
# ("The egress path reuses internal/notify — SSRFPolicy.ValidateWebhookURL
# ... a second SSRF implementation in this tree is a defect regardless of
# whether it is correct"), is internal/notify/webhook.NewHTTPTransport. So
# this probe attacks THAT primitive: whatever assessor-delivery inherits
# from it, it inherits today, before a single line of the epic is written.
#
# The attack: NewHTTPTransport resolves the configured host and validates
# every returned IP ONCE, at construction ("registration time"), then hands
# the validated URL string to a plain http.Client for every future Post()
# ("delivery time"). Nothing re-resolves or re-validates at send time. A
# host that answers a public address on the first lookup and a loopback
# address on every lookup after is registered successfully and then
# delivered straight to the loopback target — the exact DNS-rebinding gap
# ISC-5 names. The probe runs both halves for real: a throwaway DNS server
# answers the rebind, a throwaway TCP listener on 127.0.0.1 stands in for
# the internal target, and the probe reports FALSE only if that listener
# actually receives a connection.
#
# No database, NATS or MinIO is needed — the code path under test
# (internal/notify + internal/notify/webhook) is pure networking with no DB
# dependency, so this probe does not require the docker-compose stack the
# other ISC-5 siblings in this epic's Test Strategy table do.
#
# Exit 0  — the guard survived the rebind (claim holds under this attack).
# Exit 1  — the rebind reached the loopback target (claim is genuinely
#           false: a registration-time-only check, exactly as ISC-5 warns).
# Exit 3  — a precondition was missing (no go toolchain, or the harness
#           itself — the fake resolver / fake listener — never engaged), so
#           nothing was proven about the claim either way.

set -euo pipefail

if ! command -v go >/dev/null 2>&1; then
  echo "CANNOT RUN: no go toolchain on PATH" >&2
  exit 3
fi

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"

# The Go source has to live inside the module tree (not /tmp) so it can
# import internal/notify and internal/notify/webhook — Go's internal-import
# rule is keyed on the importing package's import path, and a file compiled
# from outside the module has no path under github.com/mgoodric/security-atlas
# to qualify. It is removed unconditionally on exit; nothing here is meant to
# survive the run.
SCRATCH="$REPO_ROOT/isc5-ssrf-probe-scratch"
rm -rf "$SCRATCH"
mkdir -p "$SCRATCH"
trap 'rm -rf "$SCRATCH"' EXIT

cat >"$SCRATCH/main.go" <<'GOEOF'
package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"sync/atomic"
	"time"

	"github.com/mgoodric/security-atlas/internal/notify/webhook"
)

// rebindHost never resolves anywhere real — every lookup for it is answered
// by the fake DNS server below, so no packet ever leaves this process.
const rebindHost = "rebind-target.isc5-ssrf-probe.internal"

func main() {
	// The "internal target" ISC-5 says a delivery must never reach. Proof
	// of compromise is a TCP connection landing here — deliberately below
	// the TLS/HTTP layer, since an SSRF has already happened the instant a
	// connection reaches an internal host, whether or not the app-layer
	// handshake that follows ever completes.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Println("CANNOT RUN: could not open loopback listener:", err)
		os.Exit(3)
	}
	defer func() { _ = ln.Close() }()

	var hits int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			atomic.AddInt32(&hits, 1)
			_ = conn.Close()
		}
	}()

	_, targetPort, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		fmt.Println("CANNOT RUN: could not read loopback listener port:", err)
		os.Exit(3)
	}

	var queries int32
	dnsAddr, stopDNS, err := startRebindingDNS(func() net.IP {
		n := atomic.AddInt32(&queries, 1)
		if n == 1 {
			// Registration-time lookup: answer public so the SSRF guard's
			// one-time check passes. 93.184.216.34 is the same "known
			// public" fixture internal/notify/webhook's own tests use.
			return net.ParseIP("93.184.216.34")
		}
		// Every later lookup — the one the http.Client performs when it
		// actually dials to deliver the payload — rebinds to the loopback
		// listener above.
		return net.ParseIP("127.0.0.1")
	})
	if err != nil {
		fmt.Println("CANNOT RUN: could not start fake DNS server:", err)
		os.Exit(3)
	}
	defer stopDNS()

	// Redirect ALL process DNS resolution — both the SSRF guard's
	// validation lookup and the http.Client's dial-time lookup share this
	// package-level resolver — to the fake server above. This is the
	// standard, network-safe way to simulate DNS rebinding: no real DNS
	// query is ever made.
	origResolver := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, dnsAddr)
		},
	}
	defer func() { net.DefaultResolver = origResolver }()

	cfg := webhook.Config{
		URL:     fmt.Sprintf("https://%s:%s/hook", rebindHost, targetPort),
		Timeout: 3 * time.Second,
	}
	// webhook.SSRFPolicy() is the exact strict policy production wires up —
	// the same policy the epic's Constraints require assessor-delivery to
	// reuse rather than re-implement.
	tr, err := webhook.NewHTTPTransport(cfg, webhook.SSRFPolicy())
	if err != nil {
		fmt.Println("CANNOT RUN: registration rejected before a rebind could be attempted:", err)
		os.Exit(3)
	}

	// The delivery attempt. Its error (TLS-against-a-raw-socket failure,
	// timeout, whatever) is expected and irrelevant — the compromise is
	// already complete the moment the TCP connection lands.
	_ = tr.Post(context.Background(), []byte(`{"probe":"isc-5-delivery-ssrf"}`))

	if atomic.LoadInt32(&queries) == 0 {
		fmt.Println("CANNOT RUN: fake DNS resolver was never queried — net.DefaultResolver override did not take effect in this environment")
		os.Exit(3)
	}

	if atomic.LoadInt32(&hits) > 0 {
		fmt.Println("FALSE: the webhook transport validated the delivery host once at registration time (resolved to a public address), then dialed the SAME host again at delivery time and reached 127.0.0.1 after it rebound — a registration-time-only SSRF check, exactly the gap ISC-5 names. assessor-delivery is committed to reusing this exact primitive (isa/assessor-delivery.md Constraints), so it inherits this gap unmodified.")
		os.Exit(1)
	}

	fmt.Println("the rebind did not reach the internal listener")
	os.Exit(0)
}

// startRebindingDNS starts a minimal authoritative DNS server on loopback.
// Go's resolver looks up A and AAAA concurrently for one LookupIP call, so
// ipForA is invoked ONLY for an actual A-type question — an AAAA query
// always gets an empty, no-error answer without consuming a call. That
// keeps "1st A query = registration, every later A query = dial" true
// regardless of which query type happens to arrive at the socket first.
// It returns the server's UDP address and a stop func.
func startRebindingDNS(ipForA func() net.IP) (addr string, stop func(), err error) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	go func() {
		buf := make([]byte, 512)
		for {
			n, raddr, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			resp := buildDNSResponse(buf[:n], ipForA)
			if resp != nil {
				_, _ = conn.WriteTo(resp, raddr)
			}
		}
	}()
	return conn.LocalAddr().String(), func() { _ = conn.Close() }, nil
}

// buildDNSResponse crafts a hand-built DNS response: the query's own
// question section copied back verbatim, plus one answer RR when the
// question is an A record (a compressed-name pointer to offset 12, TYPE A,
// CLASS IN, ipForA()'s address). Anything else (AAAA, etc.) gets a
// zero-answer, no-error response, and never calls ipForA.
func buildDNSResponse(query []byte, ipForA func() net.IP) []byte {
	if len(query) < 12 {
		return nil
	}
	i := 12
	for i < len(query) && query[i] != 0 {
		i += int(query[i]) + 1
	}
	i++    // the terminating zero-length label
	i += 4 // QTYPE + QCLASS
	if i > len(query) {
		return nil
	}
	question := query[12:i]
	qtype := binary.BigEndian.Uint16(query[i-4 : i-2])

	resp := make([]byte, 0, 64+len(question))
	resp = append(resp, query[0], query[1]) // echo the query ID
	resp = append(resp, 0x81, 0x80)         // response, recursion desired+available, no error
	resp = append(resp, 0x00, 0x01)         // QDCOUNT=1

	const typeA = 1
	if qtype == typeA {
		if ip4 := ipForA().To4(); ip4 != nil {
			resp = append(resp, 0x00, 0x01)             // ANCOUNT=1
			resp = append(resp, 0x00, 0x00, 0x00, 0x00) // NSCOUNT=0, ARCOUNT=0
			resp = append(resp, question...)
			resp = append(resp, 0xC0, 0x0C)             // name = pointer to offset 12
			resp = append(resp, 0x00, 0x01)             // TYPE A
			resp = append(resp, 0x00, 0x01)             // CLASS IN
			resp = append(resp, 0x00, 0x00, 0x00, 0x00) // TTL 0 — never cache
			resp = append(resp, 0x00, 0x04)             // RDLENGTH
			resp = append(resp, ip4...)
			return resp
		}
	}
	resp = append(resp, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00) // ANCOUNT/NSCOUNT/ARCOUNT=0
	resp = append(resp, question...)
	return resp
}
GOEOF

set +e
OUTPUT="$(go run "$SCRATCH/main.go" 2>&1)"
STATUS=$?
set -e

echo "$OUTPUT"
exit "$STATUS"
