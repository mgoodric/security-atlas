// #1747 — `atlas-cli policy import <dir>`.
//
// Reads every *.md file in <dir> (not recursive), one policy per file as
// Markdown with YAML front matter, and posts them to the admin-only
// POST /v1/admin/policies/import. The server parses and validates; the CLI
// only reads files and prints the per-file report. The front-matter keys
// are documented in internal/policy/importfile.
//
// Authentication and endpoint resolution follow `atlas-cli features`:
// $ATLAS_ADMIN_TOKEN (or --token), and --http-endpoint /
// $ATLAS_HTTP_ENDPOINT.

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

type policyImportFile struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

type policyImportReport struct {
	Committed bool `json:"committed"`
	Imported  int  `json:"imported"`
	Skipped   int  `json:"skipped"`
	Failed    int  `json:"failed"`
	Results   []struct {
		File   string `json:"file"`
		Status string `json:"status"`
		Reason string `json:"reason"`
		Policy *struct {
			ID string `json:"id"`
		} `json:"policy"`
	} `json:"results"`
}

func newPolicyImportCmd() *cobra.Command {
	var (
		httpEndpoint string
		partial      bool
	)
	cmd := &cobra.Command{
		Use:   "import <dir>",
		Short: "import a policy library with its approval history (admin only)",
		Long: `Imports every *.md file in <dir> as a published policy that keeps its
history. Each file is one policy: YAML front matter, then the policy text.

Required front matter: title, version, approved_by_name, approved_at,
effective_date (dates are YYYY-MM-DD).
Optional: owner_role, approver_role, next_review_at, source_url,
linked_controls (control UUIDs or bundle IDs), acknowledgment_required_roles.

By default nothing is written if any file fails validation; --partial
imports the valid files. A policy whose title and version already exist is
skipped and reported. The next version of an imported policy goes through
the normal draft, review, approve and publish workflow.`,
		Args:    cobra.ExactArgs(1),
		PreRunE: featuresPreRunE(&httpEndpoint),
		RunE: func(cmd *cobra.Command, args []string) error {
			files, err := readPolicyImportDir(args[0])
			if err != nil {
				return err
			}
			token, err := featuresAdminToken()
			if err != nil {
				return err
			}
			body, err := json.Marshal(map[string]any{"files": files, "partial": partial})
			if err != nil {
				return fmt.Errorf("encode request: %w", err)
			}
			respBody, status, err := featuresDoRequest(cmd.Context(), http.MethodPost, httpEndpoint+"/v1/admin/policies/import", body, token)
			if err != nil {
				return err
			}
			if status != http.StatusOK && status != http.StatusUnprocessableEntity {
				return fmt.Errorf("server returned %d: %s", status, strings.TrimSpace(string(respBody)))
			}
			var report policyImportReport
			if err := json.Unmarshal(respBody, &report); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}
			printPolicyImportReport(cmd.OutOrStdout(), report)
			if !report.Committed {
				return fmt.Errorf("import aborted: %d file(s) failed, nothing written (re-run with --partial to import the valid files)", report.Failed)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&httpEndpoint, "http-endpoint", "", "platform HTTP endpoint (env: ATLAS_HTTP_ENDPOINT, default: http://localhost:8080)")
	cmd.Flags().BoolVar(&partial, "partial", false, "import the valid files even if some fail")
	return cmd
}

// readPolicyImportDir reads the *.md files directly in dir, in name order.
func readPolicyImportDir(dir string) ([]policyImportFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	var files []policyImportFile
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".md") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		files = append(files, policyImportFile{Name: e.Name(), Content: string(b)})
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no .md files in %s", dir)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return files, nil
}

func printPolicyImportReport(out io.Writer, r policyImportReport) {
	for _, res := range r.Results {
		line := fmt.Sprintf("%-12s %s", res.Status, res.File)
		if res.Policy != nil {
			line += " -> " + res.Policy.ID
		}
		if res.Reason != "" {
			line += ": " + res.Reason
		}
		_, _ = fmt.Fprintln(out, line)
	}
	_, _ = fmt.Fprintf(out, "imported %d, skipped %d, failed %d\n", r.Imported, r.Skipped, r.Failed)
}
