package wip

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// Stranded is a WIP commit that cwm ≤ 0.9.1 left on a user's branch: the
// old SessionEnd committed onto the current branch (D119).
type Stranded struct {
	Dir    string
	Branch string // "" = a detached HEAD
}

// strandedProbe bounds one repo's probe; doctor stays fast.
const strandedProbe = 2 * time.Second

// FindStranded reports each dir whose HEAD is a `wip: <host> …` commit
// made with cwm's commit identity. One git call per dir; a dir that is
// not a repo, or does not answer in time, is skipped.
func FindStranded(dirs []string, host string) []Stranded {
	var out []Stranded
	seen := map[string]bool{}
	for _, d := range dirs {
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		ctx, cancel := context.WithTimeout(context.Background(), strandedProbe)
		cmd := exec.CommandContext(ctx, "git", "--no-optional-locks", "log", "-1", "--format=%an%x00%ae%x00%s%x00%D", "HEAD")
		cmd.Dir = d
		b, err := cmd.Output()
		cancel()
		if err != nil {
			continue
		}
		f := strings.SplitN(strings.TrimRight(string(b), "\r\n"), "\x00", 4)
		if len(f) != 4 || f[0] != commitName || f[1] != commitEmail || !strings.HasPrefix(f[2], "wip: "+host+" ") {
			continue
		}
		s := Stranded{Dir: d}
		for ref := range strings.SplitSeq(f[3], ", ") {
			if b, ok := strings.CutPrefix(ref, "HEAD -> "); ok {
				s.Branch = b
			}
		}
		out = append(out, s)
	}
	return out
}
