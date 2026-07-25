package scim

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Slice 733 — the SCIM Group resource (RFC 7643 §4.2 / RFC 7644). This file
// holds the pure-Go wire marshaling + member-PatchOp parsing (the fast
// unit-test surface), mirroring scim.go's User shaping. The DB-backed group
// store lives in groupstore.go.
//
// A SCIM Group carries identity + membership ONLY. It NEVER carries a role:
// the group's membership is fed to the slice-509 grouprole.Resolver (the sole
// path to a role), so a Group resource cannot escalate a role outside the 509
// mapping (P0-733-3). There is no role attribute on this wire shape, by design.

// SchemaGroup is the SCIM core Group schema URN (RFC 7643 §8).
const SchemaGroup = "urn:ietf:params:scim:schemas:core:2.0:Group"

// ResourceTypeGroup is the SCIM resource-type name for groups.
const ResourceTypeGroup = "Group"

// GroupMember is one entry in a SCIM Group `members` array (RFC 7643 §4.2).
// `Value` is the member's resource id (an atlas user id). `Ref` ($ref) and
// `Display` are advisory.
type GroupMember struct {
	Value   string `json:"value"`
	Ref     string `json:"$ref,omitempty"`
	Display string `json:"display,omitempty"`
	Type    string `json:"type,omitempty"`
}

// Group is the SCIM core Group resource (RFC 7643 §4.2) — the subset slice 733
// maps. `Members` is the membership edge set. Roles are deliberately absent
// (P0-733-3 — membership is identity data; only the slice-509 resolver turns it
// into a role).
type Group struct {
	Schemas     []string      `json:"schemas"`
	ID          string        `json:"id"`
	ExternalID  string        `json:"externalId,omitempty"`
	DisplayName string        `json:"displayName"`
	Members     []GroupMember `json:"members"`
	Meta        *Meta         `json:"meta,omitempty"`
}

// DomainGroup is the platform-side projection of a SCIM group. The handler
// renders this (plus the member user ids) into the wire Group.
type DomainGroup struct {
	ID          uuid.UUID
	TenantID    uuid.UUID
	DisplayName string
	ExternalID  string
	Active      bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// GroupRef returns the value the slice-509 resolver matches mappings against:
// the externalId when present, else the display name. Snapshotted into
// scim_group_members.group_ref at membership-write time so a re-derivation
// reuses the exact identifier the oidc_idp_group_mappings table is keyed on.
func (g DomainGroup) GroupRef() string {
	if g.ExternalID != "" {
		return g.ExternalID
	}
	return g.DisplayName
}

// WireGroup renders a DomainGroup + its member user ids into the SCIM wire
// Group. baseLocation is the resource collection URL (e.g.
// "https://host/scim/v2/Groups"). Roles are NEVER emitted (P0-733-3).
func WireGroup(g DomainGroup, memberIDs []string, baseLocation string) Group {
	members := make([]GroupMember, 0, len(memberIDs))
	loc := strings.TrimRight(baseLocation, "/")
	usersLoc := strings.TrimSuffix(loc, "/Groups") + "/Users/"
	for _, id := range memberIDs {
		members = append(members, GroupMember{
			Value: id,
			Ref:   usersLoc + id,
			Type:  "User",
		})
	}
	return Group{
		Schemas:     []string{SchemaGroup},
		ID:          g.ID.String(),
		ExternalID:  g.ExternalID,
		DisplayName: g.DisplayName,
		Members:     members,
		Meta: &Meta{
			ResourceType: ResourceTypeGroup,
			Created:      formatRFC3339(g.CreatedAt),
			LastModified: formatRFC3339(g.UpdatedAt),
			Location:     loc + "/" + g.ID.String(),
		},
	}
}

// --- displayName filter parsing (the List filter minimum) ---

// ParseDisplayNameFilter extracts the value from a `displayName eq "value"`
// filter. Returns ("", false, nil) when filter is empty (no filter → full
// list). Returns ErrUnsupportedFilter for any other shape. Mirrors the
// slice-508 ParseUserNameFilter contract.
func ParseDisplayNameFilter(filter string) (value string, present bool, err error) {
	f := strings.TrimSpace(filter)
	if f == "" {
		return "", false, nil
	}
	const attr = "displayname"
	const op = "eq"
	open := strings.Index(f, "\"")
	if open < 0 || !strings.HasSuffix(f, "\"") || len(f) < open+2 {
		return "", false, ErrUnsupportedFilter
	}
	prefix := strings.Fields(strings.ToLower(strings.TrimSpace(f[:open])))
	if len(prefix) != 2 || prefix[0] != attr || prefix[1] != op {
		return "", false, ErrUnsupportedFilter
	}
	val := f[open+1 : len(f)-1]
	if strings.Contains(val, "\"") {
		return "", false, ErrUnsupportedFilter
	}
	return val, true, nil
}

// --- Group member PatchOp parsing (RFC 7644 §3.5.2) ---

// GroupPatchIntent is the resolved effect of a Group PatchOp after the
// attribute allow-list ({displayName, members}). It is the PURE output the
// store applies — no DB, exhaustively unit-testable.
type GroupPatchIntent struct {
	// SetDisplayName + DisplayName: a displayName replace.
	SetDisplayName bool
	DisplayName    string
	// AddMembers / RemoveMembers: member user ids to add / remove.
	AddMembers    []string
	RemoveMembers []string
	// ReplaceMembers true means the `members` attribute was REPLACED wholesale
	// (clear-then-set to ReplaceMemberSet) rather than incrementally added to.
	ReplaceMembers   bool
	ReplaceMemberSet []string
}

// PlanGroupPatch walks the PatchOp operations and extracts ONLY the allow-listed
// attributes ({displayName, members}). An unknown path is silently skipped (be
// liberal in what we accept). Member ops:
//
//   - add    members           → AddMembers (incremental).
//   - replace members          → ReplaceMembers (wholesale set).
//   - remove members           → ReplaceMembers to empty (clear all).
//   - remove members[value eq "x"] → RemoveMembers (single, via the filtered
//     path Okta/Entra emit).
//
// The value shapes IdPs send vary (an array of {value} objects, a bare array of
// ids, or a single object); decodeMemberValues normalizes them.
func PlanGroupPatch(ops []PatchOperation) (GroupPatchIntent, error) {
	var intent GroupPatchIntent
	for _, op := range ops {
		action := normalizeOp(op.Op)
		path := normalizePath(op.Path)
		// A filtered member-remove path: `members[value eq "x"]` normalizes via
		// normalizePath's last-colon strip to something starting with "members[".
		rawPath := strings.ToLower(strings.TrimSpace(op.Path))
		switch {
		case path == "displayname" && (action == "add" || action == "replace"):
			s, ok := decodeString(op.Value)
			if !ok {
				return GroupPatchIntent{}, errGroupPatchValue("displayName", "string")
			}
			intent.SetDisplayName = true
			intent.DisplayName = s
		case path == "members" || strings.HasPrefix(rawPath, "members["):
			ids := decodeMemberValues(op.Value)
			switch action {
			case "add":
				intent.AddMembers = append(intent.AddMembers, ids...)
			case "replace":
				// Wholesale replace of the members set.
				intent.ReplaceMembers = true
				intent.ReplaceMemberSet = append(intent.ReplaceMemberSet, ids...)
			case "remove":
				if filterVal, ok := memberFilterValue(rawPath); ok {
					// `members[value eq "x"]` — remove that single member.
					intent.RemoveMembers = append(intent.RemoveMembers, filterVal)
				} else if len(ids) > 0 {
					// `remove` with an explicit value list.
					intent.RemoveMembers = append(intent.RemoveMembers, ids...)
				} else {
					// Bare `remove members` — clear all.
					intent.ReplaceMembers = true
					intent.ReplaceMemberSet = nil
				}
			}
		case path == "":
			// No-path replace: value is an object of attributes. Read only
			// displayName + members.
			var obj map[string]json.RawMessage
			if err := json.Unmarshal(op.Value, &obj); err != nil {
				continue
			}
			for k, raw := range obj {
				switch normalizePath(k) {
				case "displayname":
					if s, ok := decodeString(raw); ok {
						intent.SetDisplayName = true
						intent.DisplayName = s
					}
				case "members":
					intent.ReplaceMembers = true
					intent.ReplaceMemberSet = append(intent.ReplaceMemberSet, decodeMemberValues(raw)...)
				}
			}
		default:
			// unknown path / op: ignore.
			continue
		}
	}
	return intent, nil
}

// memberFilterValue extracts "x" from a `members[value eq "x"]` path
// (lowercased). Returns ("", false) when the path is not a single-value member
// filter.
func memberFilterValue(rawPath string) (string, bool) {
	const prefix = "members["
	if !strings.HasPrefix(rawPath, prefix) || !strings.HasSuffix(rawPath, "]") {
		return "", false
	}
	inner := rawPath[len(prefix) : len(rawPath)-1]
	// Expect: value eq "x"
	open := strings.Index(inner, "\"")
	if open < 0 || !strings.HasSuffix(inner, "\"") || len(inner) < open+2 {
		return "", false
	}
	lead := strings.Fields(strings.TrimSpace(inner[:open]))
	if len(lead) != 2 || lead[0] != "value" || lead[1] != "eq" {
		return "", false
	}
	val := inner[open+1 : len(inner)-1]
	if val == "" || strings.Contains(val, "\"") {
		return "", false
	}
	return val, true
}

// decodeMemberValues normalizes the many shapes IdPs send for a `members`
// value into a flat list of member ids:
//
//   - [{"value":"id1"},{"value":"id2"}]   (RFC canonical)
//   - ["id1","id2"]                        (bare id array)
//   - {"value":"id1"}                      (single object)
//   - "id1"                                (bare string)
//
// Empty / unparseable values yield an empty slice (never an error — be liberal).
func decodeMemberValues(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	// Array of objects.
	var objs []GroupMember
	if err := json.Unmarshal(raw, &objs); err == nil {
		out := make([]string, 0, len(objs))
		for _, o := range objs {
			if v := strings.TrimSpace(o.Value); v != "" {
				out = append(out, v)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	// Bare string array.
	var strs []string
	if err := json.Unmarshal(raw, &strs); err == nil {
		out := make([]string, 0, len(strs))
		for _, s := range strs {
			if v := strings.TrimSpace(s); v != "" {
				out = append(out, v)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	// Single object.
	var one GroupMember
	if err := json.Unmarshal(raw, &one); err == nil && strings.TrimSpace(one.Value) != "" {
		return []string{strings.TrimSpace(one.Value)}
	}
	// Bare string.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil && strings.TrimSpace(s) != "" {
		return []string{strings.TrimSpace(s)}
	}
	return nil
}

// errGroupPatchValue builds the typed-value error for a Group PatchOp.
func errGroupPatchValue(attr, typ string) error {
	return &patchValueError{attr: attr, typ: typ}
}

type patchValueError struct {
	attr string
	typ  string
}

func (e *patchValueError) Error() string {
	return "scim: PatchOp `" + e.attr + "` value must be " + e.typ
}

// dedupeStrings returns the input with duplicates removed, order preserved.
func dedupeStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
