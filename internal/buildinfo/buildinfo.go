// Package buildinfo says which cwm this is (D69): the version, commit and
// build time stamped by the release ldflags, with a fallback to the VCS
// data the Go toolchain embeds in every module build, plus the fixed
// author metadata the About view shows.
package buildinfo

import (
	"runtime"
	"runtime/debug"
	"strings"

	cwm "github.com/Mr-JoE1/cwm"
)

// Set at release time with -X (.goreleaser.yaml, the install harnesses).
// Empty means "not stamped": Resolve falls back to the VCS build info.
var (
	Version   = ""
	Commit    = ""
	BuildDate = ""
)

// Author metadata (owner decision, D69): a name and two links, nothing else.
const (
	AuthorName   = "Maher"
	AuthorGitHub = "https://github.com/Mr-JoE1"
	RepoURL      = "https://github.com/Mr-JoE1/cwm"
	Channel      = "beta"
)

// Info is the resolved identity of the running binary.
type Info struct {
	Version   string // "0.7.2" (no leading v), or "dev"
	Commit    string // short commit, or "unknown"
	BuildDate string // RFC 3339 when known
	Dirty     bool   // built from a modified tree (VCS fallback only)
	Dev       bool   // no release version stamped
	Go        string
	OS        string
	Arch      string
}

// readBuildInfo is a seam for tests.
var readBuildInfo = debug.ReadBuildInfo

// Get resolves the identity from the ldflags and the VCS fallback.
func Get() Info { return Resolve("", "") }

// Resolve is Get with the legacy overrides cli.Version / cli.Commit (older
// build scripts stamp those; "dev" and "" mean unset).
func Resolve(legacyVersion, legacyCommit string) Info {
	in := Info{Go: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH}
	in.Version = first(Version, unsetDev(legacyVersion))
	in.Commit = first(Commit, unsetDev(legacyCommit))
	in.BuildDate = BuildDate
	if bi, ok := readBuildInfo(); ok && bi != nil {
		if bi.GoVersion != "" {
			in.Go = bi.GoVersion
		}
		var rev, at, modified string
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.time":
				at = s.Value
			case "vcs.modified":
				modified = s.Value
			}
		}
		if in.Commit == "" && rev != "" {
			in.Commit = short(rev)
			in.Dirty = modified == "true"
		}
		if in.BuildDate == "" {
			in.BuildDate = at
		}
	}
	in.Version = strings.TrimPrefix(in.Version, "v")
	if in.Version == "" {
		in.Version, in.Dev = "dev", true
	}
	if in.Commit == "" {
		in.Commit = "unknown"
	}
	in.Commit = short(in.Commit)
	return in
}

// Label is what the dashboard shows: "v0.7.2", or "dev build a1b2c3d" —
// never "dev … dev".
func (in Info) Label() string {
	if in.Dev {
		if in.Commit == "" || in.Commit == "unknown" {
			return "dev build"
		}
		return "dev build " + in.Commit
	}
	return "v" + in.Version
}

// CommitLabel is the commit with a "-dirty" suffix for a modified tree.
func (in Info) CommitLabel() string {
	if in.Dirty {
		return in.Commit + "-dirty"
	}
	return in.Commit
}

// License is the SPDX id of the embedded LICENSE ("NOASSERTION" when the
// text is not one this function recognises).
func License() string { return spdx(cwm.License) }

func spdx(text string) string {
	t := strings.Join(strings.Fields(text), " ")
	switch {
	case strings.HasPrefix(t, "MIT License") && strings.Contains(t, "Permission is hereby granted, free of charge"):
		return "MIT"
	case strings.Contains(t, "Apache License") && strings.Contains(t, "Version 2.0"):
		return "Apache-2.0"
	case strings.Contains(t, "GNU GENERAL PUBLIC LICENSE") && strings.Contains(t, "Version 3"):
		return "GPL-3.0"
	case strings.Contains(t, "Mozilla Public License Version 2.0"):
		return "MPL-2.0"
	}
	return "NOASSERTION"
}

func first(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func unsetDev(v string) string {
	if v == "dev" {
		return ""
	}
	return v
}

func short(rev string) string {
	if len(rev) > 7 && isHex(rev) {
		return rev[:7]
	}
	return rev
}

func isHex(s string) bool {
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}
