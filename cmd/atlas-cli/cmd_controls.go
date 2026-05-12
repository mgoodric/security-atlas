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

	"github.com/mgoodric/security-atlas/internal/control"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"
)

// newControlsCmd registers `security-atlas-cli controls {validate,upload}`.
//
// `validate` is local-only: no network, no auth. It parses the bundle and
// runs the structural validator. AC-2.
//
// `upload` ships the bundle to POST /v1/controls:upload-bundle. AC-3.
func newControlsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "controls",
		Short: "control-as-code bundle authoring (validate / upload)",
	}
	cmd.AddCommand(newControlsValidateCmd())
	cmd.AddCommand(newControlsUploadCmd())
	return cmd
}

func newControlsValidateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate <path>",
		Short: "validate a control bundle locally (no network call)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			path := args[0]
			bundle, err := loadBundleFromPath(path)
			if err != nil {
				return err
			}
			fmt.Printf("OK bundle_id=%s title=%q implementation_type=%s manifest_hash=%s\n",
				bundle.Manifest.BundleID,
				bundle.Manifest.Title,
				bundle.Manifest.ImplementationType,
				bundle.ManifestHashHex,
			)
			if len(bundle.Manifest.EvidenceQueries) > 0 {
				fmt.Printf("    evidence_queries=%d\n", len(bundle.Manifest.EvidenceQueries))
			}
			if err := bundle.ValidateApplicabilityExpr(); err != nil {
				return err
			}
			return nil
		},
	}
	return cmd
}

func newControlsUploadCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "upload <path>",
		Short: "upload a control bundle to the platform",
		Args:  cobra.ExactArgs(1),
		PreRunE: func(_ *cobra.Command, _ []string) error {
			return resolveCommon()
		},
		RunE: func(_ *cobra.Command, args []string) error {
			path := args[0]
			// Validate locally first so we never burn a network round trip
			// on a malformed bundle.
			if _, err := loadBundleFromPath(path); err != nil {
				return err
			}
			return uploadBundleHTTP(path)
		},
	}
	return cmd
}

// loadBundleFromPath dispatches on path shape: directory -> ParseDirectory,
// tarball (.tar.gz / .tgz file) -> ParseTarball.
func loadBundleFromPath(path string) (*control.Bundle, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if info.IsDir() {
		return control.ParseDirectory(path)
	}
	low := strings.ToLower(path)
	if strings.HasSuffix(low, ".tar.gz") || strings.HasSuffix(low, ".tgz") {
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", path, err)
		}
		defer func() { _ = f.Close() }()
		return control.ParseTarball(f)
	}
	return nil, fmt.Errorf("bundle path must be a directory or *.tar.gz/*.tgz file: %s", path)
}

// uploadBundleHTTP sends the bundle to the platform. For tarballs we POST
// multipart with the file part; for directories we tar them in-process and
// POST the tarball. JSON inline mode is also available via the API but the
// CLI prefers tarballs so the manifest hash matches what the server stores.
func uploadBundleHTTP(path string) error {
	tarBytes, err := buildTarballForUpload(path)
	if err != nil {
		return err
	}

	endpoint := strings.TrimRight(common.endpoint, "/")
	url := endpoint + "/v1/controls:upload-bundle"
	body := &bytes.Buffer{}
	mp := multipart.NewWriter(body)
	part, err := mp.CreateFormFile("bundle.tar.gz", filepath.Base(path)+".tar.gz")
	if err != nil {
		return fmt.Errorf("multipart create: %w", err)
	}
	if _, err := part.Write(tarBytes); err != nil {
		return fmt.Errorf("multipart write: %w", err)
	}
	if err := mp.Close(); err != nil {
		return fmt.Errorf("multipart close: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, body)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", mp.FormDataContentType())
	req.Header.Set(sdk.MetadataAuthorization, sdk.BearerPrefix+common.token)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("upload: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	rspBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))

	if resp.StatusCode >= 400 {
		return fmt.Errorf("upload failed: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(rspBody)))
	}

	// Pretty-print the JSON response.
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, rspBody, "", "  "); err == nil {
		fmt.Println(pretty.String())
	} else {
		fmt.Println(string(rspBody))
	}
	return nil
}
