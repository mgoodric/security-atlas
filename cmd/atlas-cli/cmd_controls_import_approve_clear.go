package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"

	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/internal/controlimport"
)

// newControlsImportApproveClearCmd registers `controls import approve-clear
// <batch>` (#1783): approve every pending row whose top candidate is
// crosswalk-derived and reaches all of the row's tested criteria, with that
// candidate. The credential must belong to a person.
func newControlsImportApproveClearCmd() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "approve-clear <batch>",
		Short: "approve every staged control whose top candidate reaches all its tested criteria",
		Long: "Approve, in one action, every pending row of an import batch whose top candidate " +
			"comes from the SCF crosswalk and reaches every one of the row's tested-under criteria, " +
			"with that candidate. Each row is approved as `controls import approve <batch> <row> " +
			"--candidate 1` would: a control is created and the decision is audit-logged. Every " +
			"other row stays pending. The credential must belong to a person.",
		Args: cobra.ExactArgs(1),
		PreRunE: func(_ *cobra.Command, _ []string) error {
			return resolveCommon()
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return approveClearImport(cmd.OutOrStdout(), args[0], jsonOutput)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print the API response as JSON")
	return cmd
}

func approveClearImport(out io.Writer, batch string, jsonOutput bool) error {
	raw, err := callControlImport("POST", "/v1/controls/imports/"+url.PathEscape(batch)+"/approve-clear", []byte("{}"))
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
	var res controlimport.ApproveClearResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	_, _ = fmt.Fprintf(out, "approved %d of %d clear rows (%d duplicate, %d skipped)\n",
		res.ApprovedCount, res.Qualifying, len(res.Duplicates), len(res.Skipped))
	for _, d := range res.Approved {
		_, _ = fmt.Fprintf(out, "  row %d  %s  approved  %s -> control %s\n",
			d.RowNumber, printable(d.ExternalID), printable(d.SCFID), printable(d.ControlID))
	}
	for _, d := range res.Duplicates {
		_, _ = fmt.Fprintf(out, "  row %d  %s  duplicate of control %s\n",
			d.RowNumber, printable(d.ExternalID), printable(d.DuplicateOf))
	}
	for _, s := range res.Skipped {
		_, _ = fmt.Fprintf(out, "  row %d  %s  skipped: %s\n", s.RowNumber, printable(s.ExternalID), printable(s.Reason))
	}
	return nil
}
