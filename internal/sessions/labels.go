package sessions

import (
	"regexp"
	"strings"
)

// Claude Code records a slash command as XML-ish envelopes in the user
// turn (<command-message>, <command-name>, <command-args>, …). Shown raw
// they bury the prompt (v0.9 B1), so lists show "/name args" instead.
var (
	envelopeRe = regexp.MustCompile(`(?s)<(command-message|command-name|command-args|command-contents|skill-format|local-command-stdout|local-command-stderr|local-command-caveat|system-reminder)>.*?</(command-message|command-name|command-args|command-contents|skill-format|local-command-stdout|local-command-stderr|local-command-caveat|system-reminder)>`)
	cmdNameRe  = regexp.MustCompile(`<command-name>\s*/?([^<\s]+)\s*</command-name>`)
	cmdMsgRe   = regexp.MustCompile(`<command-message>\s*([^<\s]+)\s*</command-message>`)
	cmdArgsRe  = regexp.MustCompile(`(?s)<command-args>(.*?)</command-args>`)
)

// CleanPrompt turns a raw first prompt into list text: command envelopes
// become "/name args", other envelopes are dropped, whitespace collapses.
// "" means nothing human was left.
func CleanPrompt(s string) string {
	if !strings.Contains(s, "<") {
		return collapseSpaces(s)
	}
	name := ""
	if m := cmdNameRe.FindStringSubmatch(s); m != nil {
		name = m[1]
	} else if m := cmdMsgRe.FindStringSubmatch(s); m != nil {
		name = m[1]
	}
	args := ""
	if m := cmdArgsRe.FindStringSubmatch(s); m != nil {
		args = collapseSpaces(m[1])
	}
	rest := collapseSpaces(envelopeRe.ReplaceAllString(s, " "))
	if name == "" {
		return rest
	}
	return strings.TrimSpace("/" + name + " " + strings.TrimSpace(args+" "+rest))
}

var (
	oneDriveRe = regexp.MustCompile(`^[a-zA-Z]--Users-[^-]+-OneDrive---?(.+)$`)
	wslRe      = regexp.MustCompile(`^--wsl(?:-localhost|\$)?-[^-]+-(?:home-[^-]+-)?(.+)$`)
	winHomeRe  = regexp.MustCompile(`^[a-zA-Z]--Users-[^-]+-(.+)$`)
	unixHomeRe = regexp.MustCompile(`^-(?:home|Users)-[^-]+-(.+)$`)
)

// ProjectLabel shortens a project folder name for lists (v0.9 B2). A
// folder Claude Code named after a cwd has every separator as "-"; the
// user's home prefix (any user name) and the OneDrive and WSL roots are
// shortened: c--Users-<u>-OneDrive---X → …/X, --wsl-localhost-<distro>-
// home-<u>-X → WSL/X, the home folder → ~/X. Names cwm pinned (plain repo
// names) are returned as they are; callers keep the full name for
// commands and tooltips.
func ProjectLabel(slug string) string {
	if m := oneDriveRe.FindStringSubmatch(slug); m != nil {
		return "…/" + m[1]
	}
	if m := wslRe.FindStringSubmatch(slug); m != nil {
		return "WSL/" + m[1]
	}
	if m := winHomeRe.FindStringSubmatch(slug); m != nil {
		return "~/" + m[1]
	}
	if m := unixHomeRe.FindStringSubmatch(slug); m != nil {
		return "~/" + m[1]
	}
	return slug
}

// ListPrompt is the text a session list shows: the first prompt, else the
// summary, else "(no prompt)".
func ListPrompt(s Session) string {
	if s.FirstPrompt != "" && s.FirstPrompt != noPrompt {
		return s.FirstPrompt
	}
	if s.Summary != "" {
		return s.Summary
	}
	return noPrompt
}
