package slack

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mgoodric/security-atlas/internal/notify"
)

// BuildMessage assembles the minimum-disclosure Slack Block Kit payload
// from a Summary. It carries COUNTS + a single deep-link only — never
// notification contents, evidence, or secrets (P0-543-1 / threat-model I).
//
// All interpolated text is escaped for the Slack text context: Slack treats
// `&`, `<`, `>` as control characters in message text, so they are
// entity-escaped (threat-model T — the 445 HTML-escape analog for the Slack
// context). Type labels come from the CLOSED notify.TypeLabel map, so a raw
// type string from a row never reaches the payload.
func BuildMessage(s notify.Summary) ([]byte, error) {
	if s.TotalUnread <= 0 {
		return nil, fmt.Errorf("slack: empty summary")
	}

	var lines strings.Builder
	fmt.Fprintf(&lines, "*%s*\n",
		slackEscape(fmt.Sprintf("You have %d unread notification%s in security-atlas:",
			s.TotalUnread, notify.Plural(s.TotalUnread))))
	for _, t := range s.SortedTypes() {
		n := s.TypeCounts[t]
		if n <= 0 {
			continue
		}
		// "• Label: N" — label from the closed map, count is an int.
		fmt.Fprintf(&lines, "• %s: %d\n", slackEscape(notify.TypeLabel(t)), n)
	}

	// The deep-link is rendered as a Slack link: <url|text>. The URL is the
	// operator's own base URL (not notification-derived); the link text is a
	// constant. Both are escaped.
	linkText := slackEscape("Open your notifications in security-atlas")
	deepLink := slackEscape(s.DeepLink)

	msg := blockKitMessage{
		// Plain-text fallback for notifications/older clients.
		Text: fmt.Sprintf("You have %d unread notification%s in security-atlas",
			s.TotalUnread, notify.Plural(s.TotalUnread)),
		Blocks: []block{
			{Type: "section", Text: &textObject{Type: "mrkdwn", Text: lines.String()}},
			{Type: "section", Text: &textObject{Type: "mrkdwn",
				Text: fmt.Sprintf("<%s|%s> to see the details.", deepLink, linkText)}},
		},
	}
	return json.Marshal(msg)
}

// slackEscape escapes the three Slack message-text control characters. Per
// Slack's docs, message text must escape `&`, `<`, `>` (in that order, & first
// so the later replacements' entities are not double-escaped).
func slackEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// blockKitMessage is the minimal incoming-webhook payload shape.
type blockKitMessage struct {
	Text   string  `json:"text"`
	Blocks []block `json:"blocks,omitempty"`
}

type block struct {
	Type string      `json:"type"`
	Text *textObject `json:"text,omitempty"`
}

type textObject struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
