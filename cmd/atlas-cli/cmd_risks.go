package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/cmd/atlas-cli/cmdhttp"
	"github.com/mgoodric/security-atlas/internal/risk/riskimport"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"
)

// newRisksCmd registers `security-atlas-cli risks import` (#2839).
func newRisksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "risks",
		Short: "risk register import",
	}
	cmd.AddCommand(newRisksImportCmd())
	return cmd
}

// newRisksImportCmd registers `risks import <file>`: upload a CSV/XLSX risk
// register to POST /v1/risks/imports and print the staged batch's summary,
// every rejected row with its reason. Tenancy comes from the credential.
// Nothing becomes a risk until a person approves the batch.
func newRisksImportCmd() *cobra.Command {
	var (
		columnMap []string
		jsonOut   bool
	)
	cmd := &cobra.Command{
		Use:   "import <file>",
		Short: "upload a CSV/XLSX risk register and stage it as an import batch",
		Long: "Upload your risk register (CSV or XLSX) for import. Every row is either staged for " +
			"review or reported as rejected with a reason (missing title or category, a repeated " +
			"risk ID, an unknown category, a bad review date). Columns no risk field takes are kept " +
			"on the staged rows. --column-map field=Header overrides header detection (fields: " +
			strings.Join(riskimport.FieldNames(), ", ") + ").",
		Args: cobra.ExactArgs(1),
		PreRunE: func(_ *cobra.Command, args []string) error {
			if riskimport.FormatOf(args[0]) == "" {
				return fmt.Errorf("risk register must be .csv or .xlsx: %s", args[0])
			}
			return resolveCommon()
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			batch, raw, err := uploadRiskImport(args[0], columnMap)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if jsonOut {
				var pretty bytes.Buffer
				if err := json.Indent(&pretty, raw, "", "  "); err != nil {
					return fmt.Errorf("format response: %w", err)
				}
				_, _ = fmt.Fprintln(out, pretty.String())
				return nil
			}
			renderRiskImportSummary(out, batch)
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&columnMap, "column-map", nil, "field=Header override of header detection; repeatable")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print the API response as JSON")
	return cmd
}

// uploadRiskImport posts the register and returns the decoded batch plus
// the raw response body.
func uploadRiskImport(path string, columnMap []string) (riskimport.Batch, []byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return riskimport.Batch{}, nil, fmt.Errorf("read %s: %w", path, err)
	}
	body := &bytes.Buffer{}
	mp := multipart.NewWriter(body)
	part, err := mp.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		return riskimport.Batch{}, nil, fmt.Errorf("multipart create: %w", err)
	}
	if _, err := part.Write(raw); err != nil {
		return riskimport.Batch{}, nil, fmt.Errorf("multipart write: %w", err)
	}
	for _, m := range columnMap {
		if err := mp.WriteField("column_map", m); err != nil {
			return riskimport.Batch{}, nil, fmt.Errorf("multipart write: %w", err)
		}
	}
	if err := mp.Close(); err != nil {
		return riskimport.Batch{}, nil, fmt.Errorf("multipart close: %w", err)
	}

	url := strings.TrimRight(common.endpoint, "/") + "/v1/risks/imports"
	req, err := http.NewRequest(http.MethodPost, url, body)
	if err != nil {
		return riskimport.Batch{}, nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", mp.FormDataContentType())
	req.Header.Set(sdk.MetadataAuthorization, sdk.BearerPrefix+common.token)

	// As the controls import: Guard refuses plain http:// to a non-loopback
	// endpoint (#2257), without cmdhttp.Client's response-header timeout.
	client := &http.Client{Timeout: 2 * time.Minute, Transport: cmdhttp.Guard(nil)}
	resp, err := client.Do(req)
	if err != nil {
		return riskimport.Batch{}, nil, fmt.Errorf("upload: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	rspBody, err := io.ReadAll(io.LimitReader(resp.Body, 32*1024*1024))
	if err != nil {
		return riskimport.Batch{}, nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		var e struct {
			Error string `json:"error"`
		}
		msg := strings.TrimSpace(string(rspBody))
		if json.Unmarshal(rspBody, &e) == nil && e.Error != "" {
			msg = e.Error
		}
		return riskimport.Batch{}, nil, fmt.Errorf("import failed: HTTP %d: %s", resp.StatusCode, printable(msg))
	}
	var batch riskimport.Batch
	if err := json.Unmarshal(rspBody, &batch); err != nil {
		return riskimport.Batch{}, nil, fmt.Errorf("decode response: %w", err)
	}
	return batch, rspBody, nil
}

// renderRiskImportSummary prints the batch counts, the unmapped columns and
// every rejected row. Text from the uploaded register goes through
// printable, so a cell can't carry terminal escape sequences.
func renderRiskImportSummary(out io.Writer, b riskimport.Batch) {
	_, _ = fmt.Fprintf(out, "risk import batch %s (%s)\n", printable(b.ID), printable(b.SourceFilename))
	_, _ = fmt.Fprintf(out, "  rows:     %d\n", b.TotalRows)
	_, _ = fmt.Fprintf(out, "  staged:   %d\n", b.StagedCount)
	_, _ = fmt.Fprintf(out, "  rejected: %d\n", b.RejectedCount)
	if len(b.UnmappedColumns) > 0 {
		_, _ = fmt.Fprintf(out, "  unmapped columns (kept on the staged rows): %s\n", printable(strings.Join(b.UnmappedColumns, ", ")))
	}
	if len(b.Rejected) > 0 {
		_, _ = fmt.Fprintln(out, "rejected rows:")
		for _, r := range b.Rejected {
			id := r.ExternalRef
			if id == "" {
				id = "-"
			}
			_, _ = fmt.Fprintf(out, "  row %d  %s  %s: %s\n", r.RowNumber, printable(id), printable(r.Reason), printable(r.Detail))
		}
	}
	if b.StagedCount > 0 {
		_, _ = fmt.Fprintf(out, "nothing is a risk yet: a person with approver rights approves with POST /v1/risks/imports/%s/approve\n", printable(b.ID))
	}
}
