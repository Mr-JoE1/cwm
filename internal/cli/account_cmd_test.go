package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestConfirmRemoveNonTTYRequiresYes(t *testing.T) {
	e := &Env{Stdout: &bytes.Buffer{}, Stdin: strings.NewReader("")}
	_, err := confirmRemove(e, func() bool { return false })
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("err = %v, want --yes requirement", err)
	}
}

func TestConfirmRemovePrompt(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want bool
	}{
		{"y\n", true},
		{"YES\n", true},
		{"n\n", false},
		{"\n", false},
	} {
		var out bytes.Buffer
		e := &Env{Stdout: &out, Stdin: strings.NewReader(tt.in)}
		got, err := confirmRemove(e, func() bool { return true })
		if err != nil {
			t.Fatalf("input %q: %v", tt.in, err)
		}
		if got != tt.want {
			t.Errorf("input %q: confirmed = %v, want %v", tt.in, got, tt.want)
		}
		if !strings.Contains(out.String(), "[y/N]") {
			t.Errorf("input %q: missing prompt: %q", tt.in, out.String())
		}
	}
}

func TestAccountUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"no action", []string{"account"}, "usage: cwm account"},
		{"unknown action", []string{"account", "explode"}, "unknown account action"},
		{"add no name", []string{"account", "add"}, "usage: cwm account add"},
		{"use no name", []string{"account", "use"}, "usage: cwm account use"},
		{"remove no name", []string{"account", "remove"}, "usage: cwm account remove"},
		{"add bad name", []string{"account", "add", "Bad!Name"}, "invalid account name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CWM_HOME", t.TempDir())
			var out, errOut bytes.Buffer
			e := &Env{Stdout: &out, Stderr: &errOut, Stdin: strings.NewReader("")}
			code := e.Run(tt.args)
			if code != ExitError {
				t.Fatalf("code = %d, want %d (stderr: %s)", code, ExitError, errOut.String())
			}
			if !strings.Contains(errOut.String(), tt.want) {
				t.Errorf("stderr = %q, want it to contain %q", errOut.String(), tt.want)
			}
		})
	}
}

func TestAccountListEmpty(t *testing.T) {
	t.Setenv("CWM_HOME", t.TempDir())
	var out, errOut bytes.Buffer
	e := &Env{Stdout: &out, Stderr: &errOut, Stdin: strings.NewReader("")}
	if code := e.Run([]string{"account", "list"}); code != ExitOK {
		t.Fatalf("code = %d, stderr: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "default*") || !strings.Contains(out.String(), "last-login") {
		t.Errorf("output = %q, want table header", out.String())
	}
}

func TestAccountListJSONEmpty(t *testing.T) {
	t.Setenv("CWM_HOME", t.TempDir())
	var out, errOut bytes.Buffer
	e := &Env{Stdout: &out, Stderr: &errOut, Stdin: strings.NewReader("")}
	if code := e.Run([]string{"account", "list", "--json"}); code != ExitOK {
		t.Fatalf("code = %d, stderr: %s", code, errOut.String())
	}
	if strings.TrimSpace(out.String()) != "[]" {
		t.Errorf("output = %q, want []", out.String())
	}
}

func TestAccountRemoveDryRun(t *testing.T) {
	t.Setenv("CWM_HOME", t.TempDir())
	var out, errOut bytes.Buffer
	e := &Env{Stdout: &out, Stderr: &errOut, Stdin: strings.NewReader("")}
	code := e.Run([]string{"account", "remove", "main", "--dry-run"})
	if code != ExitOK {
		t.Fatalf("code = %d, stderr: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "would") {
		t.Errorf("output = %q, want a plan", out.String())
	}
}

func TestInitMigrateNotImplemented(t *testing.T) {
	t.Setenv("CWM_HOME", t.TempDir())
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	var out, errOut bytes.Buffer
	e := &Env{Stdout: &out, Stderr: &errOut, Stdin: strings.NewReader("")}
	code := e.Run([]string{"init", "--migrate"})
	if code != ExitError {
		t.Fatalf("code = %d, want %d", code, ExitError)
	}
	if !strings.Contains(errOut.String(), "v0.2-C2") {
		t.Errorf("stderr = %q, want a v0.2-C2 hint", errOut.String())
	}
}

func TestInitDryRunViaCLI(t *testing.T) {
	t.Setenv("CWM_HOME", t.TempDir())
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, errOut bytes.Buffer
	e := &Env{Stdout: &out, Stderr: &errOut, Stdin: strings.NewReader("")}
	code := e.Run([]string{"init", "--account", "main", "--dry-run"})
	if code != ExitOK {
		t.Fatalf("code = %d, stderr: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "would") {
		t.Errorf("output = %q, want a plan", out.String())
	}
}
