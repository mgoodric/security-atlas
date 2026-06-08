// Chained profile-over-profile resolution: the Go-side graph validator
// (slice 578). Slice 511 bounded a profile import to a single level (profile
// -> catalog) by construction. This file lifts that bound to a BOUNDED chain
// (profile -> profile -> ... -> catalog) and enforces the two load-bearing
// safety properties that MUST hold before the bytes ever reach the bridge:
//
//  1. Cycle detection — a profile that imports itself, directly or
//     transitively (A -> B -> A), is a structured error, never an infinite
//     loop or fetch (P0-578-2).
//  2. Depth bound — a chain deeper than MaxChainDepth is a structured error
//     so a pathological/malicious chain cannot exhaust resources (AC-3).
//
// It also carries forward slice 511's no-external-dereference guard
// (P0-511-1 / P0-578-1): every import.href is matched ONLY against the
// supplied documents; an external scheme or an href that maps to no supplied
// document is rejected with no fetch.
//
// This logic is DELIBERATELY Go-side and pure (no bridge, no Postgres) so the
// cycle + depth + no-deref properties are unit-testable without compliance-
// trestle. The bridge still performs the actual import/merge/modify
// resolution (slice 511 D1) once the chain is proven safe; the bridge lays
// every supplied profile + catalog into its sandbox and rewrites every href
// to a trestle:// path, so trestle descends the (already-validated) chain
// using only LocalFetcher reads inside the sandbox.

package profileimport

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// MaxChainDepth bounds the profile-over-profile import chain. The depth is the
// number of PROFILE links traversed from the entry profile down to (and
// including) the profile whose import names a catalog; a profile that imports a
// catalog directly is depth 1.
//
// N = 8 is chosen deliberately. Real-world OSCAL tailoring chains are shallow:
// the FedRAMP Low/Moderate/High baselines are profile-over-catalog (depth 1),
// and an agency overlay tailoring a baseline is depth 2. A handful of levels
// covers every legitimate layered-overlay shape (org -> business-unit ->
// system tailoring) with generous headroom, while 8 is far below any value at
// which a non-malicious author would plausibly arrive — so the bound bites
// only on a pathological or adversarial chain. The cap is conservative and
// can be lifted by one constant if a real-world deeper chain ever surfaces.
const MaxChainDepth = 8

// MaxSuppliedProfiles caps how many intermediate profiles the caller may
// supply for a single chained resolution (threat-model D — bound the
// resolution working set, mirroring MaxSuppliedCatalogs). The entry profile is
// counted separately (it travels in ProfileJSON, not in Profiles).
const MaxSuppliedProfiles = 32

// Sentinel errors the chain validator surfaces. They wrap ErrResolutionFailed
// so the existing CLI / caller mapping (and the persist-nothing contract) is
// unchanged, while a test can assert the specific failure class.
var (
	// ErrChainTooDeep is returned when the import chain exceeds MaxChainDepth.
	ErrChainTooDeep = fmt.Errorf("%w: import chain exceeds the maximum depth of %d", ErrResolutionFailed, MaxChainDepth)
	// ErrChainCycle is returned when a profile imports itself directly or
	// transitively (P0-578-2).
	ErrChainCycle = fmt.Errorf("%w: import chain contains a cycle", ErrResolutionFailed)
	// ErrUnresolvableImport is returned when an import.href maps to no supplied
	// document (carries forward P0-511-1; no fetch is ever attempted).
	ErrUnresolvableImport = fmt.Errorf("%w: an import.href maps to no supplied document", ErrResolutionFailed)
	// ErrExternalImport is returned when an import.href names an external/host
	// resource (carries forward P0-511-1 / P0-578-1).
	ErrExternalImport = fmt.Errorf("%w: an import.href is an external reference", ErrResolutionFailed)
	// ErrTooManyProfiles is returned when more than MaxSuppliedProfiles
	// intermediate profiles are supplied.
	ErrTooManyProfiles = errors.New("profileimport: too many supplied profiles")
	// ErrMalformedChainDoc is returned when a supplied chain document is not
	// parseable OSCAL JSON the validator can walk.
	ErrMalformedChainDoc = fmt.Errorf("%w: a supplied document is not parseable", ErrResolutionFailed)
)

// externalHrefPrefixes mirrors the bridge's _EXTERNAL_HREF_PREFIXES exactly so
// the Go-side gate and the bridge-side gate agree on what "external" means. An
// href beginning with one of these is an explicit external/host reference that
// is never dereferenced (P0-578-1 / P0-511-1).
var externalHrefPrefixes = []string{"https://", "http://", "sftp://", "ftp://", "file:", "//"}

// isExternalHref reports whether href names an external/host resource that must
// never be fetched.
func isExternalHref(href string) bool {
	h := strings.ToLower(strings.TrimSpace(href))
	for _, p := range externalHrefPrefixes {
		if strings.HasPrefix(h, p) {
			return true
		}
	}
	return false
}

// chainDoc is a supplied document (entry profile, an intermediate profile, or a
// catalog) reduced to the identity + import edges the graph walk needs. Only
// the fields the validator reads are decoded; everything else is ignored.
type chainDoc struct {
	// key is the unique identity token used for cycle tracking + href
	// matching (derived from uuid then title-slug then ordinal — same
	// precedence the bridge uses).
	key string
	// isProfile is true for a profile document, false for a catalog (a
	// catalog is a chain terminal — it has no imports to follow).
	isProfile bool
	// uuid is the document's declared OSCAL uuid (may be empty).
	uuid string
	// titleSlug is the slugified metadata.title (may be empty).
	titleSlug string
	// imports holds each import.href, in document order (profiles only).
	imports []string
}

// minimalProfile / minimalCatalog decode just enough of the OSCAL JSON to walk
// the import graph. The bridge owns full OSCAL validation; the Go side only
// needs the identity + edges.
type minimalProfile struct {
	Profile *struct {
		UUID     string `json:"uuid"`
		Metadata *struct {
			Title string `json:"title"`
		} `json:"metadata"`
		Imports []struct {
			Href string `json:"href"`
		} `json:"imports"`
	} `json:"profile"`
}

type minimalCatalog struct {
	Catalog *struct {
		UUID     string `json:"uuid"`
		Metadata *struct {
			Title string `json:"title"`
		} `json:"metadata"`
	} `json:"catalog"`
}

// slugify lowercases a string into a stable match/identity token, mirroring the
// bridge's _catalog_slug so Go-side matching and bridge-side matching agree.
func slugify(raw string) string {
	var b strings.Builder
	lastDash := true // collapse leading dashes
	for _, r := range strings.ToLower(raw) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
		} else if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// matchHref maps an import.href to a supplied document key WITHOUT fetching,
// mirroring the bridge's _match_import_href precedence: a uuid match (exact or
// contained), then a trailing-segment title-slug match. Returns the matched
// doc and true, or false on no match.
func matchHref(href string, docs []*chainDoc) (*chainDoc, bool) {
	h := strings.TrimSpace(href)
	token := strings.TrimPrefix(h, "#")
	trailing := token
	if i := strings.LastIndex(strings.TrimRight(trailing, "/"), "/"); i >= 0 {
		trailing = strings.TrimRight(trailing, "/")[i+1:]
	}
	trailingSlug := slugify(strings.TrimSuffix(trailing, ".json"))
	for _, d := range docs {
		if d.uuid != "" && (token == d.uuid || strings.Contains(token, d.uuid)) {
			return d, true
		}
		if d.titleSlug != "" && trailingSlug == d.titleSlug {
			return d, true
		}
	}
	return nil, false
}

// buildChainDocs parses the entry profile, the intermediate profiles, and the
// catalogs into chainDocs with unique keys (uuid -> title-slug -> ordinal
// precedence, deduplicated so two same-titled docs still get distinct keys).
// The entry profile is always docs[0].
func buildChainDocs(entryProfile []byte, profiles, catalogs [][]byte) ([]*chainDoc, error) {
	docs := make([]*chainDoc, 0, 1+len(profiles)+len(catalogs))
	seen := map[string]int{}

	addKey := func(uuid, titleSlug, fallback string) string {
		base := uuid
		if base == "" {
			base = titleSlug
		}
		if base == "" {
			base = fallback
		}
		base = slugify(base)
		if base == "" {
			base = fallback
		}
		key := base
		for {
			if _, dup := seen[key]; !dup {
				break
			}
			seen[key]++
			key = fmt.Sprintf("%s-%d", base, seen[base])
			seen[base]++
		}
		seen[key] = 0
		return key
	}

	parseProfile := func(raw []byte, fallback string) (*chainDoc, error) {
		var mp minimalProfile
		if err := json.Unmarshal(raw, &mp); err != nil || mp.Profile == nil {
			return nil, fmt.Errorf("%w: %s is not a profile document", ErrMalformedChainDoc, fallback)
		}
		title := ""
		if mp.Profile.Metadata != nil {
			title = mp.Profile.Metadata.Title
		}
		imports := make([]string, 0, len(mp.Profile.Imports))
		for _, imp := range mp.Profile.Imports {
			imports = append(imports, imp.Href)
		}
		return &chainDoc{
			isProfile: true,
			uuid:      mp.Profile.UUID,
			titleSlug: slugify(title),
			imports:   imports,
		}, nil
	}

	// Entry profile first (docs[0]).
	entry, err := parseProfile(entryProfile, "entry-profile")
	if err != nil {
		return nil, err
	}
	entry.key = addKey(entry.uuid, entry.titleSlug, "entry-profile")
	docs = append(docs, entry)

	for i, raw := range profiles {
		d, err := parseProfile(raw, fmt.Sprintf("supplied-profile-%d", i))
		if err != nil {
			return nil, err
		}
		d.key = addKey(d.uuid, d.titleSlug, fmt.Sprintf("supplied-profile-%d", i))
		docs = append(docs, d)
	}

	for i, raw := range catalogs {
		var mc minimalCatalog
		if err := json.Unmarshal(raw, &mc); err != nil || mc.Catalog == nil {
			return nil, fmt.Errorf("%w: supplied catalog #%d is not a catalog document", ErrMalformedChainDoc, i)
		}
		title := ""
		if mc.Catalog.Metadata != nil {
			title = mc.Catalog.Metadata.Title
		}
		d := &chainDoc{
			isProfile: false,
			uuid:      mc.Catalog.UUID,
			titleSlug: slugify(title),
		}
		d.key = addKey(d.uuid, d.titleSlug, fmt.Sprintf("supplied-catalog-%d", i))
		docs = append(docs, d)
	}

	return docs, nil
}

// validateChain walks the import graph rooted at the entry profile (docs[0]),
// enforcing the three load-bearing properties: no external/unresolvable href,
// cycle detection, and the depth bound. It returns nil when the chain is safe
// to hand to the bridge for resolution, or a wrapped ErrResolutionFailed on any
// violation (so the caller persists NOTHING — P0-578-3).
//
// The walk is a depth-first traversal that tracks the set of profile keys on
// the CURRENT path; revisiting a key on the path is a cycle. depth counts the
// profile links traversed; a depth past MaxChainDepth is rejected. A catalog is
// a terminal (no imports to follow) and a valid chain bottom.
func validateChain(docs []*chainDoc) error {
	byKey := make(map[string]*chainDoc, len(docs))
	for _, d := range docs {
		byKey[d.key] = d
	}

	// onPath is the set of profile keys on the current DFS path (cycle
	// detection). It is added on entry and removed on exit so two disjoint
	// branches that both reach the same profile are NOT a false cycle.
	onPath := map[string]bool{}

	var walk func(d *chainDoc, depth int) error
	walk = func(d *chainDoc, depth int) error {
		if !d.isProfile {
			// A catalog is a valid chain terminal — nothing to follow.
			return nil
		}
		if depth > MaxChainDepth {
			return fmt.Errorf("%w (at profile %q)", ErrChainTooDeep, d.key)
		}
		if onPath[d.key] {
			return fmt.Errorf("%w (profile %q revisited)", ErrChainCycle, d.key)
		}
		if len(d.imports) == 0 {
			// A profile with no imports cannot bottom out at a catalog; it
			// resolves to nothing. Treat as unresolvable rather than a silent
			// empty resolution (the bridge would also reject "zero controls").
			return fmt.Errorf("%w (profile %q has no imports)", ErrUnresolvableImport, d.key)
		}
		onPath[d.key] = true
		defer delete(onPath, d.key)

		for i, href := range d.imports {
			h := strings.TrimSpace(href)
			if h == "" {
				return fmt.Errorf("%w (profile %q import #%d has no href)", ErrUnresolvableImport, d.key, i)
			}
			// PRIMARY guard: an explicit external reference is rejected with no
			// fetch and no fallback (P0-578-1 / P0-511-1).
			if isExternalHref(h) {
				return fmt.Errorf("%w (profile %q import #%d href %q)", ErrExternalImport, d.key, i, h)
			}
			matched, ok := matchHref(h, docs)
			if !ok {
				return fmt.Errorf("%w (profile %q import #%d href %q)", ErrUnresolvableImport, d.key, i, h)
			}
			// Descend: a profile target increments the chain depth; a catalog
			// target is a terminal (handled at the top of walk).
			next := depth
			if matched.isProfile {
				next = depth + 1
			}
			if err := walk(matched, next); err != nil {
				return err
			}
		}
		return nil
	}

	return walk(docs[0], 1)
}

// chainLink is one supplied document recorded in the resolved-chain provenance
// (slice 578 — the success-audit detail records WHICH documents resolved the
// baseline and their hashes, so an auditor can later prove the exact chain
// material). It is serialized into the audit-log detail JSON.
type chainLink struct {
	// Role is "entry-profile", "profile" (intermediate), or "catalog".
	Role string `json:"role"`
	// Sha256 is the content hash of the supplied document bytes.
	Sha256 string `json:"sha256"`
	// Bytes is the document size, for quick diagnostics.
	Bytes int `json:"bytes"`
}

// chainProvenance records the full set of supplied chain documents (entry
// profile + intermediate profiles + catalogs) with their hashes for the
// success-audit provenance. The order is stable: entry profile, then
// intermediate profiles in supplied order, then catalogs in supplied order.
// This is provenance over the SUPPLIED material; the actual edges traversed are
// the bridge's resolution concern, but every document recorded here was a
// resolution input.
func chainProvenance(req Request) []chainLink {
	links := make([]chainLink, 0, 1+len(req.Profiles)+len(req.Catalogs))
	links = append(links, chainLink{Role: "entry-profile", Sha256: sha256Hex(req.ProfileJSON), Bytes: len(req.ProfileJSON)})
	for _, p := range req.Profiles {
		links = append(links, chainLink{Role: "profile", Sha256: sha256Hex(p), Bytes: len(p)})
	}
	for _, c := range req.Catalogs {
		links = append(links, chainLink{Role: "catalog", Sha256: sha256Hex(c), Bytes: len(c)})
	}
	return links
}

// chainProfileDepth reports how many PROFILE documents (entry + intermediate)
// participated in the resolution — a coarse provenance signal recorded
// alongside the chain. It is the supplied-profile count, not the traversed
// depth (the traversed depth is bounded + validated separately).
func chainProfileDepth(chain []chainLink) int {
	n := 0
	for _, l := range chain {
		if l.Role == "entry-profile" || l.Role == "profile" {
			n++
		}
	}
	return n
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
