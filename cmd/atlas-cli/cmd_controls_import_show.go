package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/cmd/atlas-cli/cmdhttp"
	"github.com/mgoodric/security-atlas/internal/controlimport"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"
)

// newControlsImportShowCmd registers `controls import show <batch>` (#1780):
// list a batch's staged rows with their ranked SCF anchor candidates, each
// labelled with its source and how many of the row's tested criteria it
// reaches, from GET /v1/controls/imports/{id}/rows.
func newControlsImportShowCmd() *cobra.Command {
	var (
		status     string
		unreached  bool
		jsonOutput bool
	)
	cmd := &cobra.Command{
		Use:   "show <batch>",
		Short: "list an import batch's staged controls with their SCF anchor candidates",
		Long: "Print each staged control of an import batch with its ranked SCF anchor candidates. " +
			"Each candidate shows its source (crosswalk: its framework mappings reach the control's " +
			"tested criteria; keyword: a catalog keyword match) and how many of the tested criteria " +
			"it reaches. --status filters by row status; --no-candidate-reaches keeps only controls " +
			"whose tested criteria no candidate reaches.",
		Args: cobra.ExactArgs(1),
		PreRunE: func(_ *cobra.Command, _ []string) error {
			return resolveCommon()
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			q := url.Values{}
			if status != "" {
				q.Set("status", status)
			}
			if unreached {
				q.Set("no_candidate_reaches", "true")
			}
			path := "/v1/controls/imports/" + url.PathEscape(args[0]) + "/rows"
			if len(q) > 0 {
				path += "?" + q.Encode()
			}
			raw, err := getControlImport(path)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if jsonOutput {
				var pretty bytes.Buffer
				if err := json.Indent(&pretty, raw, "", "  "); err != nil {
					return fmt.Errorf("format response: %w", err)
				}
				_, _ = fmt.Fprintln(out, pretty.String())
				return nil
			}
			var resp controlimport.RowList
			if err := json.Unmarshal(raw, &resp); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}
			renderImportRows(out, resp)
			return nil
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "only rows with this status ("+strings.Join(controlimport.RowStatuses, ", ")+")")
	cmd.Flags().BoolVar(&unreached, "no-candidate-reaches", false, "only rows whose tested criteria no candidate reaches")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print the API response as JSON")
	return cmd
}

func getControlImport(path string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(common.endpoint, "/")+path, nil)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set(sdk.MetadataAuthorization, sdk.BearerPrefix+common.token)
	resp, err := cmdhttp.Client(time.Minute).Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		var e struct {
			Error string `json:"error"`
		}
		msg := strings.TrimSpace(string(body))
		if json.Unmarshal(body, &e) == nil && e.Error != "" {
			msg = e.Error
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, msg)
	}
	return body, nil
}

// renderImportRows prints each row and its candidates. Sheet-derived text
// goes through printable.
func renderImportRows(out io.Writer, r controlimport.RowList) {
	release := "none"
	if r.SCFRelease != nil {
		release = r.SCFRelease.Version
	}
	_, _ = fmt.Fprintf(out, "import batch %s  SCF release %s  suggestions %s\n",
		printable(r.BatchID), printable(release), printable(r.SuggestionsStatus))
	if len(r.Rows) == 0 {
		_, _ = fmt.Fprintln(out, "no matching rows")
		return
	}
	for _, row := range r.Rows {
		_, _ = fmt.Fprintf(out, "\nrow %d  %s  %s  [%s]\n", row.RowNumber, printable(row.ExternalID), printable(row.Title), printable(row.Status))
		if len(row.TestedUnder) > 0 {
			codes := make([]string, len(row.TestedUnder))
			for i, t := range row.TestedUnder {
				codes[i] = t.Code
			}
			_, _ = fmt.Fprintf(out, "  tested under: %s\n", printable(strings.Join(codes, ", ")))
		}
		if row.NoCandidateReaches {
			_, _ = fmt.Fprintln(out, "  flag: no candidate reaches a tested criterion")
		}
		if len(row.Candidates) == 0 {
			_, _ = fmt.Fprintln(out, "  no candidates")
			continue
		}
		for _, c := range row.Candidates {
			line := fmt.Sprintf("  %d. %s  %s  (%s", c.Rank, printable(c.SCFID), printable(c.Title), printable(c.Source))
			if fit := c.Fit(); fit != "" {
				line += "; " + printable(fit)
			}
			_, _ = fmt.Fprintln(out, line+")")
		}
	}
}
