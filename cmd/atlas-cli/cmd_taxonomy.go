package main

// #1810: `atlas-cli taxonomy categories` manages the organisation's risk
// categories through the HTTP API (/v1/risks/categories). Every subcommand
// takes --endpoint (the platform HTTP base URL) and --token like the
// `controls import` commands. A category is named by its slug or its id.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/cmd/atlas-cli/cmdhttp"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"
)

func newTaxonomyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "taxonomy",
		Short: "manage the organisation's risk taxonomy",
	}
	cmd.AddCommand(newTaxonomyCategoriesCmd())
	return cmd
}

func newTaxonomyCategoriesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "categories",
		Short: "list, add, rename, archive, set the default, or delete risk categories",
		Long: "Manage the organisation's risk categories. A category is named by its slug or its id; " +
			"the slug is fixed when the category is added and never changes. Changes need an admin or " +
			"grc_engineer token and are audit-logged.",
	}
	cmd.AddCommand(
		newTaxonomyCategoriesListCmd(),
		newTaxonomyCategoriesAddCmd(),
		newTaxonomyCategoriesRenameCmd(),
		newTaxonomyCategoryActionCmd("archive", "hide a category from the default list and from new risks"),
		newTaxonomyCategoryActionCmd("unarchive", "restore an archived category"),
		newTaxonomyCategoryActionCmd("set-default", "make a category the organisation default"),
		newTaxonomyCategoriesDeleteCmd(),
	)
	return cmd
}

// taxonomyCategory is a category as GET /v1/risks/categories returns it.
type taxonomyCategory struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Archived    bool   `json:"archived"`
	IsDefault   bool   `json:"is_default"`
	RiskCount   int64  `json:"risk_count"`
	RuleCount   int64  `json:"rule_count"`
}

func newTaxonomyCategoriesListCmd() *cobra.Command {
	var includeArchived, jsonOutput bool
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "list the organisation's risk categories with usage counts",
		Args:    cobra.NoArgs,
		PreRunE: func(_ *cobra.Command, _ []string) error { return resolveCommon() },
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := "/v1/risks/categories"
			if includeArchived {
				path += "?include_archived=true"
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
				Categories []taxonomyCategory `json:"categories"`
			}
			if err := json.Unmarshal(raw, &resp); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}
			tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "SLUG\tNAME\tRISKS\tRULES\tSTATUS")
			for _, c := range resp.Categories {
				_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\n",
					printable(c.Slug), printable(c.Name), c.RiskCount, c.RuleCount, categoryStatus(c))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&includeArchived, "include-archived", false, "include archived categories")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print the API response as JSON")
	return cmd
}

func categoryStatus(c taxonomyCategory) string {
	var parts []string
	if c.IsDefault {
		parts = append(parts, "default")
	}
	if c.Archived {
		parts = append(parts, "archived")
	}
	if len(parts) == 0 {
		return "active"
	}
	return strings.Join(parts, ",")
}

func newTaxonomyCategoriesAddCmd() *cobra.Command {
	var name, description string
	cmd := &cobra.Command{
		Use:     "add <slug> --name <name>",
		Short:   "add a risk category",
		Args:    cobra.ExactArgs(1),
		PreRunE: func(_ *cobra.Command, _ []string) error { return resolveCommon() },
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			raw, err := taxonomyRequest(http.MethodPost, "/v1/risks/categories", map[string]string{
				"slug": args[0], "name": name, "description": description,
			})
			if err != nil {
				return err
			}
			return printCategoryResult(cmd.OutOrStdout(), "added", raw)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "display name (required)")
	cmd.Flags().StringVar(&description, "description", "", "description")
	return cmd
}

func newTaxonomyCategoriesRenameCmd() *cobra.Command {
	var name, description string
	cmd := &cobra.Command{
		Use:     "rename <slug|id> [--name <name>] [--description <text>]",
		Short:   "change a category's name or description (the slug never changes)",
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
			raw, err := taxonomyRequest(http.MethodPatch, categoryPath(args[0]), body)
			if err != nil {
				return err
			}
			return printCategoryResult(cmd.OutOrStdout(), "renamed", raw)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new display name")
	cmd.Flags().StringVar(&description, "description", "", "new description")
	return cmd
}

// newTaxonomyCategoryActionCmd builds archive, unarchive and set-default,
// each a POST to /v1/risks/categories/{ref}/<action>.
func newTaxonomyCategoryActionCmd(action, short string) *cobra.Command {
	past := map[string]string{"archive": "archived", "unarchive": "unarchived", "set-default": "set as default"}[action]
	return &cobra.Command{
		Use:     action + " <slug|id>",
		Short:   short,
		Args:    cobra.ExactArgs(1),
		PreRunE: func(_ *cobra.Command, _ []string) error { return resolveCommon() },
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := taxonomyRequest(http.MethodPost, categoryPath(args[0])+"/"+action, nil)
			if err != nil {
				return err
			}
			return printCategoryResult(cmd.OutOrStdout(), past, raw)
		},
	}
}

func newTaxonomyCategoriesDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "delete <slug|id>",
		Short:   "delete a category no risk or aggregation rule uses",
		Args:    cobra.ExactArgs(1),
		PreRunE: func(_ *cobra.Command, _ []string) error { return resolveCommon() },
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := taxonomyRequest(http.MethodDelete, categoryPath(args[0]), nil); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "deleted %s\n", printable(args[0]))
			return nil
		},
	}
}

func categoryPath(ref string) string {
	return "/v1/risks/categories/" + url.PathEscape(ref)
}

func printCategoryResult(out io.Writer, verb string, raw []byte) error {
	var resp struct {
		Category taxonomyCategory `json:"category"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	c := resp.Category
	_, _ = fmt.Fprintf(out, "%s %s (%s) [%s]\n", verb, printable(c.Slug), printable(c.Name), categoryStatus(c))
	return nil
}

func printIndented(out io.Writer, raw []byte) error {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		return fmt.Errorf("format response: %w", err)
	}
	_, _ = fmt.Fprintln(out, pretty.String())
	return nil
}

// taxonomyRequest calls the platform HTTP API with the --token bearer. A
// 4xx/5xx is an error carrying the API's message; a 409 on delete keeps
// its usage counts in that message.
func taxonomyRequest(method, path string, body any) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, strings.TrimRight(common.endpoint, "/")+path, reader)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set(sdk.MetadataAuthorization, sdk.BearerPrefix+common.token)
	resp, err := cmdhttp.Client(30 * time.Second).Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		var e struct {
			Error string `json:"error"`
		}
		msg := strings.TrimSpace(string(raw))
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			msg = e.Error
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, msg)
	}
	return raw, nil
}
