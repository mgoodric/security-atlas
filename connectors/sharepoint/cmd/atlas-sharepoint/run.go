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

	"github.com/mgoodric/security-atlas/connectors/shared/policysync"
	"github.com/mgoodric/security-atlas/connectors/sharepoint/internal/docsync"
	"github.com/mgoodric/security-atlas/connectors/sharepoint/internal/graph"
)

// ConnectorName names the binary in help text.
const ConnectorName = "atlas-sharepoint"

// Env vars for the configuration that is not the Entra credential (graph
// reads SHAREPOINT_TENANT_ID, SHAREPOINT_CLIENT_ID and
// SHAREPOINT_CLIENT_SECRET).
const (
	EnvSite     = "SHAREPOINT_SITE"
	EnvLibrary  = "SHAREPOINT_LIBRARY"
	EnvTimeZone = "SHAREPOINT_TIME_ZONE"
	EnvAtlasURL = "SECURITY_ATLAS_URL"
	EnvAtlasKey = "SECURITY_ATLAS_POLICY_SYNC_KEY"
)

// config is one run's configuration.
type config struct {
	site     string
	library  string
	tenantID string
	clientID string
	timeZone string
	atlasURL string
	// atlasKey is read from the environment only, as is the client
	// secret: neither has a flag, so neither reaches ps or shell history.
	atlasKey string
	timeout  time.Duration
	// maxSkipped is how many documents the converter may refuse before an
	// accepted push still exits ExitNeedsAttention.
	maxSkipped int

	loc *time.Location
}

// ExitNeedsAttention is the exit status of a run whose push the platform
// accepted, but which a scheduler should alert on: more documents
// refused by the converter than --max-skipped, or a platform warning.
// Exit 1 means nothing was pushed, or the platform refused the push.
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
	c.library = strings.TrimSpace(firstNonEmpty(c.library, os.Getenv(EnvLibrary)))
	c.timeZone = strings.TrimSpace(firstNonEmpty(c.timeZone, os.Getenv(EnvTimeZone), "UTC"))
	c.atlasURL = firstNonEmpty(c.atlasURL, os.Getenv(EnvAtlasURL))
	c.atlasKey = strings.TrimSpace(firstNonEmpty(c.atlasKey, os.Getenv(EnvAtlasKey)))
	var missing []string
	for _, m := range []struct{ flag, env, val string }{
		{"--site", EnvSite, c.site},
		{"--library", EnvLibrary, c.library},
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
	loc, err := time.LoadLocation(c.timeZone)
	if err != nil {
		return fmt.Errorf("--time-zone %q is not an IANA time zone: %w", c.timeZone, err)
	}
	c.loc = loc
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
		Short:         "security-atlas SharePoint policy connector",
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
		Short: "sync a SharePoint document library to security-atlas policies",
		Long: `Read every document in the --library document library of --site through
Microsoft Graph, and push the full set to POST /v1/policies:sync in one
request. Prints one report line per document.

Secrets come only from the environment, never from flags, so they never
appear in a process listing or shell history:
  SHAREPOINT_CLIENT_SECRET         the Entra app registration's client secret
  SECURITY_ATLAS_POLICY_SYNC_KEY   the policy-sync API key

Exit status:
  0  the push was accepted
  1  nothing was pushed (a Graph failure, an empty library, no
     convertible document), or the platform refused the push
  3  the push was accepted, but more than --max-skipped documents were
     sent unreadable because they could not be converted, or the
     platform returned a warning. Alert on it.

Nothing is pushed unless the whole library was read.`,
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
	f.StringVar(&c.site, "site", "", "SharePoint site, e.g. https://contoso.sharepoint.com/sites/Policies (env "+EnvSite+")")
	f.StringVar(&c.library, "library", "", "document library holding the policies, by display name (env "+EnvLibrary+")")
	f.StringVar(&c.tenantID, "tenant-id", "", "Entra directory (tenant) id (env "+graph.EnvTenantID+")")
	f.StringVar(&c.clientID, "client-id", "", "Entra app registration's application (client) id (env "+graph.EnvClientID+")")
	f.StringVar(&c.timeZone, "time-zone", "", `IANA time zone date columns are read in, the site's (env `+EnvTimeZone+`; default "UTC")`)
	f.StringVar(&c.atlasURL, "atlas-url", "", "security-atlas base URL, e.g. https://atlas.example.com (env "+EnvAtlasURL+")")
	f.DurationVar(&c.timeout, "timeout", 10*time.Minute, "limit on the whole run")
	f.IntVar(&c.maxSkipped, "max-skipped", 0, "documents the converter may refuse before the run exits 3")
	return cmd
}

// run is one sync: read the full library, push it, write the report.
func run(ctx context.Context, c config, hc *http.Client, out io.Writer) error {
	// The token, Graph and platform requests carry a secret, so a
	// redirect may not move them to another host or down to http.
	guarded := *hc
	guarded.CheckRedirect = policysync.CheckRedirect
	hc = &guarded
	site, err := graph.CheckSiteURL(c.site)
	if err != nil {
		return err
	}
	platform, err := policysync.CheckBaseURL("--atlas-url", c.atlasURL)
	if err != nil {
		return err
	}
	cred, err := graph.ResolveCredential(c.tenantID, c.clientID)
	if err != nil {
		return err
	}
	client := &graph.Client{HTTP: hc, Cred: cred}
	lib, docs, err := client.Documents(ctx, site, c.library)
	if err != nil {
		return fmt.Errorf("read SharePoint (nothing pushed): %w", err)
	}
	// An empty push would report every policy synced from this library
	// as missing. An emptied library reads the same as a wrong one, so it
	// is refused rather than pushed.
	if len(docs) == 0 {
		return fmt.Errorf("no documents in library %q (nothing pushed)", c.library)
	}

	// The source identifies the set across runs. SharePoint URLs ignore
	// case, so the site is lower-cased, and the library is named as Graph
	// returns it: a change of case in --site or --library must not read
	// as a new source whose policies duplicate the old one's.
	src := policysync.Source{Site: strings.ToLower(site.String()), Space: lib.Name}
	req, notes := docsync.Build(src, docs, c.loc)
	// Unreadable documents alone are not pushed: a converter that refuses
	// every body is a connector fault, not a source to report on.
	if req.Readable() == 0 {
		docsync.WriteReport(out, policysync.Report{}, notes)
		return errors.New("no document could be converted (nothing pushed)")
	}
	rep, err := policysync.Push(ctx, hc, platform, c.atlasKey, req)
	if err != nil {
		return err
	}
	docsync.WriteReport(out, rep, notes)
	return needsAttention(c.maxSkipped, docsync.ConverterSkipped(notes), len(rep.Warnings))
}

// needsAttention turns an accepted push the scheduler should still alert
// on into an ExitNeedsAttention error.
func needsAttention(maxSkipped, skipped, warnings int) error {
	var why []string
	if skipped > maxSkipped {
		why = append(why, fmt.Sprintf("%d document(s) not converted and sent unreadable, more than --max-skipped=%d", skipped, maxSkipped))
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
