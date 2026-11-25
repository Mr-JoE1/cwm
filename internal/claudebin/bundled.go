package claudebin

import (
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
//     (also under the MSIX package's virtualised LocalCache\Roaming);
//   - desktop app, legacy Squirrel: %LOCALAPPDATA%\AnthropicClaude\app-<ver>\…\claude.exe,
//     except app-<ver>\claude.exe itself, which is the desktop app;
//   - desktop app, macOS: ~/Library/Application Support/Claude/claude-code/<ver>/claude
//     (same shape as Windows; not confirmed on hardware);
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
	// <…>/claude-code/<ver>/claude: the version is the parent dir.
	parentVer := func(m string) string { return filepath.Base(filepath.Dir(m)) }
	switch env.GOOS {
	case "windows":
		var pats []string
		if roaming := env.Getenv("APPDATA"); roaming != "" {
			pats = append(pats, filepath.Join(roaming, "Claude", "claude-code", "*", exe))
		}
		local := env.Getenv("LOCALAPPDATA")
		if local != "" {
			pats = append(pats, filepath.Join(local, "Packages", "*Claude*", "LocalCache", "Roaming", "Claude", "claude-code", "*", exe))
		}
		if len(pats) > 0 {
			fams = append(fams, bundleFamily{src: SourceDesktop, patterns: pats, version: parentVer})
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
	case "darwin":
		if home != "" {
			fams = append(fams, bundleFamily{src: SourceDesktop, version: parentVer,
				patterns: []string{filepath.Join(home, "Library", "Application Support", "Claude", "claude-code", "*", exe)}})
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

// bundledCandidates globs every family and returns, per family, the
// matches ordered highest version first, capped at bundlePerFamily.
func bundledCandidates(env Env) []sourced {
	if env.Glob == nil {
		return nil
	}
	var out []sourced
	for _, f := range bundleFamilies(env) {
		var matches []string
		for _, pat := range f.patterns {
			m, err := env.Glob(pat)
			if err != nil {
				continue
			}
			for _, p := range m {
				matches = append(matches, p)
			}
		}
		ver := map[string]bundleVersion{}
		for _, m := range matches {
			ver[m] = parseBundleVersion(f.version(m))
		}
		sort.SliceStable(matches, func(i, j int) bool { return ver[matches[i]].newer(ver[matches[j]]) })
		for i, m := range matches {
			if i == bundlePerFamily {
				break
			}
			out = append(out, sourced{path: m, src: f.src})
		}
	}
	return out
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
