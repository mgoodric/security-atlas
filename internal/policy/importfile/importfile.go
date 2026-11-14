// Package importfile parses one policy file for the policy library import
// (#1747): Markdown with YAML front matter, one policy per file.
//
//	---
//	title: Information Security Policy
//	version: "3.2"
//	approved_by_name: Jane Doe
//	approved_at: 2025-03-01
//	effective_date: 2025-03-15
//	next_review_at: 2026-03-01                 # optional
//	owner_role: security_lead                  # optional
//	approver_role: cto                         # optional
//	source_url: https://wiki.example.com/isp   # optional
//	linked_controls: [iac-01-mfa]              # optional
//	acknowledgment_required_roles: [all_staff] # optional
//	---
//	# Purpose
//	...
//
// The Markdown after the closing delimiter is the policy text. Unknown
// front-matter keys fail the file, so a misspelt key is reported rather
// than silently dropped. Parsing is pure: control references are resolved
// against the tenant by policy.Store.Import.
package importfile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/mgoodric/security-atlas/internal/policy"
)

// DefaultRole fills owner_role and approver_role when the front matter
// omits them. Both columns are required; an imported policy often predates
// any role mapping in the tool.
const DefaultRole = "unassigned"

const dateLayout = "2006-01-02"

// frontMatter is the accepted key set. Dates are strings so a quoted and
// an unquoted YAML date parse the same way.
type frontMatter struct {
	Title                       string   `yaml:"title"`
	Version                     string   `yaml:"version"`
	ApprovedByName              string   `yaml:"approved_by_name"`
	ApprovedAt                  string   `yaml:"approved_at"`
	EffectiveDate               string   `yaml:"effective_date"`
	NextReviewAt                string   `yaml:"next_review_at"`
	OwnerRole                   string   `yaml:"owner_role"`
	ApproverRole                string   `yaml:"approver_role"`
	SourceURL                   string   `yaml:"source_url"`
	LinkedControls              []string `yaml:"linked_controls"`
	AcknowledgmentRequiredRoles []string `yaml:"acknowledgment_required_roles"`
}

// Parse turns one file into an import input. name is the file name, kept
// for the audit record and error reports. Every error names the reason.
func Parse(name string, content []byte) (policy.ImportInput, error) {
	fm, body, err := split(content)
	if err != nil {
		return policy.ImportInput{}, err
	}
	var meta frontMatter
	dec := yaml.NewDecoder(bytes.NewReader(fm))
	dec.KnownFields(true)
	if err := dec.Decode(&meta); err != nil {
		if errors.Is(err, io.EOF) {
			return policy.ImportInput{}, errors.New("front matter is empty")
		}
		return policy.ImportInput{}, fmt.Errorf("front matter: %w", err)
	}

	in := policy.ImportInput{
		SourceFile:                  name,
		Title:                       strings.TrimSpace(meta.Title),
		Version:                     strings.TrimSpace(meta.Version),
		BodyMd:                      strings.TrimSpace(body),
		HistoricalApproverName:      strings.TrimSpace(meta.ApprovedByName),
		OwnerRole:                   orDefault(meta.OwnerRole),
		ApproverRole:                orDefault(meta.ApproverRole),
		LinkedControlRefs:           trimAll(meta.LinkedControls),
		AcknowledgmentRequiredRoles: trimAll(meta.AcknowledgmentRequiredRoles),
	}
	for _, req := range []struct{ key, val string }{
		{"title", in.Title},
		{"version", in.Version},
		{"approved_by_name", in.HistoricalApproverName},
		{"approved_at", meta.ApprovedAt},
		{"effective_date", meta.EffectiveDate},
	} {
		if strings.TrimSpace(req.val) == "" {
			return policy.ImportInput{}, fmt.Errorf("missing required front matter %q", req.key)
		}
	}
	for _, f := range []struct{ key, val string }{
		{"title", in.Title},
		{"approved_by_name", in.HistoricalApproverName},
		{"file name", name},
	} {
		if len(f.val) > policy.MaxImportFieldLen {
			return policy.ImportInput{}, fmt.Errorf("%s is longer than %d characters", f.key, policy.MaxImportFieldLen)
		}
	}
	if in.BodyMd == "" {
		return policy.ImportInput{}, errors.New("policy text (the Markdown after the front matter) is empty")
	}
	if in.HistoricalApprovedAt, err = parseDate("approved_at", meta.ApprovedAt); err != nil {
		return policy.ImportInput{}, err
	}
	if in.EffectiveDate, err = parseDate("effective_date", meta.EffectiveDate); err != nil {
		return policy.ImportInput{}, err
	}
	if s := strings.TrimSpace(meta.NextReviewAt); s != "" {
		t, err := parseDate("next_review_at", s)
		if err != nil {
			return policy.ImportInput{}, err
		}
		in.NextReviewAt = &t
	}
	if s := strings.TrimSpace(meta.SourceURL); s != "" {
		if err := CheckSourceURL(s); err != nil {
			return policy.ImportInput{}, err
		}
		in.SourceURL = s
	}
	return in, nil
}

// split separates the YAML front matter from the Markdown body. The file
// must open with a "---" line and close the block with another.
func split(content []byte) (fm []byte, body string, err error) {
	s := strings.ReplaceAll(string(content), "\r\n", "\n")
	s = strings.TrimPrefix(s, "\ufeff")
	if !strings.HasPrefix(s, "---\n") {
		return nil, "", errors.New("missing front matter: file must start with a '---' line")
	}
	// Search from the opening line's newline so an empty block
	// ("---\n---\n") closes too.
	rest := s[len("---"):]
	if end := strings.Index(rest, "\n---\n"); end >= 0 {
		return []byte(rest[:end]), rest[end+len("\n---\n"):], nil
	}
	if strings.HasSuffix(rest, "\n---") {
		return []byte(strings.TrimSuffix(rest, "\n---")), "", nil
	}
	return nil, "", errors.New("unterminated front matter: no closing '---' line")
}

func parseDate(key, s string) (time.Time, error) {
	t, err := time.Parse(dateLayout, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}, fmt.Errorf("%s %q is not a date (want YYYY-MM-DD)", key, s)
	}
	return t, nil
}

// CheckSourceURL admits only absolute http(s) URLs. The detail page
// renders source_url as a link, so a javascript: or data: URL must never
// get in.
func CheckSourceURL(s string) error {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("source_url %q must be an absolute http or https URL", s)
	}
	return nil
}

func orDefault(s string) string {
	if s = strings.TrimSpace(s); s == "" {
		return DefaultRole
	}
	return s
}

func trimAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
