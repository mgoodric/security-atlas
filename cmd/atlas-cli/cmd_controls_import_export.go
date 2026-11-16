package main

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/internal/controlimport"
)

// newControlsImportExportCmd registers `controls import export <batch>
// --out <dir>` (#1784): download the batch's approved controls and write
// them under dir as one <bundle_id>/control.yaml bundle each, ready to
// commit. `controls validate <dir>` checks the result; re-uploading a
// bundle unchanged is a no-op.
func newControlsImportExportCmd() *cobra.Command {
	var outDir string
	cmd := &cobra.Command{
		Use:   "export <batch>",
		Short: "write an import batch's approved controls as control.yaml bundles",
		Long: "Download the approved controls of an import batch and write each as a control " +
			"bundle, <dir>/<bundle_id>/control.yaml, so they can be kept in version control. " +
			"Each manifest is the control's active version as stored, declaring its SCF anchor, " +
			"SCF release and external references; `controls upload` of an unchanged bundle is a " +
			"no-op. An existing control.yaml of the same bundle under <dir> is replaced; nothing " +
			"is written outside <dir>.",
		Args: cobra.ExactArgs(1),
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if strings.TrimSpace(outDir) == "" {
				return fmt.Errorf("--out is required")
			}
			return resolveCommon()
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return exportImport(cmd.OutOrStdout(), args[0], outDir)
		},
	}
	cmd.Flags().StringVar(&outDir, "out", "", "directory to write the bundles under (created if absent)")
	return cmd
}

func exportImport(out io.Writer, batch, outDir string) error {
	raw, err := getControlImport("/v1/controls/imports/" + url.PathEscape(batch) + "/export")
	if err != nil {
		return err
	}
	ids, err := controlimport.ExtractBundleArchive(bytes.NewReader(raw), outDir)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "wrote %d bundles under %s\n", len(ids), outDir)
	for _, id := range ids {
		_, _ = fmt.Fprintf(out, "  %s\n", filepath.Join(outDir, id, controlimport.ExportManifestName))
	}
	return nil
}
