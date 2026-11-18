# Security Policy

cwm handles Claude account credentials and runs a local web server, so we take
security reports seriously. Thank you for helping keep its users safe.

## Supported versions

cwm is pre-1.0. Security fixes go into `main` and ship in the next release.
Only the latest release line gets them.

| Version | Supported |
|---|---|
| `main` (v0.6 + v0.7, not yet tagged) | ✅ |
| latest release (currently v0.5.x) | ✅ |
| older releases (< v0.5) | ❌ upgrade first |

## Reporting a vulnerability

**Please don't open a public issue for a security problem.**

Use GitHub's private vulnerability reporting instead:

1. Go to the repository's **Security** tab.
2. Click **Report a vulnerability**
   ([direct link](https://github.com/Mr-JoE1/cwm/security/advisories/new)).
3. Describe the issue, the affected version (`cwm version`), your OS, and the
   steps to reproduce it. A proof of concept helps.

Only the maintainers can see the report. Please leave out real tokens,
credentials or personal data; redacted values are fine.

If the **Report a vulnerability** button isn't available, open a regular issue
that says only that you have a security report and asks for a private channel,
with **no details**, or contact the maintainer through their
[GitHub profile](https://github.com/Mr-JoE1).

## What to expect

| | Target |
|---|---|
| Acknowledgement | within **3 working days** |
| First assessment (confirmed / not a vulnerability / need more info) | within **7 days** |
| Fix for a confirmed high or critical issue | as fast as possible, normally within **30 days** |

We'll keep you posted in the advisory thread, agree on a disclosure date with
you, and credit you in the advisory and release notes unless you'd rather stay
anonymous. This is a volunteer project, so the times above are goals rather than
guarantees.

## Scope

In scope:

- **The dashboard / agent HTTP API**: authentication bypass, token leaks (for
  example the token showing up in a response, a log or a URL after the
  exchange), CSRF, DNS rebinding / `Host` checks, input validation, path
  traversal, and anything that makes the server run a command other than the
  allowlisted cwm commands.
- **The persistent token**: `~/.cwm/server.toml` permissions, rotation
  (`cwm service rotate-token`), cookie handling.
- **The installers** (`install.sh`, `install.ps1`): checksum bypass, download or
  path tampering, unsafe PATH or profile edits, privilege problems.
- **Credentials handling**: account credentials leaking into the synced
  workspace, logs, `--json` output or the API.
- **Autostart entries** (systemd user unit, Task Scheduler task, LaunchAgents)
  and the terminal launcher (argument injection).

Out of scope:

- Running the agent with `--bind` on a non-loopback address without TLS in
  front of it. This is documented as plain HTTP and needs your own reverse
  proxy.
- Attacks that need an attacker who already runs code as your user or can read
  your home directory.
- Vulnerabilities in Claude Code, Syncthing, git or other third-party software.
  Please report those upstream.
- Unsigned Windows binaries triggering SmartScreen or Defender warnings (see
  [docs/WINDOWS-SMARTSCREEN.md](docs/WINDOWS-SMARTSCREEN.md)).

## Security model

The dashboard binds to `127.0.0.1` by default, requires a per-user token
(HttpOnly SameSite=Strict cookie or `Authorization: Bearer`), requires a CSRF
header on every state-changing request, rejects unexpected `Host` headers, and
validates all input against allowlists. cwm sends no telemetry, and sync is
peer-to-peer through Syncthing. Full details: the wiki's
[Security Model](https://github.com/Mr-JoE1/cwm/wiki/Security-Model) and
[Privacy & Data](https://github.com/Mr-JoE1/cwm/wiki/Privacy-and-Data) pages,
and decisions D24, D27, D31, D32 and D37 in [docs/PLAN.md](docs/PLAN.md).
