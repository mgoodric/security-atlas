package main

// #1811: `atlas-cli taxonomy themes` manages the organisation's risk themes
// through /v1/themes. A theme is named by its name or its id; the ten
// built-ins are listed but cannot be changed.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func newTaxonomyThemesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "themes",
		Short: "list, add, rename, or delete risk themes",
		Long: "Manage the organisation's risk themes beside the ten built-ins. A theme is named by its " +
			"name or its id. Renaming rewrites the name on every risk and aggregation rule that uses it; " +
			"a theme in use cannot be deleted. Changes need an admin or grc_engineer token and are audit-logged.",
	}
	cmd.AddCommand(
		newTaxonomyThemesListCmd(),
		newTaxonomyThemesAddCmd(),
		newTaxonomyThemesRenameCmd(),
		newTaxonomyThemesDeleteCmd(),
	)
	return cmd
}

// taxonomyTheme is a theme as /v1/themes returns it.
type taxonomyTheme struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
}

func newTaxonomyThemesListCmd() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "list the built-in and organisation risk themes",
		Args:    cobra.NoArgs,
		PreRunE: func(_ *cobra.Command, _ []string) error { return resolveCommon() },
		RunE: func(cmd *cobra.Command, _ []string) error {
			raw, err := taxonomyRequest(http.MethodGet, "/v1/themes", nil)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if jsonOutput {
				return printIndented(out, raw)
			}
			var resp struct {
				Themes []taxonomyTheme `json:"themes"`
			}
			if err := json.Unmarshal(raw, &resp); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}
			tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "NAME\tSOURCE\tDESCRIPTION")
			for _, th := range resp.Themes {
				_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", printable(th.Name), themeSource(th.Source), printable(th.Description))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print the API response as JSON")
	return cmd
}

// themeSource names the API's "default" source as the built-in it is.
func themeSource(s string) string {
	if s == "default" {
		return "built-in"
	}
	return "organisation"
}

func newTaxonomyThemesAddCmd() *cobra.Command {
	var description string
	cmd := &cobra.Command{
		Use:     "add <name>",
		Short:   "add an organisation theme",
		Args:    cobra.ExactArgs(1),
		PreRunE: func(_ *cobra.Command, _ []string) error { return resolveCommon() },
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := taxonomyRequest(http.MethodPost, "/v1/themes", map[string]string{
				"name": args[0], "description": description,
			})
			if err != nil {
				return err
			}
			return printThemeResult(cmd.OutOrStdout(), "added", raw)
		},
	}
	cmd.Flags().StringVar(&description, "description", "", "description")
	return cmd
}

func newTaxonomyThemesRenameCmd() *cobra.Command {
	var name, description string
	cmd := &cobra.Command{
		Use:     "rename <name|id> [--name <new name>] [--description <text>]",
		Short:   "rename a theme on every risk and aggregation rule that uses it, or change its description",
		Args:    cobra.ExactArgs(1),
		PreRunE: func(_ *cobra.Command, _ []string) error { return resolveCommon() },
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]string{}
			if cmd.Flags().Changed("name") {
				body["name"] = name
			}
			if cmd.Flags().Changed("description") {
				body["description"] = description
			}
			if len(body) == 0 {
				return fmt.Errorf("--name or --description is required")
			}
			raw, err := taxonomyRequest(http.MethodPatch, themePath(args[0]), body)
			if err != nil {
				return err
			}
			if err := printThemeResult(cmd.OutOrStdout(), "renamed", raw); err != nil {
				return err
			}
			var counts struct {
				Risks int64 `json:"risks_updated"`
				Rules int64 `json:"rules_updated"`
			}
			if err := json.Unmarshal(raw, &counts); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "updated %d risk(s) and %d aggregation rule(s)\n", counts.Risks, counts.Rules)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new name")
	cmd.Flags().StringVar(&description, "description", "", "new description")
	return cmd
}

func newTaxonomyThemesDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "delete <name|id>",
		Short:   "delete a theme no risk or aggregation rule uses",
		Args:    cobra.ExactArgs(1),
		PreRunE: func(_ *cobra.Command, _ []string) error { return resolveCommon() },
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := taxonomyRequest(http.MethodDelete, themePath(args[0]), nil); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "deleted %s\n", printable(args[0]))
			return nil
		},
	}
}

func themePath(ref string) string {
	return "/v1/themes/" + url.PathEscape(ref)
}

func printThemeResult(out io.Writer, verb string, raw []byte) error {
	var resp struct {
		Theme taxonomyTheme `json:"theme"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	_, _ = fmt.Fprintf(out, "%s %s\n", verb, printable(resp.Theme.Name))
	return nil
}
