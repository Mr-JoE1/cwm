package debugbundle

import (
	"regexp"
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
	// User is the login name; it is replaced inside paths.
	User string
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
	winHomeRe   = regexp.MustCompile(`(?i)([A-Z]:(?:\\\\|\\|/)Users(?:\\\\|\\|/))([^/\\\s"':;,<>)\]}]+)`)
)

// notPeople are home-dir names that identify nobody.
var notPeople = map[string]bool{"public": true, "default": true, "shared": true, "all users": true, userMarker: true}

// Redact returns s with every rule applied.
func (r Redactor) Redact(s string) string {
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
	return s
}

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
