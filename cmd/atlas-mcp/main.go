// Package main is the atlas-mcp binary entrypoint. Slice 172 — MCP
// (Model Context Protocol) server foundation + six read-only tools.
//
// The binary speaks MCP over stdio (newline-delimited JSON-RPC 2.0).
// Claude Desktop and Claude Code launch this binary as a subprocess,
// pass the bearer token via env, and consume the JSON-RPC stream on
// stdout. See cmd/atlas-mcp/README.md for the client config snippets.
//
// Security posture (slice 172 anti-criteria):
//
//   - P0-A1: bearer token via ATLAS_BEARER_TOKEN env or --token-file
//     positional. NEVER via a --token=<value> flag (visible in `ps`).
//   - P0-A4: every outbound HTTP request carries
//     `User-Agent: atlas-mcp/<version> (mcp; ai_assisted=read-only)`.
//   - P0-A7: stderr is the per-call envelope only
//     (`mcp tool=<name> duration_ms=<n> status=<ok|err>`); never
//     contains tool input or output bodies.
//
// Exit codes:
//
//	0 — clean shutdown (stdin closed; client disconnected)
//	1 — runtime error (HTTP transport failure, unrecoverable I/O)
//	2 — configuration error (missing token, bad base URL, etc.)
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/mgoodric/security-atlas/internal/mcp"
	"github.com/mgoodric/security-atlas/internal/mcp/tools"
)

// Build-time variables populated via -ldflags by GoReleaser at release
// time. When the binary is built from a working tree without ldflags
// (`go run`, `go build` for local dev, `go test`), these stay at their
// zero-value placeholders.
//
// Linker flag pattern:
//
//	-X main.version=v0.1.0 -X main.commit=<sha> -X main.date=<iso8601>
//
// Keep these as package-level vars (not consts) so the linker can
// override them.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// Env-var names. Centralized so the README + tests reference one place.
const (
	envBearer = "ATLAS_BEARER_TOKEN"
	envURL    = "ATLAS_BASE_URL"
)

func main() {
	exit, err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "atlas-mcp: %s\n", err.Error())
	}
	os.Exit(exit)
}

// run is the testable main. Returns (exitCode, error). Splitting out
// from main() lets unit tests assert exit codes without spawning a
// subprocess.
func run(args []string, stdin *os.File, stdout, stderr *os.File) (int, error) {
	fset := flag.NewFlagSet("atlas-mcp", flag.ContinueOnError)
	fset.SetOutput(stderr)
	showVersion := fset.Bool("version", false, "print version and exit")
	tokenFile := fset.String("token-file", "", "path to a file containing the bearer token (alternative to ATLAS_BEARER_TOKEN env)")
	baseURL := fset.String("base-url", "", "platform base URL (alternative to ATLAS_BASE_URL env; default http://localhost:8080)")

	if err := fset.Parse(args); err != nil {
		return 2, err
	}
	if *showVersion {
		_, _ = fmt.Fprintf(stdout, "atlas-mcp %s (commit %s, built %s, %s/%s, %s)\n",
			version, commit, date, runtime.GOOS, runtime.GOARCH, runtime.Version())
		return 0, nil
	}

	// Resolve base URL: flag > env > default.
	baseAddr := *baseURL
	if baseAddr == "" {
		baseAddr = os.Getenv(envURL)
	}
	if baseAddr == "" {
		baseAddr = "http://localhost:8080"
	}

	// Resolve bearer token: --token-file > ATLAS_BEARER_TOKEN env.
	// P0-A1: NEVER a --token=<value> flag (visible in `ps`).
	bearer, err := resolveBearer(*tokenFile)
	if err != nil {
		return 2, err
	}

	client, err := mcp.NewClient(baseAddr, bearer, version)
	if err != nil {
		return 2, fmt.Errorf("init client: %w", err)
	}

	toolset := tools.All(client)
	logEnv := newStderrLogger(stderr)

	// Wrap each tool with the P0-A7 one-line stderr envelope. The
	// per-tool Definition() and Handle() flow through unchanged; the
	// envelope captures the wall-clock and ok/err status only.
	wrapped := make([]mcp.Tool, len(toolset))
	for i, t := range toolset {
		wrapped[i] = wrapWithLogger(t, logEnv)
	}

	server := mcp.NewServer("atlas-mcp", version, wrapped, logEnv)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle SIGINT / SIGTERM gracefully so a kill from the MCP
	// client (Claude Desktop closing the subprocess) is a clean
	// shutdown rather than a half-flushed stream.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
	}()
	defer signal.Stop(sigCh)

	if err := server.Run(ctx, stdin, stdout); err != nil {
		if errors.Is(err, context.Canceled) {
			return 0, nil
		}
		return 1, fmt.Errorf("server run: %w", err)
	}
	return 0, nil
}

// resolveBearer returns the bearer token from the file path (if non-empty)
// or from the ATLAS_BEARER_TOKEN env var. Returns a config error (exit 2)
// when neither is set or the file is unreadable.
//
// P0-A1: this function is the ONLY entrypoint that reads the bearer.
// There is no `--token=<value>` flag and no positional argument that
// carries the token literal.
func resolveBearer(tokenFile string) (string, error) {
	if tokenFile != "" {
		data, err := os.ReadFile(tokenFile)
		if err != nil {
			// Distinguish "file missing" from "permission denied"
			// so the operator gets actionable diagnostics.
			if errors.Is(err, fs.ErrNotExist) {
				return "", fmt.Errorf("token file not found: %s", tokenFile)
			}
			return "", fmt.Errorf("read token file %s: %w", tokenFile, err)
		}
		t := strings.TrimSpace(string(data))
		if t == "" {
			return "", fmt.Errorf("token file %s is empty", tokenFile)
		}
		return t, nil
	}
	t := strings.TrimSpace(os.Getenv(envBearer))
	if t == "" {
		return "", fmt.Errorf("bearer token required: set %s env var or pass --token-file <path>", envBearer)
	}
	return t, nil
}

// newStderrLogger returns the one-line-per-event stderr emitter that
// the server + tool envelope share. Bounded to NO tool input/output
// bodies (P0-A7).
func newStderrLogger(w *os.File) func(format string, args ...any) {
	return func(format string, args ...any) {
		_, _ = fmt.Fprintf(w, "atlas-mcp "+format+"\n", args...)
	}
}

// loggingTool wraps a mcp.Tool with the P0-A7 envelope. We measure
// wall-clock around Handle and emit one line per call. The envelope
// records tool name, duration, and ok/err status — NEVER the arguments
// or the result body.
type loggingTool struct {
	inner mcp.Tool
	log   func(format string, args ...any)
}

func wrapWithLogger(t mcp.Tool, log func(format string, args ...any)) mcp.Tool {
	return &loggingTool{inner: t, log: log}
}

// Definition delegates to the wrapped tool.
func (l *loggingTool) Definition() mcp.ToolDefinition { return l.inner.Definition() }

// Handle delegates with timing + status logging.
func (l *loggingTool) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	start := time.Now()
	out, err := l.inner.Handle(ctx, args)
	status := "ok"
	if err != nil {
		status = "err"
	}
	l.log("tool=%s duration_ms=%d status=%s",
		l.inner.Definition().Name,
		time.Since(start).Milliseconds(),
		status)
	return out, err
}
