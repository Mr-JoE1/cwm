package service

import (
	"encoding/xml"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
)

// macOS LaunchAgent labels.
const (
	AgentLabel     = "io.github.mr-joe1.cwm.agent"
	DashboardLabel = "io.github.mr-joe1.cwm.dashboard"
)

// PlistPath is ~/Library/LaunchAgents/<label>.plist.
func (e Env) PlistPath(label string) string {
	return filepath.Join(e.Home, "Library", "LaunchAgents", label+".plist")
}

func (e Env) domain() string { return "gui/" + strconv.Itoa(e.UID) }

var plistTmpl = template.Must(template.New("plist").Funcs(template.FuncMap{
	"x": func(s string) string {
		var b strings.Builder
		_ = xml.EscapeText(&b, []byte(s))
		return b.String()
	},
}).Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<!-- ` + generatedBanner + ` -->
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>{{x .Label}}</string>
  <key>ProgramArguments</key>
  <array>
{{- range .Args}}
    <string>{{x .}}</string>
{{- end}}
  </array>
  <key>RunAtLoad</key>
  <true/>
{{- if .KeepAlive}}
  <key>KeepAlive</key>
  <dict>
    <key>SuccessfulExit</key>
    <false/>
  </dict>
{{- end}}
  <key>LimitLoadToSessionType</key>
  <string>Aqua</string>
{{- if or .CwmHome .Path}}
  <key>EnvironmentVariables</key>
  <dict>
{{- if .CwmHome}}
    <key>CWM_HOME</key>
    <string>{{x .CwmHome}}</string>
{{- end}}
{{- if .Path}}
    <key>PATH</key>
    <string>{{x .Path}}</string>
{{- end}}
  </dict>
{{- end}}
  <key>StandardOutPath</key>
  <string>{{x .Log}}</string>
  <key>StandardErrorPath</key>
  <string>{{x .Log}}</string>
</dict>
</plist>
`))

func (e Env) plist(label string, args []string, keepAlive bool, log string) (File, error) {
	content, err := render(plistTmpl, map[string]any{
		"Label": label, "Args": args, "KeepAlive": keepAlive, "CwmHome": e.CwmHome, "Path": e.ServicePath(), "Log": log,
	})
	if err != nil {
		return File{}, err
	}
	return File{Path: e.PlistPath(label), Content: content, Mode: 0o644}, nil
}

func darwinInstall(e Env, o Options) (Plan, error) {
	if err := checkPath(e.Exe); err != nil {
		return Plan{}, err
	}
	logs := filepath.Join(e.Root, "logs")
	var p Plan
	add := func(label string, args []string, keepAlive bool, log string) error {
		f, err := e.plist(label, args, keepAlive, log)
		if err != nil {
			return err
		}
		p.Files = append(p.Files, f)
		// bootout first so a reinstall replaces a loaded job.
		p.Pre = append(p.Pre, Command{Name: "launchctl", Args: []string{"bootout", e.domain() + "/" + label}, IgnoreError: true})
		p.Commands = append(p.Commands, Command{Name: "launchctl", Args: []string{"bootstrap", e.domain(), f.Path}})
		return nil
	}
	if o.OpenOnly {
		if err := add(DashboardLabel, []string{e.Exe, "service", "open-url", o.URL}, false, filepath.Join(logs, "opener.log")); err != nil {
			return Plan{}, err
		}
		p.Notes = append(p.Notes, "only the browser opener was installed; the agent itself runs elsewhere (e.g. a container)")
		return p, nil
	}
	if err := add(AgentLabel, []string{e.Exe, "agent", "--no-open"}, true, filepath.Join(logs, "agent.log")); err != nil {
		return Plan{}, err
	}
	if o.NoOpen {
		p.Pre = append(p.Pre, Command{Name: "launchctl", Args: []string{"bootout", e.domain() + "/" + DashboardLabel}, IgnoreError: true})
		p.Remove = append(p.Remove, e.PlistPath(DashboardLabel))
		return p, nil
	}
	if err := add(DashboardLabel, []string{e.Exe, "dashboard", "--wait", "30"}, false, filepath.Join(logs, "opener.log")); err != nil {
		return Plan{}, err
	}
	return p, nil
}

func darwinUninstall(e Env) Plan {
	var p Plan
	for _, l := range []string{AgentLabel, DashboardLabel} {
		p.Pre = append(p.Pre, Command{Name: "launchctl", Args: []string{"bootout", e.domain() + "/" + l}, IgnoreError: true})
		p.Remove = append(p.Remove, e.PlistPath(l))
	}
	return p
}

func darwinControl(e Env, a Action) (Plan, bool) {
	if !e.exists(e.PlistPath(AgentLabel)) {
		return Plan{}, false
	}
	target := e.domain() + "/" + AgentLabel
	if a == Stop {
		// SIGTERM → the agent exits 0 → KeepAlive(SuccessfulExit=false)
		// leaves it stopped until the next login or `cwm service start`.
		return Plan{Commands: []Command{{Name: "launchctl", Args: []string{"kill", "SIGTERM", target}}}}, true
	}
	return Plan{Commands: []Command{{Name: "launchctl", Args: []string{"kickstart", target}}}}, true
}

func darwinStatus(e Env) []Item {
	items := []Item{
		fileItem(e, "LaunchAgent (agent)", e.PlistPath(AgentLabel)),
		fileItem(e, "LaunchAgent (opener)", e.PlistPath(DashboardLabel)),
	}
	if e.Runner != nil && e.exists(e.PlistPath(AgentLabel)) {
		state := "not loaded"
		if out, err := e.Runner.Run("launchctl", "print", e.domain()+"/"+AgentLabel); err == nil {
			state = "loaded"
			for _, line := range strings.Split(out, "\n") {
				if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok && strings.TrimSpace(k) == "state" {
					state = strings.TrimSpace(v)
					break
				}
			}
		}
		items = append(items, Item{Name: "launchd", State: state, Detail: AgentLabel})
	}
	return items
}
