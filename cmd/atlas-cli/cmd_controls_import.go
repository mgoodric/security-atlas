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
	"unicode"

	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/internal/controlimport"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"
)

// newControlsImportCmd registers `controls import <file> --framework <slug>`
// (#1779): upload a CSV/XLSX control sheet to POST /v1/controls/imports and
// print the staged import batch's summary, every rejected row with its
// reason. Tenancy comes from the credential.
func newControlsImportCmd() *cobra.Command {
	var (
		framework string
		columnMap []string
		jsonOut   bool
	)
	cmd := &cobra.Command{
		Use:   "import <file>",
		Short: "upload a CSV/XLSX control sheet and stage it as an import batch",
		Long: "Upload your existing controls (CSV or XLSX) for import. Every row is either " +
			"staged for review or reported as rejected with a reason (missing field, unknown " +
			"tested-under code, duplicate control ID). --framework names the framework the " +
			"tested-under codes belong to; --column-map field=Header overrides header detection " +
			"(fields: " + strings.Join(controlimport.FieldNames(), ", ") + ").",
		Args: cobra.ExactArgs(1),
		PreRunE: func(_ *cobra.Command, args []string) error {
			if strings.TrimSpace(framework) == "" {
				return fmt.Errorf("--framework is required")
			}
			if controlimport.FormatOf(args[0]) == "" {
				return fmt.Errorf("control file must be .csv or .xlsx: %s", args[0])
			}
			return resolveCommon()
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			batch, raw, err := uploadControlImport(args[0], framework, columnMap)
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
			renderImportSummary(out, batch)
			return nil
		},
	}
	cmd.Flags().StringVar(&framework, "framework", "", "slug of the framework the tested-under codes belong to (e.g. soc2)")
	cmd.Flags().StringArrayVar(&columnMap, "column-map", nil, "field=Header override of header detection; repeatable")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print the API response as JSON")
	return cmd
}

// uploadControlImport posts the sheet and returns the decoded batch plus the
// raw response body.
func uploadControlImport(path, framework string, columnMap []string) (controlimport.Batch, []byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return controlimport.Batch{}, nil, fmt.Errorf("read %s: %w", path, err)
	}
	body := &bytes.Buffer{}
	mp := multipart.NewWriter(body)
	part, err := mp.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		return controlimport.Batch{}, nil, fmt.Errorf("multipart create: %w", err)
	}
	if _, err := part.Write(raw); err != nil {
		return controlimport.Batch{}, nil, fmt.Errorf("multipart write: %w", err)
	}
	if err := mp.WriteField("framework", framework); err != nil {
		return controlimport.Batch{}, nil, fmt.Errorf("multipart write: %w", err)
	}
	for _, m := range columnMap {
		if err := mp.WriteField("column_map", m); err != nil {
			return controlimport.Batch{}, nil, fmt.Errorf("multipart write: %w", err)
		}
	}
	if err := mp.Close(); err != nil {
		return controlimport.Batch{}, nil, fmt.Errorf("multipart close: %w", err)
	}

	url := strings.TrimRight(common.endpoint, "/") + "/v1/controls/imports"
	req, err := http.NewRequest(http.MethodPost, url, body)
	if err != nil {
		return controlimport.Batch{}, nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", mp.FormDataContentType())
	req.Header.Set(sdk.MetadataAuthorization, sdk.BearerPrefix+common.token)

	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return controlimport.Batch{}, nil, fmt.Errorf("upload: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	rspBody, err := io.ReadAll(io.LimitReader(resp.Body, 32*1024*1024))
	if err != nil {
		return controlimport.Batch{}, nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		var e struct {
			Error string `json:"error"`
		}
		msg := strings.TrimSpace(string(rspBody))
		if json.Unmarshal(rspBody, &e) == nil && e.Error != "" {
			msg = e.Error
		}
		return controlimport.Batch{}, nil, fmt.Errorf("import failed: HTTP %d: %s", resp.StatusCode, msg)
	}
	var batch controlimport.Batch
	if err := json.Unmarshal(rspBody, &batch); err != nil {
		return controlimport.Batch{}, nil, fmt.Errorf("decode response: %w", err)
	}
	return batch, rspBody, nil
}

// renderImportSummary prints the batch counts and every rejected row. Text
// that came from the uploaded sheet is printed through printable, so a cell
// can't carry terminal escape sequences or fake extra lines.
func renderImportSummary(out io.Writer, b controlimport.Batch) {
	_, _ = fmt.Fprintf(out, "import batch %s (%s, framework %s %s)\n",
		printable(b.ID), printable(b.SourceFilename), printable(b.Framework.Slug), printable(b.Framework.Version))
	_, _ = fmt.Fprintf(out, "  rows:     %d\n", b.TotalRows)
	_, _ = fmt.Fprintf(out, "  staged:   %d\n", b.StagedCount)
	_, _ = fmt.Fprintf(out, "  rejected: %d\n", b.RejectedCount)
	if len(b.UnmappedColumns) > 0 {
		_, _ = fmt.Fprintf(out, "  ignored columns: %s\n", printable(strings.Join(b.UnmappedColumns, ", ")))
	}
	if len(b.Rejected) == 0 {
		return
	}
	_, _ = fmt.Fprintln(out, "rejected rows:")
	for _, r := range b.Rejected {
		id := r.ExternalID
		if id == "" {
			id = "-"
		}
		_, _ = fmt.Fprintf(out, "  row %d  %s  %s: %s\n", r.RowNumber, printable(id), printable(r.Reason), printable(r.Detail))
	}
}

// printable replaces control characters (ESC, CR, LF, tabs, C1 controls)
// with a space.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}
