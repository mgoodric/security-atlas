package policy

import (
	"errors"
	"net/url"
	"strings"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// Names of the systems a synced policy can come from (#2837). The badge
// on a synced policy and the 409 refusing an in-tool edit name it.
const (
	SyncSourceConfluence = "Confluence"
	SyncSourceSharePoint = "SharePoint"
)

// sharePointHostSuffix is the host of every SharePoint Online site the
// SharePoint policy connector accepts (connectors/sharepoint).
const sharePointHostSuffix = ".sharepoint.com"

// SyncSourceName names the system a sync source's site belongs to. The
// sync contract carries only a site and a space (#2850), so the name is
// read from the site: a *.sharepoint.com site is SharePoint, and any
// other site is Confluence, the first connector, whose sites (including
// custom domains) have no single host suffix.
func SyncSourceName(site string) string {
	site = strings.TrimSpace(site)
	if !strings.Contains(site, "://") {
		// A bare host, as "acme.atlassian.net".
		site = "https://" + site
	}
	u, err := url.Parse(site)
	if err != nil {
		return SyncSourceConfluence
	}
	if isSharePointHost(u.Hostname()) {
		return SyncSourceSharePoint
	}
	return SyncSourceConfluence
}

// isSharePointHost reports whether host is <name>.sharepoint.com: the
// suffix with at least one character before it. It is the rule the
// SharePoint connector applies to the sites it accepts
// (connectors/sharepoint/internal/graph.IsSharePointHost), restated here
// because the platform does not import connector code.
func isSharePointHost(host string) bool {
	host = strings.ToLower(host)
	return strings.HasSuffix(host, sharePointHostSuffix) && len(host) > len(sharePointHostSuffix)
}

// ManagedBySyncError refuses an in-tool edit of a synced policy (#2852),
// naming the system its page lives in (#2837). errors.Is(err,
// ErrManagedBySync) matches it.
type ManagedBySyncError struct {
	// Source is the system the policy is synced from, e.g. "SharePoint".
	Source string
}

func (e *ManagedBySyncError) Error() string {
	return "policy: managed in " + e.Source + "; edit it at its source page"
}

// Is makes errors.Is(err, ErrManagedBySync) hold.
func (e *ManagedBySyncError) Is(target error) bool { return target == ErrManagedBySync }

// SyncSourceOf names the source of err when it refuses an edit of a
// synced policy, and reports whether it does. A bare ErrManagedBySync
// names Confluence.
func SyncSourceOf(err error) (string, bool) {
	var m *ManagedBySyncError
	if errors.As(err, &m) {
		return m.Source, true
	}
	if errors.Is(err, ErrManagedBySync) {
		return SyncSourceConfluence, true
	}
	return "", false
}

// managedBySync is the refusal for an edit of the synced row r.
func managedBySync(r dbx.Policy) error {
	site := ""
	if r.SyncSite != nil {
		site = *r.SyncSite
	}
	return &ManagedBySyncError{Source: SyncSourceName(site)}
}
