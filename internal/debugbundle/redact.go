package debugbundle

import (
	"regexp"
	"sort"
	"strings"
)

// Redactor scrubs a debug bundle before anyone sees it (D52). It is
// applied to the whole text, after every section is rendered, so a
// section that forgets to be careful still cannot leak. The rules are
// deliberately greedy: a false positive costs one unreadable value in a
// bug report, a false negative publishes a secret in a public issue.
type Redactor struct {
	// Home is the user home dir; every occurrence becomes "~".
	Home string
	// User is the login name; it is replaced inside paths and inside
	// dash-encoded folder names.
	User string
	// Projects are the session folder names to show as numbered
	// aliases (<project-N>) wherever they appear (D99). Build fills it
	// unless the report is asked to show project names.
	Projects []Alias
}

// Alias is one name and what the report shows instead.
type Alias struct {
	Name  string
	Label string
}

// Replacement markers. They contain no characters the rules match, so
// redacting twice is a no-op.
const (
	redacted     = "<redacted>"
	redactedKey  = "<redacted private key>"
	redactedHex  = "<redacted-hex>"
	redactedMail = "<email>"
	userMarker   = "<user>"
)

var (
	// PEM private key blocks (RSA, EC, OPENSSH, ENCRYPTED, …), also when
	// the END line was cut off by a log tail, and also JSON-escaped on a
	// single line ("\n" inside a string).
	pemRe = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----(?s:.*?)(?:-----END [A-Z0-9 ]*PRIVATE KEY-----|\z)`)

	// key = value, "key": "value", key: value, KEY=value, where the key
	// names a secret. Mixed case, dashes and dots in the key are fine
	// (X-Api-Key, syncthing.apikey, CWM_TOKEN). The value is a quoted
	// string (escapes allowed) or a bare word. [ \t] — never \s — so a
	// key at the end of a line never swallows the next line.
	secretKV = regexp.MustCompile(`(?i)("?[A-Za-z0-9_.-]*(?:token|secret|passwd|password|pwd|apikey|api_key|api-key|private_key|key|authorization|cookie|credential|session_id|sessionid)[A-Za-z0-9_.-]*"?[ \t]*[:=][ \t]*)("(?:[^"\\\n]|\\.)*"|'[^'\n]*'|[^\s,;&}\])"'<>]+)`)

	// <apikey>…</apikey> and friends (syncthing config.xml).
	xmlSecretRe = regexp.MustCompile(`(?i)(<(?:apikey|password|token|secret)>)[^<]*(</)`)

	// Authorization: Bearer <x> / Basic <x>, wherever it appears.
	bearerRe = regexp.MustCompile(`(?i)\b(bearer|basic)[ \t]+[A-Za-z0-9._~+/=-]{8,}`)

	// URL query parameters that carry credentials (?t=<token> is the
	// dashboard's own launch link).
	queryRe = regexp.MustCompile(`(?i)([?&](?:t|token|key|apikey|api_key|sig|signature|code|access_token|auth)=)[^&\s"'<>]+`)

	// Well-known token shapes that may appear without a key.
	knownTokenRe = regexp.MustCompile(`\b(?:sk-ant-[A-Za-z0-9_-]{8,}|gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|xox[abprs]-[A-Za-z0-9-]{10,}|AKIA[0-9A-Z]{16})`)

	// 64+ hex characters: the dashboard token, its CSRF derivative,
	// session cookies, sha256 digests. Boundaries are "not hex", so a
	// token glued to letters (t=abc…) is still caught.
	hexRe = regexp.MustCompile(`(?i)(^|[^0-9a-f])[0-9a-f]{64,}`)

	emailRe = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}`)

	// Other users' homes: /home/<u>, /Users/<u>, C:\Users\<u> (also with
	// doubled JSON backslashes or forward slashes). Public / Default /
	// Shared are not people.
	posixHomeRe = regexp.MustCompile(`(/(?:home|Users)/)([^/\\\s"':;,<>)\]}]+)`)
	// Any drive, Users or Home (F:\Home\<u>\…: a profile moved off C:).
	winHomeRe = regexp.MustCompile(`(?i)([A-Z]:(?:\\\\|\\|/)(?:Users|Home)(?:\\\\|\\|/))([^/\\\s"':;,<>)\]}]+)`)
	// The same homes in Claude Code's dash-encoded folder names (every
	// character outside [A-Za-z0-9] becomes "-"): -home-<u>-…,
	// -Users-<u>-…, C--Users-<u>-…, F--Home-<u>-…. The name ends at the
	// next dash: a dot or dash inside a user name is a dash here too.
	encHomeRe = regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])((?:[A-Z]-)?-(?:home|users)-)([A-Za-z0-9_]+)`)
)

// notPeople are home-dir names that identify nobody.
var notPeople = map[string]bool{"public": true, "default": true, "shared": true, "all users": true, userMarker: true}

// Redact returns s with every rule applied.
func (r Redactor) Redact(s string) string {
	// Project aliases first: a folder name carries the user name, which
	// the later rules would change before the alias could match.
	s = r.redactProjects(s)
	s = pemRe.ReplaceAllString(s, redactedKey)
	s = xmlSecretRe.ReplaceAllString(s, "${1}"+redacted+"${2}")
	s = bearerRe.ReplaceAllString(s, "${1} "+redacted)
	s = queryRe.ReplaceAllString(s, "${1}"+redacted)
	s = secretKV.ReplaceAllStringFunc(s, func(m string) string {
		sub := secretKV.FindStringSubmatch(m)
		val := sub[2]
		if val == redacted || val == `"`+redacted+`"` || val == "'"+redacted+"'" ||
			val == redactedKey || strings.HasPrefix(val, "<redacted") {
			return m
		}
		switch {
		case strings.HasPrefix(val, `"`):
			return sub[1] + `"` + redacted + `"`
		case strings.HasPrefix(val, `'`):
			return sub[1] + "'" + redacted + "'"
		}
		return sub[1] + redacted
	})
	s = knownTokenRe.ReplaceAllString(s, redacted)
	s = hexRe.ReplaceAllString(s, "${1}"+redactedHex)
	s = emailRe.ReplaceAllString(s, redactedMail)
	s = r.redactHome(s)
	s = posixHomeRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := posixHomeRe.FindStringSubmatch(m)
		if notPeople[strings.ToLower(sub[2])] {
			return m
		}
		return sub[1] + userMarker
	})
	s = winHomeRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := winHomeRe.FindStringSubmatch(m)
		if notPeople[strings.ToLower(sub[2])] {
			return m
		}
		return sub[1] + userMarker
	})
	s = r.redactUser(s)
	// The known home and name first: the generic rule ends a name at the
	// first dash, which would leave the rest of "j-doe" behind.
	s = r.redactEncodedUser(s)
	s = encHomeRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := encHomeRe.FindStringSubmatch(m)
		if notPeople[strings.ToLower(sub[3])] {
			return m
		}
		return sub[1] + sub[2] + userMarker
	})
	return s
}

// encodeName is Claude Code's folder-name encoding of a path or name
// (pathmirror.FolderName without the long-name hash).
func encodeName(s string) string {
	var b strings.Builder
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			b.WriteRune(c)
		case c > 0xFFFF:
			b.WriteString("--") // two UTF-16 units
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// redactEncodedUser replaces the home dir and the login name inside
// dash-encoded folder names (D99): "-home-maher-projects-x" and
// "F--Home-Maher-Projects-x". The name must sit between dashes (or a
// dash and the end of the word), so free text keeps short words.
func (r Redactor) redactEncodedUser(s string) string {
	if home := strings.TrimRight(r.Home, `/\`); len(home) >= 2 {
		// -home-maher -> -home-<user>: the parent stays readable.
		parent := home[:strings.LastIndexAny(home, `/\`)+1]
		re := regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])` + regexp.QuoteMeta(encodeName(home)) + `([^A-Za-z0-9]|$)`)
		s = re.ReplaceAllString(s, "${1}"+encodeName(parent)+userMarker+"${2}")
	}
	u := r.User
	if i := strings.LastIndexAny(u, `\/`); i >= 0 {
		u = u[i+1:]
	}
	if len(u) < 2 || notPeople[strings.ToLower(u)] {
		return s
	}
	re := regexp.MustCompile(`(?i)(-)` + regexp.QuoteMeta(encodeName(u)) + `(-|[^A-Za-z0-9<]|$)`)
	for range 2 {
		s = re.ReplaceAllString(s, "${1}"+userMarker+"${2}")
	}
	return s
}

// redactProjects swaps every project folder name for its alias, longest
// name first (one name can contain another), case-insensitively (Windows
// and macOS folder names are) and only as a whole word, so a short name
// cannot eat part of a longer one.
func (r Redactor) redactProjects(s string) string {
	list := append([]Alias(nil), r.Projects...)
	sort.SliceStable(list, func(i, j int) bool { return len(list[i].Name) > len(list[j].Name) })
	for _, a := range list {
		if len(a.Name) < minAliasName {
			continue
		}
		re := regexp.MustCompile(`(?i)(^|[^A-Za-z0-9_.-])` + regexp.QuoteMeta(a.Name) + `([^A-Za-z0-9_.-]|$)`)
		// Matches can share a separator; two passes cover that.
		for range 2 {
			s = re.ReplaceAllString(s, "${1}"+a.Label+"${2}")
		}
	}
	return s
}

// minAliasName keeps very short folder names (an isolated-mode project
// called "x") from turning every "x" in the report into an alias; the
// workspace section lists them by alias anyway.
const minAliasName = 4

// redactHome replaces the home dir in its native, forward-slash and
// JSON-escaped spellings, case-insensitively (Windows and macOS paths
// are), and only at a path boundary (/home/al must not eat /home/alice).
func (r Redactor) redactHome(s string) string {
	home := strings.TrimRight(r.Home, `/\`)
	if len(home) < 2 {
		return s
	}
	variants := map[string]bool{
		home:                                true,
		strings.ReplaceAll(home, `\`, `/`):  true,
		strings.ReplaceAll(home, `\`, `\\`): true,
		strings.ReplaceAll(home, `/`, `\`):  true,
		strings.ReplaceAll(home, `/`, `\\`): true,
	}
	for v := range variants {
		re := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(v) + `([/\\\s"':;,<>)\]}]|$)`)
		s = re.ReplaceAllString(s, "~${1}")
	}
	return s
}

// redactUser replaces the login name where it is a whole path element
// (C:\work\maher\x, /srv/maher/…); free text is left alone so a short
// name such as "dev" cannot shred the report.
func (r Redactor) redactUser(s string) string {
	u := r.User
	if i := strings.LastIndexAny(u, `\/`); i >= 0 {
		u = u[i+1:] // DOMAIN\user
	}
	if len(u) < 2 || notPeople[strings.ToLower(u)] {
		return s
	}
	re := regexp.MustCompile(`(?i)([/\\])` + regexp.QuoteMeta(u) + `([/\\\s"':;,<>)\]}]|$)`)
	// Matches can share a separator (/u/u/); two passes cover that.
	for range 2 {
		s = re.ReplaceAllString(s, "${1}"+userMarker+"${2}")
	}
	return s
}
