package desktopapp

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFileLocked: a file another handle holds open reads as locked (the
// running app's cookie store), and not once it is closed.
func TestFileLocked(t *testing.T) {
	p := filepath.Join(t.TempDir(), "Cookies")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if fileLocked(p) {
		t.Fatal("closed file reads as locked")
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if !fileLocked(p) {
		t.Fatal("open file reads as not locked")
	}
	f.Close()
	if fileLocked(p) {
		t.Fatal("file still locked after close")
	}
}
