package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/internal/controlimport"
)

// newControlsImportApproveCmd registers `controls import approve <batch>
// <row>` (#1782): approve a staged row with one of its candidates
// (--candidate) or any anchor of the batch's SCF release (--scf-id),
// creating a control in the credential's tenant. A row is named by its ID
// or its sheet row number. The credential must belong to a person.
func newControlsImportApproveCmd() *cobra.Command {
	var (
		candidate  int
		scfID      string
		implType   string
		applExpr   string
		jsonOutput bool
	)
	cmd := &cobra.Command{
		Use:   "approve <batch> <row>",
		Short: "approve a staged control, creating a real control in the organisation",
		Long: "Approve one staged control of an import batch. --candidate N picks the row's " +
			"candidate of rank N (see `controls import show`); --scf-id picks any anchor of the " +
			"batch's SCF release. The control is created with the row's title and description, " +
			"the organisation control ID and tested-under criteria as external references, and " +
			"--implementation-type / --applicability-expr when given (otherwise the row's, then " +
			"manual_attested and applicable everywhere). <row> is the row's ID or its sheet row " +
			"number. The credential must belong to a person: a machine credential cannot approve.",
		Args: cobra.ExactArgs(2),
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if (candidate > 0) == (strings.TrimSpace(scfID) != "") {
				return fmt.Errorf("give exactly one of --candidate or --scf-id")
			}
			return resolveCommon()
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]any{}
			if candidate > 0 {
				body["candidate_rank"] = candidate
			} else {
				body["scf_id"] = strings.TrimSpace(scfID)
			}
			if implType != "" {
				body["implementation_type"] = implType
			}
			if applExpr != "" {
				var expr map[string]any
				if err := json.Unmarshal([]byte(applExpr), &expr); err != nil {
					return fmt.Errorf("--applicability-expr must be a JSON object: %w", err)
				}
				body["applicability_expr"] = expr
			}
			return decideImportRow(cmd.OutOrStdout(), args[0], args[1], "approve", body, jsonOutput)
		},
	}
	cmd.Flags().IntVar(&candidate, "candidate", 0, "approve with the row's candidate of this rank")
	cmd.Flags().StringVar(&scfID, "scf-id", "", "approve with this SCF anchor code from the batch's SCF release")
	cmd.Flags().StringVar(&implType, "implementation-type", "", "automated, semi_automated, manual_attested or manual_periodic")
	cmd.Flags().StringVar(&applExpr, "applicability-expr", "", "applicability expression as JSON (default: applies everywhere)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print the API response as JSON")
	return cmd
}

// newControlsImportRejectCmd registers `controls import reject <batch> <row>
// --reason <text>` (#1782).
func newControlsImportRejectCmd() *cobra.Command {
	var (
		reason     string
		jsonOutput bool
	)
	cmd := &cobra.Command{
		Use:   "reject <batch> <row>",
		Short: "reject a staged control with a reason",
		Long: "Reject one staged control of an import batch; the reason is kept with the row " +
			"and in the audit log. <row> is the row's ID or its sheet row number. The credential " +
			"must belong to a person.",
		Args: cobra.ExactArgs(2),
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if strings.TrimSpace(reason) == "" {
				return fmt.Errorf("--reason is required")
			}
			return resolveCommon()
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return decideImportRow(cmd.OutOrStdout(), args[0], args[1], "reject",
				map[string]any{"reason": reason}, jsonOutput)
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "why the control is not imported (required)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print the API response as JSON")
	return cmd
}

func decideImportRow(out io.Writer, batch, row, verb string, body map[string]any, jsonOutput bool) error {
	rowID, err := resolveImportRowID(batch, row)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	path := "/v1/controls/imports/" + url.PathEscape(batch) + "/rows/" + url.PathEscape(rowID) + "/" + verb
	raw, err := callControlImport("POST", path, payload)
	if err != nil {
		return err
	}
	if jsonOutput {
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, raw, "", "  "); err != nil {
			return fmt.Errorf("format response: %w", err)
		}
		_, _ = fmt.Fprintln(out, pretty.String())
		return nil
	}
	var d controlimport.RowDecision
	if err := json.Unmarshal(raw, &d); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	renderRowDecision(out, d)
	return nil
}

// resolveImportRowID returns row as given when it is not a number, and
// otherwise looks up the staged row with that sheet row number.
func resolveImportRowID(batch, row string) (string, error) {
	n, err := strconv.Atoi(row)
	if err != nil {
		return row, nil
	}
	raw, err := getControlImport("/v1/controls/imports/" + url.PathEscape(batch) + "/rows")
	if err != nil {
		return "", err
	}
	var list controlimport.RowList
	if err := json.Unmarshal(raw, &list); err != nil {
		return "", fmt.Errorf("decode rows: %w", err)
	}
	for _, r := range list.Rows {
		if r.RowNumber == n {
			return r.ID, nil
		}
	}
	return "", fmt.Errorf("import batch %s has no staged row %d", batch, n)
}

func renderRowDecision(out io.Writer, d controlimport.RowDecision) {
	_, _ = fmt.Fprintf(out, "row %d  %s  %s\n", d.RowNumber, printable(d.ExternalID), printable(d.Status))
	switch d.Status {
	case controlimport.RowStatusApproved:
		_, _ = fmt.Fprintf(out, "  control: %s (bundle %s)\n", printable(d.ControlID), printable(d.BundleID))
		_, _ = fmt.Fprintf(out, "  anchor:  %s in SCF release %s\n", printable(d.SCFID), printable(d.SCFRelease))
		for _, r := range d.ExternalRefs {
			_, _ = fmt.Fprintf(out, "  ref:     %s %s %s\n", printable(r.System), printable(r.Kind), printable(r.ID))
		}
	case controlimport.RowStatusRejected:
		_, _ = fmt.Fprintf(out, "  reason:  %s\n", printable(d.RejectionReason))
	}
	_, _ = fmt.Fprintf(out, "  decided by %s\n", printable(d.DecidedBy))
}
