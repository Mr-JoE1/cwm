package debugbundle

import (
	"regexp"
	"strings"
	"testing"
)

const hex64 = "3f9a0c1d2e4b5a6978877665544332211ffeeddccbbaa99887766554433221100"

// leakRe are the shapes no redacted text may contain.
var (
	anyHex64 = regexp.MustCompile(`(?i)[0-9a-f]{64}`)
	anyEmail = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+\.[A-Za-z]{2,}`)
)

func TestRedactSamples(t *testing.T) {
	posix := Redactor{Home: "/home/maher", User: "maher"}
	win := Redactor{Home: `C:\Users\Maher`, User: `CORP\Maher`}
	cases := []struct {
		name   string
		r      Redactor
		in     string
		want   []string // substrings that must be present
		absent []string // substrings that must be gone
	}{
		{
			name:   "bare 64-hex token",
			r:      posix,
			in:     "token loaded " + hex64 + " ok",
			want:   []string{"token loaded <redacted-hex> ok"},
			absent: []string{hex64},
		},
		{
			name:   "launch URL with ?t=",
			r:      posix,
			in:     "open http://127.0.0.1:47800/?t=" + hex64 + "&x=1 now",
			want:   []string{"http://127.0.0.1:47800/?t=<redacted>&x=1 now"},
			absent: []string{hex64},
		},
		{
			name:   "hex glued to letters still caught",
			r:      posix,
			in:     "cookie_val" + hex64 + "zz",
			absent: []string{hex64},
		},
		{
			name:   "quoted JSON values, mixed-case keys",
			r:      posix,
			in:     `{"Token": "abc\"def", "apiKey":"k-123", "PASSWORD" : "hunter2", "name":"work"}`,
			want:   []string{`"Token": "<redacted>"`, `"apiKey":"<redacted>"`, `"PASSWORD" : "<redacted>"`, `"name":"work"`},
			absent: []string{"abc", "k-123", "hunter2"},
		},
		{
			name:   "TOML, env and header forms",
			r:      posix,
			in:     "token = \"s3cr3t\"\nCWM_SECRET=xyz123\nX-Api-Key: qqq999\nclient_secret: 'single'\nlink_mode = \"auto\"",
			want:   []string{`token = "<redacted>"`, "CWM_SECRET=<redacted>", "X-Api-Key: <redacted>", "client_secret: '<redacted>'", `link_mode = "auto"`},
			absent: []string{"s3cr3t", "xyz123", "qqq999", "single"},
		},
		{
			name: "a key at end of line never eats the next line",
			r:    posix,
			in:   "apikey:\nnext line stays",
			want: []string{"apikey:\nnext line stays"},
		},
		{
			name:   "bearer header",
			r:      posix,
			in:     "Authorization: Bearer abcdefghijklmnop.qrs",
			absent: []string{"abcdefghijklmnop"},
		},
		{
			name:   "syncthing xml apikey",
			r:      posix,
			in:     "<gui><apikey>AbCdEf123456</apikey><Password>pw</Password></gui>",
			want:   []string{"<apikey><redacted></apikey>", "<Password><redacted></Password>"},
			absent: []string{"AbCdEf123456", ">pw<"},
		},
		{
			name: "multi-line PEM private key",
			r:    posix,
			in: "before\n-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMwAAAAtzc2gtZW\n" +
				"QyNTUxOQAAACDx\n-----END OPENSSH PRIVATE KEY-----\nafter",
			want:   []string{"before\n<redacted private key>\nafter"},
			absent: []string{"b3BlbnNzaC1rZXkt", "QyNTUxOQ"},
		},
		{
			name:   "PEM cut off by a log tail",
			r:      posix,
			in:     "x\n-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEA0Z3\nMORE",
			want:   []string{"x\n<redacted private key>"},
			absent: []string{"MIIEpAIBAAKCAQEA0Z3", "MORE"},
		},
		{
			name:   "JSON-escaped PEM on one line",
			r:      posix,
			in:     `{"k":"-----BEGIN EC PRIVATE KEY-----\nMHcCAQEEIBb\n-----END EC PRIVATE KEY-----\n"}`,
			absent: []string{"MHcCAQEEIBb"},
		},
		{
			name:   "emails anywhere",
			r:      posix,
			in:     "login: First.Last+cwm@example.co.uk, ssh git@github.com:org/repo",
			want:   []string{"<email>"},
			absent: []string{"First.Last", "example.co.uk"},
		},
		{
			name:   "posix home and other users",
			r:      posix,
			in:     "/home/maher/.cwm/logs/agent.log; /home/maherx/y; /home/bob/z; /Users/alice/Library; /home/maher",
			want:   []string{"~/.cwm/logs/agent.log", "/home/<user>/y", "/home/<user>/z", "/Users/<user>/Library", "; ~"},
			absent: []string{"maher", "bob", "alice"},
		},
		{
			name: "windows home in native, forward-slash, lower-case and JSON-escaped spellings",
			r:    win,
			in: `C:\Users\Maher\.cwm\config.toml | c:\users\maher\x | C:/Users/Maher/y | {"p":"C:\\Users\\Maher\\.local\\bin\\claude.exe"} | ` +
				`D:\Users\Bob\z | C:\Users\Public\Desktop | D:\work\Maher\repo`,
			want: []string{`~\.cwm\config.toml`, `~\x`, `~/y`, `"p":"~\\.local\\bin\\claude.exe"`, `D:\Users\<user>\z`,
				`C:\Users\Public\Desktop`, `D:\work\<user>\repo`},
			absent: []string{"Maher", "maher", "Bob"},
		},
		{
			name:   "known token shapes",
			r:      posix,
			in:     "sk-ant-api03-AAAAbbbbCCCC ghp_abcdefghijklmnopqrstuvwxyz0123",
			absent: []string{"AAAAbbbbCCCC", "ghp_abcdef"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.r.Redact(tc.in)
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q in\n%s", w, got)
				}
			}
			for _, a := range tc.absent {
				if strings.Contains(got, a) {
					t.Errorf("leaked %q in\n%s", a, got)
				}
			}
			if anyHex64.MatchString(got) || anyEmail.MatchString(got) {
				t.Errorf("hex64 or email survived:\n%s", got)
			}
			if again := tc.r.Redact(got); again != got {
				t.Errorf("redaction is not idempotent:\n%s\n--- second pass ---\n%s", got, again)
			}
		})
	}
}

func TestRedactShortUserLeavesProseAlone(t *testing.T) {
	r := Redactor{Home: "/home/dev", User: "dev"}
	got := r.Redact("developer devices: /srv/dev/x")
	if got != "developer devices: /srv/<user>/x" {
		t.Errorf("got %q", got)
	}
}

func TestRedactEmptyHome(t *testing.T) {
	r := Redactor{}
	if got := r.Redact("/ and C:\\ stay"); got != "/ and C:\\ stay" {
		t.Errorf("got %q", got)
	}
}
