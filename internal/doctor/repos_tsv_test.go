package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// D66: a fresh workspace has no repos.tsv and no session folders; only a
// missing manifest next to existing project dirs is worth a warning.
func TestReposTSVMissing(t *testing.T) {
	for _, tt := range []struct {
		name   string
		setup  func(t *testing.T, ws string)
		status Status
		want   string
	}{
		{"no projects dir", func(t *testing.T, ws string) {}, StatusOK, "created on first launch"},
		{"empty projects dir", func(t *testing.T, ws string) {
			if err := os.MkdirAll(filepath.Join(ws, "projects"), 0o755); err != nil {
				t.Fatal(err)
			}
		}, StatusOK, "created on first launch"},
		{"projects exist", func(t *testing.T, ws string) {
			writeFile(t, filepath.Join(ws, "projects", "alpha", "s.jsonl"), "{}\n")
		}, StatusWarn, "repos.tsv not readable"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			noClaude(t)
			unreachableSyncthing(t)
			root := t.TempDir()
			t.Setenv("CWM_HOME", root)
			ws := filepath.Join(root, "workspace")
			writeFile(t, filepath.Join(ws, ".stignore"), "x\n")
			tt.setup(t, ws)
			var got *Check
			for _, c := range Checks(root, false) {
				if c.Name == "repos-tsv" {
					got = &c
				}
			}
			if got == nil {
				t.Fatal("no repos-tsv check")
			}
			if got.Status != tt.status || !strings.Contains(got.Detail, tt.want) {
				t.Errorf("repos-tsv = %+v, want %s %q", *got, tt.status, tt.want)
			}
		})
	}
}
