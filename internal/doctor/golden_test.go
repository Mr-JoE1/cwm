package doctor

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/syncthing"
)

// -update rewrites testdata/golden/*.txt. The committed goldens were
// captured from the pre-V0.5 doctor (before Run was refactored onto the
// []Check slice), so these tests pin the text renderer byte-for-byte.
var updateGolden = flag.Bool("update", false, "rewrite doctor golden files")

type goldenCase struct {
	name  string
	net   bool
	setup func(t *testing.T) (root string, repl map[string]string)
}

// unreachableSyncthing pins check 12 to a deterministic refusal.
func unreachableSyncthing(t *testing.T) {
	t.Helper()
	t.Setenv(syncthing.EnvURL, "http://127.0.0.1:1")
	t.Setenv(syncthing.EnvAPIKey, "test")
}

func goldenCases() []goldenCase {
	healthy := func(t *testing.T) (string, map[string]string) {
		root := healthyWorkspace(t)
		unreachableSyncthing(t)
		sshStub(t, "Hi fake! You've successfully authenticated, but GitHub does not provide shell access.")
		return root, nil
	}
	return []goldenCase{
		{name: "healthy", setup: healthy},
		{name: "healthy_net_ok", net: true, setup: healthy},
		{name: "net_fail", net: true, setup: func(t *testing.T) (string, map[string]string) {
			root := healthyWorkspace(t)
			unreachableSyncthing(t)
			sshStub(t, "fake@example.com: Permission denied (publickey).")
			return root, nil
		}},
		{name: "missing_workspace", setup: func(t *testing.T) (string, map[string]string) {
			root := t.TempDir()
			t.Setenv("CWM_HOME", root)
			t.Setenv("HOME", t.TempDir())
			t.Setenv("PATH", t.TempDir())
			unreachableSyncthing(t)
			return root, nil
		}},
		{name: "settings_keyless_missing_hook", setup: func(t *testing.T) (string, map[string]string) {
			root := healthyWorkspace(t)
			unreachableSyncthing(t)
			writeFile(t, filepath.Join(root, "workspace", "settings.json"),
				`{"env":{},"x":"/nonexistent/cwm hook session-start"}`)
			return root, nil
		}},
		{name: "hook_not_executable_legacy_malformed", setup: func(t *testing.T) (string, map[string]string) {
			root := healthyWorkspace(t)
			unreachableSyncthing(t)
			bin := filepath.Join(t.TempDir(), "cwm-notexec")
			writeFile(t, bin, "not executable\n")
			writeFile(t, filepath.Join(root, "workspace", "settings.json"),
				fmt.Sprintf(`{"hooks":{"SessionStart":[{"hooks":[{"command":"%s hook session-start"}]}]}}`, bin))
			writeFile(t, filepath.Join(root, "workspace", "projects", "-Users-x-legacy", "s.jsonl"), "{}\n")
			writeFile(t, filepath.Join(root, "workspace", "repos.tsv"), "alpha\thttps://x/alpha\nbadline\n")
			stubBin(t, map[string]string{"claude": "#!/bin/sh\necho '2.1.100 (Claude Code)'\n"})
			return root, map[string]string{bin: "<NOEXEC>"}
		}},
		{name: "syncthing_ok", setup: func(t *testing.T) (string, map[string]string) {
			root := healthyWorkspace(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, `{"ping":"pong"}`)
			}))
			t.Cleanup(srv.Close)
			t.Setenv(syncthing.EnvURL, srv.URL)
			t.Setenv(syncthing.EnvAPIKey, "test")
			return root, map[string]string{srv.URL: "<SYNCTHING>"}
		}},
	}
}

func TestDoctorGolden(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX PATH stub — behavioral coverage on linux CI (see docs/PLAN.md D22)")
	}
	for _, tc := range goldenCases() {
		t.Run(tc.name, func(t *testing.T) {
			root, repl := tc.setup(t)
			var out strings.Builder
			Run(&out, root, tc.net)
			got := out.String()
			for from, to := range repl {
				got = strings.ReplaceAll(got, from, to)
			}
			got = strings.ReplaceAll(got, root, "<ROOT>")
			path := filepath.Join("testdata", "golden", tc.name+".txt")
			if *updateGolden {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden %s: %v", path, err)
			}
			if got != string(want) {
				t.Errorf("doctor output drifted from golden %s\n--- got ---\n%s--- want ---\n%s", path, got, want)
			}
		})
	}
}
