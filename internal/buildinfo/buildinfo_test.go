package buildinfo

import (
	"runtime/debug"
	"testing"
)

func fake(settings map[string]string) func() (*debug.BuildInfo, bool) {
	return func() (*debug.BuildInfo, bool) {
		bi := &debug.BuildInfo{GoVersion: "go1.99"}
		for k, v := range settings {
			bi.Settings = append(bi.Settings, debug.BuildSetting{Key: k, Value: v})
		}
		return bi, true
	}
}

func stamp(t *testing.T, v, c, d string, bi func() (*debug.BuildInfo, bool)) {
	t.Helper()
	ov, oc, od, ob := Version, Commit, BuildDate, readBuildInfo
	Version, Commit, BuildDate, readBuildInfo = v, c, d, bi
	t.Cleanup(func() { Version, Commit, BuildDate, readBuildInfo = ov, oc, od, ob })
}

func TestReleaseStamp(t *testing.T) {
	stamp(t, "v0.7.2", "a1b2c3d4e5f6", "2026-11-21T10:00:00Z", fake(map[string]string{"vcs.revision": "ffffffffffff", "vcs.modified": "true"}))
	in := Get()
	if in.Version != "0.7.2" || in.Commit != "a1b2c3d" || in.Dev || in.Dirty || in.BuildDate != "2026-11-21T10:00:00Z" || in.Go != "go1.99" {
		t.Fatalf("Get() = %+v", in)
	}
	if in.Label() != "v0.7.2" {
		t.Errorf("Label() = %q", in.Label())
	}
}

func TestVCSFallback(t *testing.T) {
	stamp(t, "", "", "", fake(map[string]string{"vcs.revision": "0123456789abcdef", "vcs.time": "2026-11-20T09:00:00Z", "vcs.modified": "true"}))
	in := Get()
	if !in.Dev || in.Version != "dev" || in.Commit != "0123456" || !in.Dirty || in.BuildDate != "2026-11-20T09:00:00Z" {
		t.Fatalf("Get() = %+v", in)
	}
	if got := in.Label(); got != "dev build 0123456" {
		t.Errorf("Label() = %q, want dev build 0123456", got)
	}
	if got := in.CommitLabel(); got != "0123456-dirty" {
		t.Errorf("CommitLabel() = %q", got)
	}
}

func TestNothingKnown(t *testing.T) {
	stamp(t, "", "", "", func() (*debug.BuildInfo, bool) { return nil, false })
	in := Resolve("dev", "dev")
	if !in.Dev || in.Commit != "unknown" || in.Label() != "dev build" {
		t.Fatalf("Resolve(dev, dev) = %+v label %q", in, in.Label())
	}
}

func TestLegacyOverrides(t *testing.T) {
	stamp(t, "", "", "", fake(nil))
	in := Resolve("0.6.9", "harness")
	if in.Dev || in.Version != "0.6.9" || in.Commit != "harness" {
		t.Fatalf("Resolve(legacy) = %+v", in)
	}
}

func TestLicenseAndAuthor(t *testing.T) {
	if got := License(); got != "MIT" {
		t.Errorf("License() = %q, want MIT", got)
	}
	if spdx("some custom terms") != "NOASSERTION" {
		t.Error("unknown text must be NOASSERTION")
	}
	if AuthorName != "Maher" || AuthorGitHub != "https://github.com/Mr-JoE1" || RepoURL != "https://github.com/Mr-JoE1/cwm" {
		t.Error("author constants changed (owner decision D69)")
	}
}
