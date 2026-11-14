package main

// #1797: `atlas-cli mappings overlay` manages the organisation's overlay
// edges (its own requirement -> SCF anchor edges, #1735) through the HTTP
// API (/v1/mapping-overlays). Every subcommand takes --endpoint and --token
// like the taxonomy commands; the tenant is the token's, never a flag.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func newMappingsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mappings",
		Short: "manage the organisation's framework mappings",
	}
	cmd.AddCommand(newMappingsOverlayCmd())
	return cmd
}

func newMappingsOverlayCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "overlay",
		Short: "add, list or retire the organisation's overlay edges (requirement -> SCF anchor)",
		Long: "An overlay edge is the organisation's own link from a framework requirement to an SCF anchor, " +
			"such as a criterion its auditor tested a control under. Overlay edges belong to the token's " +
			"tenant only. Adding and retiring need an admin or grc_engineer token and are audit-logged.",
	}
	cmd.AddCommand(newMappingsOverlayAddCmd(), newMappingsOverlayListCmd(), newMappingsOverlayRetireCmd())
	return cmd
}

// overlayEdge is an overlay edge as the API returns it.
type overlayEdge struct {
	ID               string  `json:"id"`
	Framework        string  `json:"framework"`
	FrameworkVersion string  `json:"framework_version"`
	RequirementCode  string  `json:"requirement_code"`
	SCFRelease       string  `json:"scf_release"`
	AnchorCode       string  `json:"anchor_code"`
	RelationshipType string  `json:"relationship_type"`
	Strength         float64 `json:"strength"`
	Rationale        string  `json:"rationale"`
	SourceRef        string  `json:"source_ref"`
	Status           string  `json:"status"`
	RetirementReason string  `json:"retirement_reason"`
}

func newMappingsOverlayAddCmd() *cobra.Command {
	var (
		frameworkVersion, scfRelease, relationship, rationale, source string
		strength                                                      float64
	)
	cmd := &cobra.Command{
		Use:   "add <framework> <requirement-code> <anchor-code> --relationship <r> --strength <s> --rationale <text> --source <ref>",
		Short: "add an overlay edge from a framework requirement to an SCF anchor",
		Example: "  atlas-cli mappings overlay add soc2 CC1.1 HRS-07.1 --framework-version 2017 --scf-release 2025.2 \\\n" +
			"    --relationship intersects_with --strength 0.8 \\\n" +
			"    --rationale 'Auditor tested the confidentiality agreement control under CC1.1' \\\n" +
			"    --source 'FY25 SOC 2 report, section 4, CC1.1'",
		Args:    cobra.ExactArgs(3),
		PreRunE: func(_ *cobra.Command, _ []string) error { return resolveCommon() },
		RunE: func(cmd *cobra.Command, args []string) error {
			if rationale == "" {
				return fmt.Errorf("--rationale is required")
			}
			if source == "" {
				return fmt.Errorf("--source is required")
			}
			raw, err := taxonomyRequest(http.MethodPost, "/v1/mapping-overlays", map[string]any{
				"framework":         args[0],
				"framework_version": frameworkVersion,
				"requirement_code":  args[1],
				"scf_release":       scfRelease,
				"anchor_code":       args[2],
				"relationship_type": relationship,
				"strength":          strength,
				"rationale":         rationale,
				"source_ref":        source,
			})
			if err != nil {
				return err
			}
			return printOverlayResult(cmd.OutOrStdout(), "added", raw)
		},
	}
	cmd.Flags().StringVar(&frameworkVersion, "framework-version", "", "framework version label (default: the current version)")
	cmd.Flags().StringVar(&scfRelease, "scf-release", "", "SCF release the anchor code is in (default: the current release)")
	cmd.Flags().StringVar(&relationship, "relationship", "", "STRM relationship: equal, subset_of, superset_of or intersects_with (required)")
	cmd.Flags().Float64Var(&strength, "strength", 0, "strength, 0 to 1 (required)")
	cmd.Flags().StringVar(&rationale, "rationale", "", "why the requirement maps to the anchor (required)")
	cmd.Flags().StringVar(&source, "source", "", "the evidence for the mapping, e.g. an audit report section (required)")
	_ = cmd.MarkFlagRequired("relationship")
	_ = cmd.MarkFlagRequired("strength")
	return cmd
}

func newMappingsOverlayListCmd() *cobra.Command {
	var framework, requirement, anchor, status string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "list the organisation's overlay edges",
		Args:    cobra.NoArgs,
		PreRunE: func(_ *cobra.Command, _ []string) error { return resolveCommon() },
		RunE: func(cmd *cobra.Command, _ []string) error {
			q := url.Values{}
			for k, v := range map[string]string{
				"framework": framework, "requirement": requirement, "anchor": anchor, "status": status,
			} {
				if v != "" {
					q.Set(k, v)
				}
			}
			path := "/v1/mapping-overlays"
			if len(q) > 0 {
				path += "?" + q.Encode()
			}
			raw, err := taxonomyRequest(http.MethodGet, path, nil)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if jsonOutput {
				return printIndented(out, raw)
			}
			var resp struct {
				OverlayEdges []overlayEdge `json:"overlay_edges"`
			}
			if err := json.Unmarshal(raw, &resp); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}
			tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "ID\tREQUIREMENT\tANCHOR\tRELATIONSHIP\tSTRENGTH\tSTATUS\tSOURCE")
			for _, e := range resp.OverlayEdges {
				_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
					printable(e.ID), overlayRequirement(e), overlayAnchor(e), printable(e.RelationshipType),
					strconv.FormatFloat(e.Strength, 'f', -1, 64), printable(e.Status), printable(e.SourceRef))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&framework, "framework", "", "only this framework slug")
	cmd.Flags().StringVar(&requirement, "requirement", "", "only this requirement code")
	cmd.Flags().StringVar(&anchor, "anchor", "", "only this SCF anchor code")
	cmd.Flags().StringVar(&status, "status", "", "only active or retired edges")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print the API response as JSON")
	return cmd
}

func newMappingsOverlayRetireCmd() *cobra.Command {
	var reason string
	cmd := &cobra.Command{
		Use:     "retire <id> --reason <text>",
		Short:   "retire an overlay edge; it stops counting and keeps its history",
		Args:    cobra.ExactArgs(1),
		PreRunE: func(_ *cobra.Command, _ []string) error { return resolveCommon() },
		RunE: func(cmd *cobra.Command, args []string) error {
			if reason == "" {
				return fmt.Errorf("--reason is required")
			}
			raw, err := taxonomyRequest(http.MethodPost,
				"/v1/mapping-overlays/"+url.PathEscape(args[0])+"/retire", map[string]string{"reason": reason})
			if err != nil {
				return err
			}
			return printOverlayResult(cmd.OutOrStdout(), "retired", raw)
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "why the edge is retired (required)")
	return cmd
}

func overlayRequirement(e overlayEdge) string {
	return printable(e.Framework + ":" + e.FrameworkVersion + ":" + e.RequirementCode)
}

func overlayAnchor(e overlayEdge) string {
	return printable("SCF " + e.SCFRelease + ":" + e.AnchorCode)
}

func printOverlayResult(out io.Writer, verb string, raw []byte) error {
	var resp struct {
		OverlayEdge overlayEdge `json:"overlay_edge"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	e := resp.OverlayEdge
	_, _ = fmt.Fprintf(out, "%s %s: %s -> %s [%s]\n", verb, printable(e.ID), overlayRequirement(e), overlayAnchor(e), printable(e.Status))
	return nil
}
