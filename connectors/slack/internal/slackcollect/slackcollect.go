// Package slackcollect reads the three high-signal Slack evidence surfaces
// the slice-443 connector emits: workspace member roster (access evidence),
// admin audit-log entries (admin-action evidence), and message-retention
// settings (data-retention evidence).
//
// Over-collection is the defining risk for a Slack connector (slice 443
// threat-model I): Slack holds extremely sensitive message content this
// package must NEVER touch. Every collector here reads membership / admin /
// retention METADATA only. There is no code path that reads a message body,
// a DM, or channel history, and the result structs physically have no field
// that could carry one. A reflection guard (slackcollect_test.go) fails the
// build if such a field is ever added.
package slackcollect

import (
	"context"
	"fmt"
)

// Result enumerates the per-record verdict. Maps 1:1 onto the gRPC Result
// enum at the cmd layer (mirrors awss3.EncryptionResult).
type Result string

const (
	ResultPass         Result = "pass"
	ResultFail         Result = "fail"
	ResultInconclusive Result = "inconclusive"
)

// Member is one workspace-member access record. Roster METADATA only:
// stable id, handle, role flags, 2FA-enforcement state. Never message
// content, never a DM, never a private profile field beyond the public
// handle/display name.
type Member struct {
	UserID  string // Slack-assigned stable user id (e.g. "U012AB3CD")
	Handle  string // profile.display_name or name — the public handle
	IsAdmin bool
	IsOwner bool
	IsBot   bool
	Deleted bool // deactivated/deprovisioned account
	Has2FA  bool // two_factor_authentication enabled for this member
	Result  Result
	Reason  string // human-readable inconclusive reason
}

// AuditEvent is one admin audit-log entry. Admin-action METADATA only:
// what action, who took it, when, and the entity acted on — never message
// content or the body of any object Slack acted on.
type AuditEvent struct {
	ID         string // Slack audit entry id — also the idempotency anchor
	Action     string // e.g. "user_login", "channel_archive", "role_change"
	ActorID    string // Slack user id of the actor (opaque id, not PII)
	ActorEmail string // present in Slack audit payloads; the access-review join key
	EntityType string // "workspace" | "channel" | "user" | "app" | ...
	DateCreate int64  // Slack epoch-seconds timestamp of the event
}

// RetentionSettings is the workspace message-retention / DLP posture.
// Settings-only — the duration and policy flags, never any retained message.
type RetentionSettings struct {
	TeamID                string
	MessagesRetentionDays int32 // 0 == retain forever
	FilesRetentionDays    int32 // 0 == retain forever
	RetentionEnabled      bool  // a non-default retention policy is in force
	Result                Result
	Reason                string
}

// MembersAPI is the narrow surface this package consumes for the roster
// read. The concrete Slack admin/users client satisfies it; tests pass a
// fake. Paginated to bound a large workspace (threat-model D).
type MembersAPI interface {
	ListMembers(ctx context.Context, cursor string) (members []Member, nextCursor string, err error)
}

// AuditAPI is the narrow surface for the admin audit-log read.
type AuditAPI interface {
	ListAuditEvents(ctx context.Context, cursor string) (events []AuditEvent, nextCursor string, err error)
}

// RetentionAPI is the narrow surface for the retention-settings read.
type RetentionAPI interface {
	GetRetention(ctx context.Context) (RetentionSettings, error)
}

// MaxPages bounds every paginated read so a pathologically large workspace
// (thousands of members, a huge audit log) can never make a run unbounded
// (slice 443 threat-model D — denial of service).
const MaxPages = 100

// CollectMembers walks the paginated member roster and assigns a per-member
// access verdict. A member with 2FA enabled is `pass`; without is `fail`;
// a deleted account is `pass` (correctly deprovisioned, off the hot path).
func CollectMembers(ctx context.Context, api MembersAPI) ([]Member, error) {
	out := make([]Member, 0, 64)
	cursor := ""
	for page := 0; page < MaxPages; page++ {
		members, next, err := api.ListMembers(ctx, cursor)
		if err != nil {
			return nil, fmt.Errorf("slackcollect: list members: %w", err)
		}
		for _, m := range members {
			out = append(out, scoreMember(m))
		}
		if next == "" {
			return out, nil
		}
		cursor = next
	}
	return out, fmt.Errorf("slackcollect: member roster exceeded MaxPages (%d)", MaxPages)
}

// scoreMember assigns the access verdict. Bots are excluded from the 2FA
// posture (they authenticate via tokens, not 2FA) and reported inconclusive
// so the evaluator does not fail a workspace on its bots.
func scoreMember(m Member) Member {
	switch {
	case m.Deleted:
		m.Result = ResultPass // deprovisioned — correct end state
	case m.IsBot:
		m.Result = ResultInconclusive
		m.Reason = "bot account — 2FA not applicable"
	case m.Has2FA:
		m.Result = ResultPass
	default:
		m.Result = ResultFail
		m.Reason = "no two-factor authentication enrolled"
	}
	return m
}

// CollectAuditEvents walks the paginated admin audit-log. Each entry is one
// admin-action evidence record. No verdict is assigned — admin-log entries
// are observational evidence, not pass/fail checks.
func CollectAuditEvents(ctx context.Context, api AuditAPI) ([]AuditEvent, error) {
	out := make([]AuditEvent, 0, 64)
	cursor := ""
	for page := 0; page < MaxPages; page++ {
		events, next, err := api.ListAuditEvents(ctx, cursor)
		if err != nil {
			return nil, fmt.Errorf("slackcollect: list audit events: %w", err)
		}
		out = append(out, events...)
		if next == "" {
			return out, nil
		}
		cursor = next
	}
	return out, fmt.Errorf("slackcollect: audit log exceeded MaxPages (%d)", MaxPages)
}

// CollectRetention reads the single workspace retention-settings record and
// assigns a verdict: a workspace with a finite, enabled retention policy is
// `pass`; a workspace retaining forever with no policy is `fail`.
func CollectRetention(ctx context.Context, api RetentionAPI) (RetentionSettings, error) {
	s, err := api.GetRetention(ctx)
	if err != nil {
		return RetentionSettings{}, fmt.Errorf("slackcollect: get retention: %w", err)
	}
	return scoreRetention(s), nil
}

func scoreRetention(s RetentionSettings) RetentionSettings {
	if s.RetentionEnabled && s.MessagesRetentionDays > 0 {
		s.Result = ResultPass
		return s
	}
	s.Result = ResultFail
	if s.Reason == "" {
		s.Reason = "no finite message-retention policy configured (retain-forever)"
	}
	return s
}
