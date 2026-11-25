<div align="center">

<a href="https://github.com/Mr-JoE1/cwm">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/brand/cwm-logo-on-dark.svg">
    <source media="(prefers-color-scheme: light)" srcset="docs/brand/cwm-logo-on-light.svg">
    <img alt="cwm" src="docs/brand/cwm-logo-on-light.svg" width="280">
  </picture>
</a>

<h3>Claude Workspace Manager</h3>

<p><strong>Your Claude Code workspace on every device, under any account.</strong></p>
<p>Every Claude Code session, memory, skill and MCP server on this machine,<br>synced peer-to-peer between your machines and independent of any login.</p>

[![Latest release](https://img.shields.io/github/v/release/Mr-JoE1/cwm?sort=semver&display_name=tag&color=00c27a&label=release)](https://github.com/Mr-JoE1/cwm/releases/latest) [![CI](https://img.shields.io/github/actions/workflow/status/Mr-JoE1/cwm/ci.yml?branch=main&label=CI&logo=github)](https://github.com/Mr-JoE1/cwm/actions/workflows/ci.yml?query=branch%3Amain) [![License: MIT](https://img.shields.io/github/license/Mr-JoE1/cwm?color=blue)](LICENSE) [![Go version](https://img.shields.io/github/go-mod/go-version/Mr-JoE1/cwm?logo=go&logoColor=white)](go.mod) [![Platforms](https://img.shields.io/badge/platform-Linux%20%7C%20Windows%20%7C%20macOS-555)](https://github.com/Mr-JoE1/cwm/wiki/Installation)

<p>
  <a href="https://github.com/Mr-JoE1/cwm/wiki"><b>📖 Docs</b></a> ·
  <a href="#install"><b>⬇️ Install</b></a> ·
  <a href="https://github.com/Mr-JoE1/cwm/wiki/Dashboard-Tour"><b>🖥️ Dashboard</b></a> ·
  <a href="CONTRIBUTING.md"><b>🤝 Contributing</b></a> ·
  <a href="SECURITY.md"><b>🛡️ Security</b></a>
</p>

</div>

## Install

```sh
curl -fsSL https://mr-joe1.github.io/cwm/install.sh | sh        # Linux / macOS
```

```powershell
irm https://mr-joe1.github.io/cwm/install.ps1 | iex             # Windows (PowerShell)
```

Per user, with no sudo and no admin rights. Every download is SHA-256-checked against the release's `checksums.txt`, and nothing is installed if the check fails.

> [!TIP]
> Prefer a manual install? Archives for every platform, `.deb`, checksums, upgrading and uninstalling are in the **[Installation guide](https://github.com/Mr-JoE1/cwm/wiki/Installation)**.

<table>
  <tr>
    <td width="33%" valign="top">
      <h4>1 · The dashboard opens</h4>
      <p>The installer runs <code>cwm init</code>, sets up autostart at login and opens the local dashboard.</p>
    </td>
    <td width="33%" valign="top">
      <h4>2 · Add an account</h4>
      <p>The setup wizard walks you through the Claude login. Add more any time; they all share one workspace.</p>
    </td>
    <td width="33%" valign="top">
      <h4>3 · Pair &amp; resume</h4>
      <p>Optionally pair another device, then resume any session anywhere. Repos clone on demand.</p>
    </td>
  </tr>
</table>

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/img/gui-sessions-dark.png">
    <source media="(prefers-color-scheme: light)" srcset="docs/img/gui-sessions-light.png">
    <img alt="The cwm dashboard: every workspace session from every device, newest first, with a Resume button per row" src="docs/img/gui-sessions-dark.png" width="100%">
  </picture>
</p>

## Why cwm

Claude Code keeps your sessions, memory, skills and MCP setup in one config folder on one machine, tied to one login. Switching accounts splits your history, a new computer starts from zero, and copied sessions don't resume on another OS because their folder names come from OS-specific paths.

**cwm makes the workspace the main thing.** By default (capture mode) `~/.claude` itself is the workspace: every Claude Code run on the machine lands in it — plain `claude`, the IDE extensions, the desktop app — with no wrapper and no environment variable. Claude accounts are logins kept in a credential vault, so switching accounts swaps the login and changes nothing else. An allow-list of workspace items syncs directly between your own devices with [Syncthing](https://syncthing.net), with no cloud in between, and cwm mirrors each session under the folder name your other machine's path for the same repo produces, so Linux, Windows and macOS resume the same conversations. Prefer to keep `~/.claude` untouched? Isolated mode keeps the workspace in `~/.cwm/workspace` for runs started through cwm ([Capture vs. isolated](https://github.com/Mr-JoE1/cwm/wiki/Capture-vs-Isolated)).

## Features

<table>
  <tr>
    <td width="33%" valign="top"><h4>🔀 Multiple accounts</h4><p>Logins in a vault: <code>cwm account use</code> saves the current login and swaps in another. <code>cwm run --account</code> runs a second account beside it, on the same sessions.</p></td>
    <td width="33%" valign="top"><h4>🔒 One workspace</h4><p>Capture mode makes <code>~/.claude</code> the workspace, so every Claude Code run is in it. Isolated mode keeps it in <code>~/.cwm/workspace</code> instead.</p></td>
    <td width="33%" valign="top"><h4>🔄 Device sync</h4><p>Peer-to-peer over Syncthing, from an allow-list you switch per item. <code>cwm pair</code> does the setup. Credentials never sync.</p></td>
  </tr>
  <tr>
    <td valign="top"><h4>🗂️ Session browser</h4><p>Every session from every device in one list. Resume in one click, even when the repo sits at a different path on this machine; it clones if it's missing.</p></td>
    <td valign="top"><h4>🖥️ Web dashboard</h4><p>Sessions, Projects, Devices, handoff, Health, Accounts (add, log in, make default, run in parallel, remove), Stats and Settings, in a tmux-style frame with a Ctrl-K command palette. Dark/light themes, live updates, phone layout with a bottom bar.</p></td>
    <td valign="top"><h4>⚡ Always on</h4><p>A small per-user agent starts at login and serves a bookmarkable dashboard. No admin rights.</p></td>
  </tr>
  <tr>
    <td valign="top"><h4>🧩 MCP servers</h4><code>cwm mcp add</code> once, and the server is there on every device and every account.</td>
    <td valign="top"><h4>🌿 WIP handoff</h4><p>Uncommitted work is pushed to a <code>sync/&lt;host&gt;</code> branch when a session ends.</p></td>
    <td valign="top"><h4>🩺 Health checks</h4><code>cwm doctor</code> and the Health tab check Claude Code, the workspace, Syncthing and SSH.</td>
  </tr>
  <tr>
    <td valign="top"><h4>📦 One binary</h4><p>Linux, Windows and macOS on amd64 and arm64. No runtime, no Node, no CDN.</p></td>
    <td valign="top"><h4>📥 Bring your setup</h4><p>An existing <code>~/.claude</code> is adopted in place. <code>cwm migrate</code> previews, backs up with a sha256 manifest, and rolls back byte for byte.</p></td>
    <td valign="top"><h4>🛡️ Local only</h4><p>No telemetry, no cloud. The dashboard listens on localhost behind a token.</p></td>
  </tr>
  <tr>
    <td valign="top" colspan="3"><h4>📊 Stats</h4><p>Sessions, messages, tokens, streaks and a year-long activity heatmap, counted from your local transcripts (<code>cwm stats</code> and the Stats tab); nothing is sent anywhere.</p></td>
  </tr>
</table>

## Screenshots

<table>
  <tr>
    <td colspan="2" valign="top" align="center">
      <picture>
        <source media="(prefers-color-scheme: light)" srcset="docs/img/gui-stats-light.png">
        <img alt="Stats tab, dark theme: KPI cards, the 12-month activity heatmap, top projects, top models and the billed/cache token bars (generated demo data)" src="docs/img/gui-stats-dark.png" width="100%">
      </picture><br>
      <sub><b>Stats</b>: activity from your local transcripts (demo data)</sub>
    </td>
  </tr>
  <tr>
    <td width="68%" valign="top" align="center">
      <img alt="Health tab, dark theme: a boot log with the problems first, each with a plain why and a copyable fix command" src="docs/img/gui-health-dark.png" width="100%"><br>
      <sub><b>Health</b>: what to fix first, and the command that fixes it</sub>
    </td>
    <td width="32%" valign="top" align="center" rowspan="2">
      <img alt="Sessions tab on a phone: sessions grouped by day as cards with a full-width resume button, and the bottom bar" src="docs/img/gui-sessions-mobile-dark.png" width="100%"><br>
      <sub><b>Mobile</b>: session cards and a bottom bar</sub>
    </td>
  </tr>
  <tr>
    <td width="68%" valign="top" align="center">
      <img alt="Setup wizard, light theme: the step rail, step 1 (initialize the workspace) and the live setup log" src="docs/img/gui-wizard-light.png" width="100%"><br>
      <sub><b>Setup wizard</b>: init, account login and pairing</sub>
    </td>
  </tr>
</table>

## cwm vs. a manual setup

|  | Claude Code on its own | By hand (symlinks, copied folders, DIY sync) | **cwm** |
|---|---|---|---|
| Several accounts, one shared history | one config folder per account | symlink each item yourself | ✅ accounts attach to one workspace |
| Sessions synced across devices | — | set up sync and ignore rules yourself | ✅ `cwm pair`, ignore rules included |
| Resume on another OS or path | — (folder names differ per path) | rename or rewrite paths | ✅ sessions mirrored per repo |
| Credentials kept out of sync | n/a | easy to get wrong | ✅ allow-list sync, never `.credentials.json` or `.claude.json` |
| Repos cloned on demand | — | — | ✅ from the synced repo list |
| Browse all sessions + dashboard | per project folder | — | ✅ `cwm sessions` and the web UI |

## Quick start

```sh
cwm account add work          # save another Claude login in the vault (the live one is kept)
cwm account use work          # make it the default: plain claude now logs in as "work"
cwm run --account personal    # a second account beside the default, same sessions
cwm myproject                 # open ~/repos/myproject: clone on demand, then the resume picker
cwm sessions --pick           # every session, newest first: pick one to resume
cwm pair                      # pair this device with another one
cwm doctor                    # health checks
cwm config list               # every setting (the dashboard's Settings tab edits the same keys)
```

cwm finds Claude Code even when its folder isn't on `PATH` yet (right after its installer, for example), remembers it as `claude_path` in `config.toml`, and prints the command that puts it on `PATH`. With only the Claude desktop app or the VS Code extension installed, neither of which puts `claude` on `PATH`, cwm uses the copy bundled with them (desktop app first), looks it up again each time because its folder changes with every app update, and recommends the standalone CLI. To use a specific binary, set `CWM_CLAUDE=/path/to/claude`.

Every command takes `--help`, and every view takes `--json`. Full list: **[CLI Reference](https://github.com/Mr-JoE1/cwm/wiki/CLI-Reference)**.

## What syncs, and what doesn't

Synced (each item has a switch, `cwm config set sync.<item> off`): sessions (`projects/`), prompt history, `CLAUDE.md`, skills, agents, commands, rules, output styles, scheduled tasks, plugins (not their caches), `settings.json` and cwm's `mcp.json`, which cwm merges into each machine's `.claude.json`.

Never synced: credentials (`.credentials.json`, the macOS Keychain) and `.claude.json` itself (it holds the login identity and machine-specific state), caches, logs, todos, tasks, shell snapshots, file history, IDE state and anything a future Claude Code version adds. The `.stignore` is an allow-list, so new items stay local until cwm knows them.

Not cwm's to sync: claude.ai connectors and other account-level settings live in Anthropic's cloud and follow your account, not your devices. An MCP server added with `claude mcp add` (outside cwm) stays on that machine; `cwm mcp add` makes it follow you. A session open on two devices at once can split; cwm keeps both copies and the Health tab says so.

Details: [Device sync](https://github.com/Mr-JoE1/cwm/wiki/Device-Sync).

## Documentation

<table>
  <tr>
    <td width="50%" valign="top">
      <h4>👤 <a href="https://github.com/Mr-JoE1/cwm/wiki/User-Guide">User Guide</a></h4>
      <a href="https://github.com/Mr-JoE1/cwm/wiki/Getting-Started">Getting started</a> ·
      <a href="https://github.com/Mr-JoE1/cwm/wiki/Installation">Installation</a> ·
      <a href="https://github.com/Mr-JoE1/cwm/wiki/First-Time-Setup">Setup wizard</a> ·
      <a href="https://github.com/Mr-JoE1/cwm/wiki/Device-Sync">Device sync</a> ·
      <a href="https://github.com/Mr-JoE1/cwm/wiki/Capture-vs-Isolated">Capture vs. isolated</a> ·
      <a href="https://github.com/Mr-JoE1/cwm/wiki/Migration">Migration</a> ·
      <a href="https://github.com/Mr-JoE1/cwm/wiki/Auto-Start-and-Service">Autostart</a> ·
      <a href="https://github.com/Mr-JoE1/cwm/wiki/Troubleshooting">Troubleshooting</a> ·
      <a href="https://github.com/Mr-JoE1/cwm/wiki/FAQ">FAQ</a>
    </td>
    <td width="50%" valign="top">
      <h4>🛠 <a href="https://github.com/Mr-JoE1/cwm/wiki/Developer-Guide">Developer Guide</a></h4>
      <a href="https://github.com/Mr-JoE1/cwm/wiki/Architecture">Architecture</a> ·
      <a href="https://github.com/Mr-JoE1/cwm/wiki/Building-and-Testing">Building &amp; testing</a> ·
      <a href="https://github.com/Mr-JoE1/cwm/wiki/Local-API-Reference">Local API</a> ·
      <a href="https://github.com/Mr-JoE1/cwm/wiki/Security-Model">Security model</a> ·
      <a href="https://github.com/Mr-JoE1/cwm/wiki/Decision-Log">Decision log</a> ·
      <a href="https://github.com/Mr-JoE1/cwm/wiki/Roadmap">Roadmap</a>
    </td>
  </tr>
</table>

## Privacy & security

- **Local only, no telemetry.** No server, no analytics, nothing phones home. Sync runs directly between your devices.
- **Credentials stay on the machine.** The live login and the vault (`~/.cwm/accounts`, or the macOS Keychain) are never synced, the dashboard API never returns them, and the debug report redacts them.
- **Localhost dashboard.** Bound to `127.0.0.1`, with a per-user token, CSRF checks on every action, a DNS-rebinding guard and strict input allowlists.
- **Verified installs.** The installers check SHA-256 against `checksums.txt` and refuse to install on a mismatch.

Details: [Privacy & Data](https://github.com/Mr-JoE1/cwm/wiki/Privacy-and-Data) · [Security Model](https://github.com/Mr-JoE1/cwm/wiki/Security-Model). Found a vulnerability? Please report it privately, as described in **[SECURITY.md](SECURITY.md)**.

## Contributing

Bug reports, ideas, docs fixes and pull requests are welcome. For a bug, attach the redacted debug report: **Copy debug logs** in the dashboard (Health tab) or `cwm debug --copy`. Start with **[CONTRIBUTING.md](CONTRIBUTING.md)**, and please follow the [Code of Conduct](CODE_OF_CONDUCT.md).

## License

MIT, see [LICENSE](LICENSE). cwm is an unofficial community tool, not affiliated with or endorsed by Anthropic.

---

<div align="center">

**If cwm saves you time, please [⭐ star the repo](https://github.com/Mr-JoE1/cwm/stargazers).** It helps others find it.

Designed &amp; developed by **Maher**<br> [GitHub](https://github.com/Mr-JoE1) · [X](https://x.com/MrJ0_3) · [LinkedIn](https://www.linkedin.com/in/mr-joe1)

</div>
