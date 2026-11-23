package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/connectors/confluence/internal/confluenceapi"
	"github.com/mgoodric/security-atlas/connectors/confluence/internal/policysync"
)

// ConnectorName names the binary in help text.
const ConnectorName = "atlas-confluence"

// Env vars for the configuration that is not the Confluence credential
// (confluenceapi reads CONFLUENCE_EMAIL and CONFLUENCE_API_TOKEN).
const (
	EnvSite      = "CONFLUENCE_SITE"
	EnvSpace     = "CONFLUENCE_SPACE"
	EnvLabel     = "CONFLUENCE_LABEL"
	EnvAtlasURL  = "SECURITY_ATLAS_URL"
	EnvAtlasKey  = "SECURITY_ATLAS_POLICY_SYNC_KEY"
	defaultLabel = "policy"
)

// config is one run's configuration.
type config struct {
	site            string
	space           string
	label           string
	confluenceEmail string
	atlasURL        string
	// atlasKey is read from the environment only, as is the Confluence
	// API token: neither has a flag, so neither reaches ps or shell
	// history.
	atlasKey string
	timeout  time.Duration
	// maxSkipped is how many pages the converter may refuse before an
	// accepted push still exits ExitNeedsAttention.
	maxSkipped int
}

// ExitNeedsAttention is the exit status of a run whose push the platform
// accepted, but which a scheduler should alert on: more pages refused by
// the converter than --max-skipped, or a platform warning. Exit 1 means
// nothing was pushed, or the platform refused the push.
const ExitNeedsAttention = 3

// exitError carries an exit status other than 1 to main.
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }

// exitCode is the process exit status for a run's error: an exitError's
// own code, else 1.
func exitCode(err error) int {
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	return 1
}

// resolve fills unset values from the environment and checks them.
func (c *config) resolve() error {
	c.site = firstNonEmpty(c.site, os.Getenv(EnvSite))
	c.space = strings.TrimSpace(firstNonEmpty(c.space, os.Getenv(EnvSpace)))
	c.label = strings.TrimSpace(firstNonEmpty(c.label, os.Getenv(EnvLabel), defaultLabel))
	c.atlasURL = firstNonEmpty(c.atlasURL, os.Getenv(EnvAtlasURL))
	c.atlasKey = strings.TrimSpace(firstNonEmpty(c.atlasKey, os.Getenv(EnvAtlasKey)))
	var missing []string
	for _, m := range []struct{ flag, env, val string }{
		{"--site", EnvSite, c.site},
		{"--space", EnvSpace, c.space},
		{"--atlas-url", EnvAtlasURL, c.atlasURL},
		{"", EnvAtlasKey, c.atlasKey},
	} {
		if strings.TrimSpace(m.val) == "" {
			if m.flag == "" {
				missing = append(missing, "env "+m.env)
				continue
			}
			missing = append(missing, m.flag+" (env "+m.env+")")
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("required: %s", strings.Join(missing, ", "))
	}
	if c.timeout <= 0 {
		return errors.New("--timeout must be positive")
	}
	if c.maxSkipped < 0 {
		return errors.New("--max-skipped must be zero or more")
	}
	return nil
}

func newRootCmd(out io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:           ConnectorName,
		Short:         "security-atlas Confluence policy connector",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newRunCmd(out))
	return root
}

func newRunCmd(out io.Writer) *cobra.Command {
	var c config
	cmd := &cobra.Command{
		Use:   "run",
		Short: "sync labelled Confluence pages to security-atlas policies",
		Long: `Read every page labelled --label in --space, and push the full set to
POST /v1/policies:sync in one request. Prints one report line per page.

Secrets come only from the environment, never from flags, so they never
appear in a process listing or shell history:
  CONFLUENCE_API_TOKEN             the Confluence API token
  SECURITY_ATLAS_POLICY_SYNC_KEY   the policy-sync API key

Exit status:
  0  the push was accepted
  1  nothing was pushed (a Confluence failure, no labelled pages, no
     convertible body), or the platform refused the push
  3  the push was accepted, but more than --max-skipped pages were sent
     unreadable because their body could not be converted, or the
     platform returned a warning. Alert on it.

Nothing is pushed unless the whole labelled set was read.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := c.resolve(); err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), c.timeout)
			defer cancel()
			return run(ctx, c, &http.Client{Timeout: 60 * time.Second}, out)
		},
	}
	f := cmd.Flags()
	f.StringVar(&c.site, "site", "", "Confluence Cloud site, e.g. https://acme.atlassian.net (env "+EnvSite+")")
	f.StringVar(&c.space, "space", "", "space key the policy pages live in (env "+EnvSpace+")")
	f.StringVar(&c.label, "label", "", `label marking a page as a policy (env `+EnvLabel+`; default "`+defaultLabel+`")`)
	f.StringVar(&c.confluenceEmail, "confluence-email", "", "Atlassian account email (env "+confluenceapi.EnvEmail+" preferred)")
	f.StringVar(&c.atlasURL, "atlas-url", "", "security-atlas base URL, e.g. https://atlas.example.com (env "+EnvAtlasURL+")")
	f.DurationVar(&c.timeout, "timeout", 10*time.Minute, "limit on the whole run")
	f.IntVar(&c.maxSkipped, "max-skipped", 0, "pages the converter may refuse before the run exits 3")
	return cmd
}

// run is one sync: read the full set, push it, write the report.
func run(ctx context.Context, c config, hc *http.Client, out io.Writer) error {
	// Both requests carry a secret, so a redirect may not move them to
	// another host or down to http.
	guarded := *hc
	guarded.CheckRedirect = confluenceapi.CheckRedirect
	hc = &guarded
	site, err := confluenceapi.CheckBaseURL("--site", c.site)
	if err != nil {
		return err
	}
	platform, err := confluenceapi.CheckBaseURL("--atlas-url", c.atlasURL)
	if err != nil {
		return err
	}
	cred, err := confluenceapi.ResolveCredential(c.confluenceEmail, "")
	if err != nil {
		return err
	}
	client := &confluenceapi.Client{HTTP: hc, Site: site, Cred: cred}
	pages, err := client.LabelledPages(ctx, c.space, c.label)
	if err != nil {
		return fmt.Errorf("read Confluence (nothing pushed): %w", err)
	}
	// An empty push would report every policy synced from this space as
	// missing. A misspelt label or space reads the same as an empty
	// space, so it is refused rather than pushed.
	if len(pages) == 0 {
		return fmt.Errorf("no pages labelled %q in space %q (nothing pushed)", c.label, c.space)
	}

	req, notes := policysync.Build(policysync.Source{Site: site.String(), Space: c.space}, pages)
	// Unreadable pages alone are not pushed: a converter that refuses
	// every body is a connector fault, not a source to report on.
	if req.Readable() == 0 {
		policysync.WriteReport(out, policysync.Report{}, notes)
		return errors.New("no page body could be converted (nothing pushed)")
	}
	rep, err := policysync.Push(ctx, hc, platform, c.atlasKey, req)
	if err != nil {
		return err
	}
	policysync.WriteReport(out, rep, notes)
	return needsAttention(c.maxSkipped, policysync.ConverterSkipped(notes), len(rep.Warnings))
}

// needsAttention turns an accepted push the scheduler should still alert
// on into an ExitNeedsAttention error. A converter regression that
// refuses many but not all pages would otherwise exit 0 while the
// platform holds those pages unreadable, their policies frozen.
func needsAttention(maxSkipped, skipped, warnings int) error {
	var why []string
	if skipped > maxSkipped {
		why = append(why, fmt.Sprintf("%d page(s) not converted and sent unreadable, more than --max-skipped=%d", skipped, maxSkipped))
	}
	if warnings > 0 {
		why = append(why, fmt.Sprintf("%d platform warning(s)", warnings))
	}
	if len(why) == 0 {
		return nil
	}
	return &exitError{code: ExitNeedsAttention, msg: "push accepted, needs attention: " + strings.Join(why, "; ")}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
