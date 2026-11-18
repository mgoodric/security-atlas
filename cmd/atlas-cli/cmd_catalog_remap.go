package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/internal/control"
)

// newCatalogRemapCmd registers `catalog remap` (#1776): review the SCF remap
// proposals an SCF import queued (#1775) and decide them through the API.
// Every decision needs a credential that belongs to a person.
func newCatalogRemapCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remap",
		Short: "review and decide SCF remap proposals for controls left on an older SCF release",
	}
	cmd.AddCommand(newCatalogRemapListCmd())
	cmd.AddCommand(newCatalogRemapApproveCmd())
	cmd.AddCommand(newCatalogRemapApproveUnchangedCmd())
	cmd.AddCommand(newCatalogRemapCloseCmd("reject",
		"reject a proposal; the control stays on its legacy anchor and the proposal can be revisited"))
	cmd.AddCommand(newCatalogRemapCloseCmd("keep-legacy",
		"keep the control on its legacy anchor for good; the proposal is closed"))
	return cmd
}

// remapListEntry is one proposal as GET /v1/controls/remap-proposals
// returns it.
type remapListEntry struct {
	ID              string   `json:"id"`
	BundleID        string   `json:"bundle_id"`
	Title           string   `json:"title"`
	LegacySCFID     string   `json:"legacy_scf_id"`
	LegacyRelease   string   `json:"legacy_release"`
	TargetRelease   string   `json:"target_release"`
	Class           string   `json:"class"`
	Status          string   `json:"status"`
	SuccessorSCFIDs []string `json:"successor_scf_ids"`
}

func newCatalogRemapListCmd() *cobra.Command {
	var (
		class, classNot, status, cursor string
		limit                           int
		jsonOutput                      bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "list the tenant's SCF remap proposals",
		Long: "List the remap proposals of the credential's tenant, optionally filtered by " +
			"--class or --class-not (unchanged, renamed, split, reused_id, no_successor) and --status (" +
			strings.Join(control.RemapStatuses, ", ") + "). The list is paged: --limit sizes a " +
			"page, and the last line names the --cursor that reads the next one. " +
			"--status rebound --class-not unchanged lists the controls the upgrade moved that " +
			"may still want a look.",
		Args:    cobra.NoArgs,
		PreRunE: func(_ *cobra.Command, _ []string) error { return resolveCommon() },
		RunE: func(cmd *cobra.Command, _ []string) error {
			q := url.Values{}
			if class != "" {
				q.Set("class", class)
			}
			if classNot != "" {
				q.Set("class_not", classNot)
			}
			if status != "" {
				q.Set("status", status)
			}
			if limit > 0 {
				q.Set("limit", strconv.Itoa(limit))
			}
			if cursor != "" {
				q.Set("cursor", cursor)
			}
			path := "/v1/controls/remap-proposals"
			if len(q) > 0 {
				path += "?" + q.Encode()
			}
			raw, err := getControlImport(path)
			if err != nil {
				return err
			}
			if jsonOutput {
				return printIndented(cmd.OutOrStdout(), raw)
			}
			var list struct {
				Proposals  []remapListEntry `json:"proposals"`
				Total      int              `json:"total"`
				NextCursor string           `json:"next_cursor"`
			}
			if err := json.Unmarshal(raw, &list); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}
			out := cmd.OutOrStdout()
			if len(list.Proposals) == 0 {
				_, _ = fmt.Fprintln(out, "no remap proposals")
				return nil
			}
			for _, p := range list.Proposals {
				successors := "none"
				if len(p.SuccessorSCFIDs) > 0 {
					successors = strings.Join(p.SuccessorSCFIDs, ",")
				}
				_, _ = fmt.Fprintf(out, "%s  %s  %s  %s@%s -> %s (%s)  %s\n",
					printable(p.ID), printable(p.Status), printable(p.Class),
					printable(p.LegacySCFID), printable(p.LegacyRelease),
					printable(successors), printable(p.TargetRelease), printable(p.BundleID))
			}
			if list.NextCursor != "" {
				_, _ = fmt.Fprintf(out, "%d of %d shown; next page: --cursor %s\n",
					len(list.Proposals), list.Total, printable(list.NextCursor))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&class, "class", "", "only proposals of this class")
	cmd.Flags().StringVar(&classNot, "class-not", "", "leave out proposals of this class")
	cmd.Flags().StringVar(&status, "status", "", "only proposals with this status")
	cmd.Flags().IntVar(&limit, "limit", 0, "proposals per page (the platform defaults to 100, at most 500)")
	cmd.Flags().StringVar(&cursor, "cursor", "", "read the page after this next_cursor")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print the API response as JSON")
	return cmd
}

func newCatalogRemapApproveCmd() *cobra.Command {
	var (
		scfID, reason string
		jsonOutput    bool
	)
	cmd := &cobra.Command{
		Use:   "approve <proposal-id>",
		Short: "re-anchor a control on its successor as a new control revision",
		Long: "Approve one remap proposal. The control is re-anchored on the successor in the " +
			"proposal's target SCF release as a new, superseding control revision. --scf-id names " +
			"the successor; it is required for split and no_successor proposals, and otherwise " +
			"defaults to the single proposed successor. The credential must belong to a person.",
		Args:    cobra.ExactArgs(1),
		PreRunE: func(_ *cobra.Command, _ []string) error { return resolveCommon() },
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]any{}
			if s := strings.TrimSpace(scfID); s != "" {
				body["scf_id"] = s
			}
			if reason != "" {
				body["reason"] = reason
			}
			return decideRemap(cmd.OutOrStdout(), args[0], "approve", body, jsonOutput)
		},
	}
	cmd.Flags().StringVar(&scfID, "scf-id", "", "the successor SCF code in the target release")
	cmd.Flags().StringVar(&reason, "reason", "", "why, kept in the anchor history")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print the API response as JSON")
	return cmd
}

func newCatalogRemapCloseCmd(verb, short string) *cobra.Command {
	var (
		reason     string
		jsonOutput bool
	)
	cmd := &cobra.Command{
		Use:     verb + " <proposal-id>",
		Short:   short,
		Long:    short + ". The credential must belong to a person.",
		Args:    cobra.ExactArgs(1),
		PreRunE: func(_ *cobra.Command, _ []string) error { return resolveCommon() },
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]any{}
			if reason != "" {
				body["reason"] = reason
			}
			return decideRemap(cmd.OutOrStdout(), args[0], verb, body, jsonOutput)
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "why, kept in the anchor history")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print the API response as JSON")
	return cmd
}

func newCatalogRemapApproveUnchangedCmd() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "approve-unchanged",
		Short: "approve every open unchanged proposal with its single successor",
		Long: "Approve every open proposal of class unchanged (a renumbered SCF code with the same " +
			"title), re-anchoring each control as a new revision. Other classes, and proposals a " +
			"person already rejected or kept legacy, are left alone. The credential must belong to a person.",
		Args:    cobra.NoArgs,
		PreRunE: func(_ *cobra.Command, _ []string) error { return resolveCommon() },
		RunE: func(cmd *cobra.Command, _ []string) error {
			raw, err := callControlImport("POST", "/v1/controls/remap-proposals/approve-unchanged", []byte("{}"))
			if err != nil {
				return err
			}
			if jsonOutput {
				return printIndented(cmd.OutOrStdout(), raw)
			}
			var res control.RemapBulkResult
			if err := json.Unmarshal(raw, &res); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "approved %d unchanged proposals, skipped %d\n", res.ApprovedCount, len(res.Skipped))
			for _, d := range res.Approved {
				renderRemapDecision(out, d)
			}
			for _, s := range res.Skipped {
				_, _ = fmt.Fprintf(out, "skipped %s  %s: %s\n", printable(s.ProposalID), printable(s.BundleID), printable(s.Reason))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print the API response as JSON")
	return cmd
}

func decideRemap(out io.Writer, id, verb string, body map[string]any, jsonOutput bool) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	raw, err := callControlImport("POST", "/v1/controls/remap-proposals/"+url.PathEscape(id)+"/"+verb, payload)
	if err != nil {
		return err
	}
	if jsonOutput {
		return printIndented(out, raw)
	}
	var d control.RemapDecision
	if err := json.Unmarshal(raw, &d); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	renderRemapDecision(out, d)
	return nil
}

func renderRemapDecision(out io.Writer, d control.RemapDecision) {
	_, _ = fmt.Fprintf(out, "proposal %s  %s  %s\n", printable(d.ProposalID), printable(d.BundleID), printable(d.Decision))
	_, _ = fmt.Fprintf(out, "  legacy:  %s in SCF release %s (control %s)\n",
		printable(d.LegacySCFID), printable(d.LegacyRelease), printable(d.LegacyControlID))
	if d.NewControlID != "" {
		_, _ = fmt.Fprintf(out, "  new:     %s in SCF release %s (control %s, version %d)\n",
			printable(d.NewSCFID), printable(d.NewRelease), printable(d.NewControlID), d.NewVersion)
	}
	if d.Reason != "" {
		_, _ = fmt.Fprintf(out, "  reason:  %s\n", printable(d.Reason))
	}
	_, _ = fmt.Fprintf(out, "  decided by %s at %s\n", printable(d.DecidedBy), printable(d.DecidedAt))
}
