package claudebin

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Bundled copies (D98). The Claude desktop app and the Claude Code editor
// extension each ship their own Claude Code binary and neither puts it on
// PATH, so a machine with only those installed has a working claude that
// no PATH lookup finds (GitHub issue #1). Their folders carry a version
// that changes on every app update, so a bundled hit is resolved afresh
// each time and never pinned into config.toml.
//
// Layouts (publicly reported; the probe decides, never the location):
//   - desktop app, Windows MSIX: %APPDATA%\Claude\claude-code\<ver>\claude.exe
//     (also under the MSIX package's virtualised LocalCache\Roaming, where
//     the file really lives when the app runs virtualised); D103 also
//     looks one and two levels below <ver>;
//   - desktop app, legacy Squirrel: %LOCALAPPDATA%\AnthropicClaude\app-<ver>\…\claude.exe,
//     except app-<ver>\claude.exe itself, which is the desktop app;
//   - desktop app, macOS: ~/Library/Application Support/Claude/claude-code/<ver>/claude.app/Contents/MacOS/claude
//     (D103; the Linux builds use ~/.config/Claude/claude-code with the
//     same app bundle);
//   - editor extension: <ext dir>/anthropic.claude-code-<ver>[-<platform>]/resources/native-binary/claude(.exe)
//     for VS Code, Insiders, Cursor, Windsurf, VSCodium and VS Code Server.

// bundlePerFamily caps the probes per family: the highest version first,
// one older copy in case the newest is half-updated. A cold start with
// nothing installed probes nothing (the globs match no file).
const bundlePerFamily = 2

// editorDirs are the extension folders, relative to the home dir, with
// the editor name reports show.
var editorDirs = []struct{ dir, name string }{
	{".vscode", "VS Code"},
	{".vscode-insiders", "VS Code Insiders"},
	{".cursor", "Cursor"},
	{".windsurf", "Windsurf"},
	{".vscode-oss", "VSCodium"},
	{".vscode-server", "VS Code Server"},
	{".vscode-server-insiders", "VS Code Server Insiders"},
}

// bundleFamily is one set of versioned folders that ship the same thing.
type bundleFamily struct {
	src      Source
	patterns []string
	// version extracts the version segment of a match ("" = not one).
	version func(match string) string
}

// bundleFamilies lists the families for env.GOOS in lookup order: the
// desktop app before the editor extensions (D98).
func bundleFamilies(env Env) []bundleFamily {
	exe := "claude"
	if env.GOOS == "windows" {
		exe = "claude.exe"
	}
	home := env.Home
	if env.GOOS == "windows" {
		if p := env.Getenv("USERPROFILE"); p != "" {
			home = p
		}
	}
	var fams []bundleFamily
	switch env.GOOS {
	case "windows":
		var pats []string
		for _, base := range desktopBases(env) {
			pats = append(pats, desktopPatterns(base, exe)...)
		}
		local := env.Getenv("LOCALAPPDATA")
		if len(pats) > 0 {
			fams = append(fams, bundleFamily{src: SourceDesktop, patterns: pats, version: claudeCodeVer})
		}
		if local != "" {
			// Squirrel nests the CLI an unknown depth below app-<ver>;
			// up to three levels cover resources\…\claude.exe without a
			// walk. app-<ver>\claude.exe itself is the desktop app's own
			// exe (starting it opens the app), so depth 0 is never probed.
			base := filepath.Join(local, "AnthropicClaude")
			var sq []string
			for _, mid := range []string{"*", filepath.Join("*", "*"), filepath.Join("*", "*", "*")} {
				sq = append(sq, filepath.Join(base, "app-*", mid, exe))
			}
			fams = append(fams, bundleFamily{src: SourceDesktop, patterns: sq,
				version: func(m string) string {
					rel, err := filepath.Rel(base, m)
					if err != nil {
						return ""
					}
					first := strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]
					return strings.TrimPrefix(first, "app-")
				}})
		}
	case "darwin", "linux":
		for _, base := range desktopBases(env) {
			fams = append(fams, bundleFamily{src: SourceDesktop, version: claudeCodeVer, patterns: desktopPatterns(base, exe)})
		}
	}
	if home != "" {
		var pats []string
		for _, e := range editorDirs {
			pats = append(pats, filepath.Join(home, e.dir, "extensions", "anthropic.claude-code-*", "resources", "native-binary", exe))
		}
		fams = append(fams, bundleFamily{src: SourceExtension, patterns: pats, version: func(m string) string {
			// …/anthropic.claude-code-<ver>[-<platform>]/resources/native-binary/claude
			dir := filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(m))))
			return strings.TrimPrefix(dir, "anthropic.claude-code-")
		}})
	}
	return fams
}

// desktopBases are the desktop app's claude-code folders (one per
// <version> subfolder) for env.GOOS; a base may hold a glob (the MSIX
// package folder).
func desktopBases(env Env) []string {
	var out []string
	switch env.GOOS {
	case "windows":
		if roaming := env.Getenv("APPDATA"); roaming != "" {
			out = append(out, filepath.Join(roaming, "Claude", "claude-code"))
		}
		if local := env.Getenv("LOCALAPPDATA"); local != "" {
			out = append(out, filepath.Join(local, "Packages", "*Claude*", "LocalCache", "Roaming", "Claude", "claude-code"))
		}
	case "darwin":
		if env.Home != "" {
			out = append(out, filepath.Join(env.Home, "Library", "Application Support", "Claude", "claude-code"))
		}
	case "linux":
		if env.Home != "" {
			out = append(out, filepath.Join(env.Home, ".config", "Claude", "claude-code"))
		}
	}
	return out
}

// desktopPatterns look for the executable in <base>/<ver>/ and up to two
// levels below it (<ver>/bin/claude.exe, <ver>/<x>/<y>/claude.exe, D103):
// the layout inside <ver> is not documented and has changed before. Three
// fixed-depth globs, never a walk, so a lookup stays fast.
// On macOS (and the Linux builds) the version folder holds an app bundle:
// <ver>/claude.app/Contents/MacOS/claude.
func desktopPatterns(base, exe string) []string {
	pats := []string{
		filepath.Join(base, "*", exe),
		filepath.Join(base, "*", "*", exe),
		filepath.Join(base, "*", "*", "*", exe),
	}
	if exe == "claude" {
		pats = append(pats, filepath.Join(base, "*", "claude.app", "Contents", "MacOS", exe))
	}
	return pats
}

// claudeCodeVer is the path segment after "claude-code" (the version
// folder of a desktop-app copy).
func claudeCodeVer(m string) string {
	parts := strings.Split(filepath.ToSlash(m), "/")
	for i := len(parts) - 2; i >= 0; i-- {
		if strings.EqualFold(parts[i], "claude-code") {
			return parts[i+1]
		}
	}
	return ""
}

// bundledCandidates globs every family and returns, per family, the
// matches ordered highest version first, capped at bundlePerFamily. The
// result is tiered (D103): the newest copy of every family first, then
// the second newest of every family, so no probe cap can drop a whole
// family.
func bundledCandidates(env Env) []sourced {
	if env.Glob == nil {
		return nil
	}
	var perFam [][]sourced
	for _, f := range bundleFamilies(env) {
		var matches []string
		seen := map[string]bool{}
		for _, pat := range f.patterns {
			m, err := env.Glob(pat)
			if err != nil {
				continue
			}
			for _, p := range m {
				if !seen[p] {
					seen[p] = true
					matches = append(matches, p)
				}
			}
		}
		ver := map[string]bundleVersion{}
		for _, m := range matches {
			ver[m] = parseBundleVersion(f.version(m))
		}
		// Highest version first; within one version the shallowest path
		// (<ver>/claude.exe before <ver>/bin/claude.exe).
		sort.SliceStable(matches, func(i, j int) bool {
			a, b := ver[matches[i]], ver[matches[j]]
			if a.newer(b) || b.newer(a) {
				return a.newer(b)
			}
			return strings.Count(matches[i], string(filepath.Separator)) < strings.Count(matches[j], string(filepath.Separator))
		})
		var fam []sourced
		for i, m := range matches {
			if i == bundlePerFamily {
				break
			}
			fam = append(fam, sourced{path: m, src: f.src, tier: i})
		}
		perFam = append(perFam, fam)
	}
	var out []sourced
	for tier := range bundlePerFamily {
		for _, fam := range perFam {
			if tier < len(fam) {
				out = append(out, fam[tier])
			}
		}
	}
	return out
}

// bundledHit is a verified bundled candidate.
type bundledHit struct {
	c  sourced
	pr ProbeResult
}

// chooseBundled picks among verified bundled copies (D103): the highest
// Claude Code version (from the probe, not the folder); on a tie the
// desktop app's copy, which is more stable than an editor extension's. It
// returns the pick and why, for doctor.
func chooseBundled(hits []bundledHit) (bundledHit, string) {
	best := hits[0]
	for _, h := range hits[1:] {
		a, b := parseBundleVersion(h.pr.Version), parseBundleVersion(best.pr.Version)
		switch {
		case a.newer(b):
			best = h
		case !b.newer(a) && h.c.src == SourceDesktop && best.c.src != SourceDesktop:
			best = h
		}
	}
	if len(hits) == 1 {
		return best, ""
	}
	var others []string
	tie := false
	for _, h := range hits {
		if h.c.path == best.c.path {
			continue
		}
		if h.pr.Version == best.pr.Version {
			tie = true
		}
		others = append(others, BundleName(h.c.src, h.c.path)+" has "+orUnknown(h.pr.Version))
	}
	name := BundleName(best.c.src, best.c.path)
	if tie && best.c.src == SourceDesktop {
		return best, fmt.Sprintf("chosen: %s's copy (%s); %s — on a tie the desktop app's copy is preferred, it is more stable than an editor extension's",
			name, orUnknown(best.pr.Version), strings.Join(others, ", "))
	}
	return best, fmt.Sprintf("chosen: %s's copy, the newest (%s); %s", name, orUnknown(best.pr.Version), strings.Join(others, ", "))
}

func orUnknown(v string) string {
	if v == "" {
		return "version unknown"
	}
	return v
}

// BundleNotes explains desktop-app folders that hold no executable (D103):
// for the newest <ver> folder of each claude-code base that has one but
// in which no claude was found, a line naming the folder and up to five
// of its entries, so the next bug report shows the layout.
func BundleNotes(env Env) []string {
	if env.Glob == nil {
		return nil
	}
	exe := "claude"
	if env.GOOS == "windows" {
		exe = "claude.exe"
	}
	home := env.searchHome()
	var notes []string
	for _, base := range desktopBases(env) {
		dirs, err := env.Glob(filepath.Join(base, "*"))
		if err != nil || len(dirs) == 0 {
			continue
		}
		var vers []string
		for _, d := range dirs {
			if fi, err := env.stat(d); err == nil && fi.IsDir() {
				vers = append(vers, d)
			}
		}
		if len(vers) == 0 {
			continue
		}
		sort.SliceStable(vers, func(i, j int) bool {
			return parseBundleVersion(filepath.Base(vers[i])).newer(parseBundleVersion(filepath.Base(vers[j])))
		})
		newest := vers[0]
		found := false
		esc := globEscape(newest)
		for _, pat := range []string{filepath.Join(esc, exe), filepath.Join(esc, "*", exe), filepath.Join(esc, "*", "*", exe),
			filepath.Join(esc, "claude.app", "Contents", "MacOS", exe)} {
			if m, err := env.Glob(pat); err == nil && len(m) > 0 {
				found = true
				break
			}
		}
		if found {
			continue
		}
		ents, _ := env.Glob(filepath.Join(globEscape(newest), "*"))
		names := make([]string, 0, 5)
		for _, e := range ents {
			n := filepath.Base(e)
			if fi, err := env.stat(e); err == nil && fi.IsDir() {
				n += string(filepath.Separator)
			}
			names = append(names, n)
		}
		sort.Strings(names)
		more := ""
		if len(names) > 5 {
			more = fmt.Sprintf(" (+%d more)", len(names)-5)
			names = names[:5]
		}
		listing := "it is empty"
		if len(names) > 0 {
			listing = "entries: " + strings.Join(names, ", ") + more
		}
		notes = append(notes, fmt.Sprintf("desktop app dir found (%s) but no %s inside; %s", ShortPath(home, newest), exe, listing))
	}
	return notes
}

// globEscape quotes the glob metacharacters of a literal path (a version
// folder named [x] must not become a character class).
func globEscape(p string) string {
	if !strings.ContainsAny(p, "*?[") {
		return p
	}
	var b strings.Builder
	for _, r := range p {
		if r == '*' || r == '?' || r == '[' {
			b.WriteByte('[')
			b.WriteRune(r)
			b.WriteByte(']')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// bundleSearched is where the bundled families look, for the not-found
// message: one short entry per family root.
func bundleSearched(env Env) (desktop, editors []string) {
	for _, f := range bundleFamilies(env) {
		for _, pat := range f.patterns {
			// Cut the pattern at its first wildcard: the folder to look in.
			root := pat
			if i := strings.IndexAny(root, "*?["); i >= 0 {
				root = filepath.Dir(root[:i+1])
			}
			if f.src == SourceExtension {
				// ~/.vscode/extensions → ~/.vscode
				root = filepath.Dir(root)
				editors = append(editors, root)
				continue
			}
			desktop = append(desktop, root)
		}
	}
	return dedupe(desktop), dedupe(editors)
}

func dedupe(list []string) []string {
	seen := map[string]bool{}
	out := list[:0]
	for _, s := range list {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// bundleVersion is a parsed version segment: numeric parts, plus whether
// a pre-release suffix follows (2.1.400-beta.1 < 2.1.400). A platform
// suffix (2.1.319-win32-x64) is not a pre-release.
type bundleVersion struct {
	nums []int
	pre  string
	ok   bool
}

var bundleVerRe = regexp.MustCompile(`^v?(\d+(?:\.\d+)*)(?:-([0-9A-Za-z.-]+))?`)

// platformSuffix matches the VS Code extension's target suffixes.
var platformSuffix = regexp.MustCompile(`^(?:win32|linux|darwin|alpine|web)(?:-|$)`)

func parseBundleVersion(s string) bundleVersion {
	m := bundleVerRe.FindStringSubmatch(s)
	if m == nil {
		return bundleVersion{}
	}
	v := bundleVersion{ok: true}
	for p := range strings.SplitSeq(m[1], ".") {
		n, err := strconv.Atoi(p)
		if err != nil {
			return bundleVersion{}
		}
		v.nums = append(v.nums, n)
	}
	if m[2] != "" && !platformSuffix.MatchString(m[2]) {
		v.pre = m[2]
	}
	return v
}

// newer reports whether v sorts before w (higher version first). An
// unparseable segment sorts last; equal versions keep their order.
func (v bundleVersion) newer(w bundleVersion) bool {
	if v.ok != w.ok {
		return v.ok
	}
	for i := range max(len(v.nums), len(w.nums)) {
		a, b := 0, 0
		if i < len(v.nums) {
			a = v.nums[i]
		}
		if i < len(w.nums) {
			b = w.nums[i]
		}
		if a != b {
			return a > b
		}
	}
	if (v.pre == "") != (w.pre == "") {
		return v.pre == ""
	}
	return v.pre > w.pre
}

// BundleName says which app a bundled path belongs to, for reports:
// "the Claude desktop app", "the VS Code extension", "the VS Code
// extension in Cursor". "" for a path that is not a known bundle.
func BundleName(src Source, path string) string {
	switch src {
	case SourceDesktop:
		return "the Claude desktop app"
	case SourceExtension:
		p := strings.ToLower(filepath.ToSlash(path))
		for _, e := range editorDirs {
			if strings.Contains(p, "/"+e.dir+"/extensions/") {
				if e.name == "VS Code" {
					break
				}
				return "the VS Code extension in " + e.name
			}
		}
		return "the VS Code extension"
	}
	return ""
}

// Bundled reports whether s is one of the app-bundled sources (D98).
func (s Source) Bundled() bool { return s == SourceDesktop || s == SourceExtension }
