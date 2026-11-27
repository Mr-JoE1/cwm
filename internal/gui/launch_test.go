package gui

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/claudebin"
	"github.com/Mr-JoE1/cwm/internal/claudeopen"
)

// launchFixture: capture mode, one session, every option detected as the
// test says, background runs recorded.
func launchFixture(t *testing.T, choices []claudeopen.Choice) (*fixture, *fakeLauncher, *[][]string) {
	t.Helper()
	f, l := actionFixture(t, okSync)
	writeFile(t, filepath.Join(f.root, "config.toml"), "mode = \"capture\"\n")
	writeFile(t, filepath.Join(f.root, "workspace", "projects", "-home-a-api", "44444444-dddd.jsonl"), "{}\n")
	var bg [][]string
	f.srv.opts.Launchers = func() []claudeopen.Choice { return choices }
	f.srv.opts.Background = func(argv []string) (string, error) {
		bg = append(bg, argv)
		return "opened /home/a/api in VS Code\n", nil
	}
	return f, l, &bg
}

var allChoices = []claudeopen.Choice{
	{ID: "auto", Label: "Auto", Available: true, Resume: true},
	{ID: "cli", Label: "Claude Code CLI", Reason: claudeopen.ReasonOnlyBundled},
	{ID: "vscode", Label: "VS Code", Available: true, Resume: true, Editor: "VS Code"},
	{ID: "desktop", Label: "Claude desktop app", Available: true, Note: claudeopen.NoteDesktopFolder},
}

func TestLaunchWithValidation(t *testing.T) {
	f, l, bg := launchFixture(t, allChoices)
	for _, body := range []string{
		`{"project":"-home-a-api","id":"44444444-dddd","with":"bogus"}`,
		`{"project":"-home-a-api","id":"44444444-dddd","with":"cli; rm -rf /"}`,
		`{"project":"-home-a-api","id":"44444444-dddd","with":"VSCODE"}`,
		`{"project":"-home-a-api","id":"44444444-dddd","with":7}`,
	} {
		if rec := f.post(t, "/api/sessions/resume", body, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", body, rec.Code)
		}
	}
	if rec := f.post(t, "/api/accounts/run", `{"name":"x","with":"code"}`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("run bad with = %d, want 400", rec.Code)
	}
	if len(l.calls) != 0 || len(*bg) != 0 {
		t.Errorf("a rejected request launched something: %v %v", l.calls, *bg)
	}
}

func TestLaunchWithUnavailableIs424(t *testing.T) {
	f, l, _ := launchFixture(t, allChoices)
	rec := f.post(t, "/api/sessions/resume", `{"project":"-home-a-api","id":"44444444-dddd","with":"cli"}`, nil)
	var e apiError
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	if rec.Code != http.StatusFailedDependency || !strings.Contains(e.Error, claudeopen.ReasonOnlyBundled) || e.Command == "" {
		t.Fatalf("cli = %d %+v", rec.Code, e)
	}
	if len(l.calls) != 0 {
		t.Errorf("launched: %v", l.calls)
	}
}

func TestLaunchWithAppsRunInBackground(t *testing.T) {
	f, l, bg := launchFixture(t, allChoices)
	rec := f.post(t, "/api/sessions/resume", `{"project":"-home-a-api","id":"44444444-dddd","with":"vscode"}`, nil)
	var lr launchResult
	_ = json.Unmarshal(rec.Body.Bytes(), &lr)
	if rec.Code != 200 || lr.Opened != "VS Code" || lr.Command != "claude --resume 44444444-dddd" || !strings.Contains(lr.Note, "VS Code") {
		t.Fatalf("vscode = %d %s", rec.Code, rec.Body.String())
	}
	want := fakeCwm + "|sessions|resume|44444444-dddd|--with|vscode"
	if len(*bg) != 1 || strings.Join((*bg)[0], "|") != want || len(l.calls) != 0 {
		t.Fatalf("background = %q (terminal %q), want %s", *bg, l.calls, want)
	}
	// The setting applies without an override; the desktop app says it
	// only opens the folder.
	writeFile(t, filepath.Join(f.root, "config.toml"), "mode = \"capture\"\n[launch]\nclaude = \"desktop\"\n")
	rec = f.post(t, "/api/sessions/resume", `{"project":"-home-a-api","id":"44444444-dddd"}`, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &lr)
	if rec.Code != 200 || lr.Note != claudeopen.NoteDesktopFolder || !strings.HasSuffix(strings.Join((*bg)[1], "|"), "--with|desktop") {
		t.Fatalf("desktop = %d %s %q", rec.Code, rec.Body.String(), *bg)
	}
	// An override of auto beats the setting, in a terminal.
	rec = f.post(t, "/api/sessions/resume", `{"project":"-home-a-api","id":"44444444-dddd","with":"auto"}`, nil)
	if rec.Code != 200 || len(l.calls) != 1 || !strings.HasSuffix(strings.Join(l.calls[0], "|"), "|--with|auto") {
		t.Fatalf("auto override = %d %q", rec.Code, l.calls)
	}
	// Start claude as an account in VS Code: a background `cwm run`.
	writeFile(t, filepath.Join(f.root, "config.toml"), "mode = \"capture\"\ndefault_account = \"home\"\n[[account]]\nname = \"home\"\n")
	rec = f.post(t, "/api/accounts/run", `{"name":"home","with":"vscode"}`, nil)
	if rec.Code != 200 || len(*bg) != 3 || strings.Join((*bg)[2], "|") != fakeCwm+"|run|--account|home|--with|vscode" {
		t.Fatalf("run vscode = %d %s %q", rec.Code, rec.Body.String(), *bg)
	}
	// A failing background run is a 500 carrying the copyable fallback.
	f.srv.opts.Background = func([]string) (string, error) { return "", errors.New("code not found") }
	rec = f.post(t, "/api/sessions/resume", `{"project":"-home-a-api","id":"44444444-dddd","with":"vscode"}`, nil)
	var e apiError
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	if rec.Code != 500 || !strings.Contains(e.Error, "code not found") || e.Command != "claude --resume 44444444-dddd" {
		t.Fatalf("failure = %d %+v", rec.Code, e)
	}
}

func TestLaunchersEndpoint(t *testing.T) {
	f, _, _ := launchFixture(t, allChoices)
	var v launchersView
	getJSON(t, f, "/api/launchers", 200, &v)
	if v.Setting != "auto" || len(v.Choices) != 4 || v.Choices[1].Reason != claudeopen.ReasonOnlyBundled || v.Install != claudebin.InstallCommand(runtime.GOOS) {
		t.Fatalf("launchers = %+v", v)
	}
}
