//go:build integration

// Integration tests for slice 022: PDF render via chromedp.
//
// AC-5: GET /v1/policies/{id}/pdf returns a real PDF (not a stub).
// The test boots headless Chrome (chromedp DefaultExecAllocator) and
// asserts the returned bytes begin with `%PDF-`. When Chrome is not
// available on the host the test is skipped gracefully so the slice
// is still buildable in environments without Chromium.
//
// Run with: go test -tags=integration ./internal/policy/pdf/...

package pdf_test

import (
	"context"
	"errors"
	"testing"
	"time"

	policypdf "github.com/mgoodric/security-atlas/internal/policy/pdf"
)

func TestRender_ProducesRealPDF(t *testing.T) {
	// Slice 340 re-enable: the chromedp websocket-url-timeout flake
	// (5 consecutive CI failures across slices 312/315/320, all at
	// exactly 20.04s) was diagnosed as chromedp's hardcoded 20s
	// wsURLReadTimeout firing before Chrome on the GHA runner could
	// print its `DevTools listening on ws://...` line to stderr,
	// stretched by Harden-Runner audit-mode egress instrumentation
	// (slice 117). Fix landed in render.go: WSURLReadTimeout(60s) +
	// DefaultTimeout 30s→90s. Full diagnosis at
	// docs/audit-log/340-chromedp-flake-decisions.md.
	doc := policypdf.Doc{
		Title:         "Test Policy",
		Version:       "1.0.0",
		EffectiveDate: "2026-05-12",
		OwnerRole:     "tenant_admin",
		ApproverRole:  "security_lead",
		Status:        "published",
		BodyMd: `# Purpose

This is a test policy used by the slice 022 PDF render integration test.

## Scope

The scope is the test environment only.

- Bullet one
- Bullet two
- Bullet three

## Policy

The body is intentionally short so the render completes quickly.
`,
	}
	ctx, cancel := context.WithTimeout(context.Background(), policypdf.DefaultTimeout)
	defer cancel()

	pdfBytes, err := policypdf.Render(ctx, doc)
	if err != nil {
		if errors.Is(err, policypdf.ErrChromeUnavailable) {
			t.Skipf("chromedp could not launch Chrome: %v", err)
		}
		t.Fatalf("Render: %v", err)
	}
	if len(pdfBytes) < 5 {
		t.Fatalf("expected non-trivial PDF output, got %d bytes", len(pdfBytes))
	}
	prefix := string(pdfBytes[:5])
	if prefix != "%PDF-" {
		t.Fatalf("expected leading magic bytes `%%PDF-`, got %q (first 16 bytes: %q)",
			prefix, safe(pdfBytes, 16))
	}
}

// TestRender_ProducesRealPDF_FiveIterations stress-tests the chromedp
// render path under repeated cold-start load. Satisfies slice 340 AC-4
// ("consecutive CI runs without flaking") via an in-test loop rather
// than a CI matrix-strategy — the loop is cheaper to maintain, lives
// next to the assertion it stresses, and doesn't require a temporary
// workflow change.
//
// Iteration count is 5: with the slice 340 DefaultTimeout raised to
// 90s, a 10-iteration ceiling would be 15 minutes per run, exceeding
// typical CI job-step budgets. Expected wall-clock is ~10-15s total
// (warm renders complete in <2s each); the timeout is a safety net,
// not a target. The name was rebased from `_TenIterations` to
// `_FiveIterations` by slice 348 U-4 — the original name reflected an
// earlier slice 340 AC-4 draft that called for 10; the merged AC and
// the running body both settled on 5, and the audit (slice 334 U-4)
// flagged the name-to-behavior drift.
//
// Fail-fast on the first failed iteration is intentional: the test's
// purpose is detecting flakes, and one failed iteration IS the signal
// we want to surface. Collecting all failures and asserting at the end
// would dilute that signal.
func TestRender_ProducesRealPDF_FiveIterations(t *testing.T) {
	doc := policypdf.Doc{
		Title:         "Test Policy",
		Version:       "1.0.0",
		EffectiveDate: "2026-05-12",
		OwnerRole:     "tenant_admin",
		ApproverRole:  "security_lead",
		Status:        "published",
		BodyMd: `# Purpose

This is a test policy used by the slice 340 stress-test loop.

## Scope

The scope is the test environment only.

- Bullet one
- Bullet two
- Bullet three

## Policy

The body is intentionally short so each iteration completes quickly.
`,
	}

	const iterations = 5
	for i := 1; i <= iterations; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), policypdf.DefaultTimeout)
		pdfBytes, err := policypdf.Render(ctx, doc)
		cancel()
		if err != nil {
			if errors.Is(err, policypdf.ErrChromeUnavailable) {
				t.Skipf("chromedp could not launch Chrome on iteration %d/%d: %v", i, iterations, err)
			}
			t.Fatalf("iteration %d/%d: Render: %v", i, iterations, err)
		}
		if len(pdfBytes) < 5 {
			t.Fatalf("iteration %d/%d: expected non-trivial PDF output, got %d bytes",
				i, iterations, len(pdfBytes))
		}
		prefix := string(pdfBytes[:5])
		if prefix != "%PDF-" {
			t.Fatalf("iteration %d/%d: expected leading magic bytes `%%PDF-`, got %q (first 16 bytes: %q)",
				i, iterations, prefix, safe(pdfBytes, 16))
		}
	}
}

// TestRender_CancelledContext verifies that an already-cancelled ctx
// surfaces quickly (defense-in-depth against runaway renders in tests).
func TestRender_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	start := time.Now()
	_, err := policypdf.Render(ctx, policypdf.Doc{Title: "x", Version: "1", OwnerRole: "o", ApproverRole: "a", Status: "draft", BodyMd: "x"})
	if err == nil {
		t.Fatal("expected error with cancelled context")
	}
	if elapsed := time.Since(start); elapsed > policypdf.DefaultTimeout {
		t.Fatalf("expected fast return on cancelled ctx, took %v", elapsed)
	}
}

func safe(b []byte, n int) string {
	if len(b) < n {
		n = len(b)
	}
	return string(b[:n])
}
