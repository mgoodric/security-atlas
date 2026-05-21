package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/mcp"
	"github.com/mgoodric/security-atlas/internal/mcp/tools"
)

// updateGolden is set when -update is passed to `go test`. Use to
// regenerate snapshots after an intentional schema change:
//
//	go test ./internal/mcp/... -run TestSchemaStability -update
var updateGolden = flag.Bool("update", false, "regenerate schema snapshots under testdata/")

// TestSchemaStability is the AC-15 schema-stability gate. It walks the
// six canonical tools, captures each tool's name + description +
// input schema, and asserts the output matches the committed snapshot
// in internal/mcp/testdata/. CI fails on drift; intentional schema
// changes must regenerate the snapshot (`go test ... -update`) and
// land in the same PR.
//
// Per slice-172 D2 + decisions log, schema changes are additive only:
// removing a field or changing its type is a breaking contract for
// existing MCP-client sessions and MUST be a separately-merged
// follow-on (slice-174-style) so a 30-day soak window observes the
// change before older clients hit it.
func TestSchemaStability(t *testing.T) {
	// Build the snapshot from a real client (the schemas are
	// constants, so the client URL is irrelevant — we never make an
	// HTTP call here).
	client, _ := mcp.NewClient("http://localhost:8080", "test-bearer", "v0.0.0-test")
	// Slice 173 expanded the surface to include write tools; the snapshot
	// now covers all 11 tools so a drift in any input schema (read OR
	// write) trips the gate.
	all := tools.AllWithWrites(client)

	type entry struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"inputSchema"`
	}
	snapshot := struct {
		Tools []entry `json:"tools"`
	}{}
	for _, tool := range all {
		def := tool.Definition()
		// Re-encode the input schema through encoding/json to
		// canonicalize whitespace so diffs are minimal.
		var canon any
		if err := json.Unmarshal(def.InputSchema, &canon); err != nil {
			t.Fatalf("tool %q has invalid input schema: %v", def.Name, err)
		}
		canonical, err := json.MarshalIndent(canon, "    ", "  ")
		if err != nil {
			t.Fatalf("tool %q schema marshal: %v", def.Name, err)
		}
		snapshot.Tools = append(snapshot.Tools, entry{
			Name:        def.Name,
			Description: def.Description,
			InputSchema: json.RawMessage(canonical),
		})
	}
	got, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	got = append(got, '\n')

	goldenPath := filepath.Join("testdata", "tools.golden.json")
	if *updateGolden {
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("updated %s", goldenPath)
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden (run with -update to create): %v", err)
	}
	if !bytes.Equal(bytes.TrimSpace(want), bytes.TrimSpace(got)) {
		// Emit a tractable diff hint.
		t.Errorf("schema snapshot drift; re-run with -update to regenerate %s.\n\n--- WANT ---\n%s\n\n--- GOT ---\n%s",
			goldenPath, string(want), string(got))
	}
}

// TestToolsListMatchesCanonicalOrder is the runtime sibling of the
// snapshot test — verifies the protocol-layer `tools/list` returns
// tools in CanonicalToolOrder. AC-15 gate at the wire-shape level.
func TestToolsListMatchesCanonicalOrder(t *testing.T) {
	t.Parallel()

	client, _ := mcp.NewClient("http://localhost:8080", "test-bearer", "v0.0.0-test")
	server := mcp.NewServer("atlas-mcp", "v0.0.0-test", tools.AllWithWrites(client), nil)

	var out bytes.Buffer
	if err := server.Run(context.Background(),
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`+"\n"),
		&out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var resp struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Result.Tools) != len(mcp.CanonicalToolOrder) {
		t.Fatalf("tools/list returned %d tools, want %d (P0-A10)",
			len(resp.Result.Tools), len(mcp.CanonicalToolOrder))
	}
	for i, want := range mcp.CanonicalToolOrder {
		if resp.Result.Tools[i].Name != want {
			t.Errorf("tools[%d] = %q, want %q",
				i, resp.Result.Tools[i].Name, want)
		}
	}
}
