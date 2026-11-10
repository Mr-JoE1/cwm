# Windows: SmartScreen warning or Defender deleting `cwm.exe`

You may hit one or both of these on Windows:

1. A blue box: **"Microsoft Defender SmartScreen prevented an unrecognized app
   from starting"**, showing *App: cwm.exe* and *Publisher: Unknown publisher*.
2. **Microsoft Defender Antivirus quarantines or deletes `cwm.exe`**, either
   right after you extract it or the first time you run it.

Neither one means cwm is malware. cwm's release binaries are **not
code-signed**, and every release is a **brand-new file Microsoft hasn't seen
before**. **You don't need to buy anything to avoid either problem.** The free
fixes come first below. The background is in
[*Why this happens*](#why-this-happens-the-mark-of-the-web). Paid signing is
optional, and it's at the very end.

---

## Free fixes (no purchase)

**Always get cwm from <https://github.com/Mr-JoE1/cwm/releases>** (or through
the scoop bucket, which points at the same files). To check a manual download
against `checksums.txt` from the same release:

```powershell
Get-FileHash .\cwm_*_windows_amd64.* -Algorithm SHA256
# compare with the matching line in checksums.txt
```

### 1. Scoop (recommended)

```powershell
scoop bucket add cwm https://github.com/Mr-JoE1/scoop-bucket
scoop install cwm
```

**No SmartScreen prompt.** Scoop downloads the release through .NET's HTTP
stack (`System.Net.WebRequest`). That never writes a Mark-of-the-Web, so the
installed `cwm.exe` carries no Zone.Identifier. Scoop also checks each
download's SHA-256 against the manifest. Update with `scoop update cwm`. New
to scoop? See <https://scoop.sh>. It installs per-user and doesn't need admin.

### 2. tar.gz + the built-in `tar.exe`

Releases after v0.4.1 include `cwm_<version>_windows_amd64.tar.gz` and
`cwm_<version>_windows_arm64.tar.gz` next to the zips. Download one, then
extract it **from a terminal** (PowerShell or cmd):

```powershell
tar -xzf .\cwm_<version>_windows_amd64.tar.gz
.\cwm.exe version
```

**No SmartScreen prompt.** Windows' built-in `tar.exe` (bsdtar, included since
Windows 10 build 17063 / version 1803) doesn't copy the archive's
Mark-of-the-Web onto the files it extracts. **Don't** open the archive with
File Explorer's "Extract all". Explorer *does* copy the mark (see the table
below). The same `tar` command also extracts the `.zip` without the mark:
`tar -xf .\cwm_<version>_windows_amd64.zip`.

### 3. `go install` (build it yourself)

```sh
go install github.com/Mr-JoE1/cwm/cmd/cwm@latest
```

**No SmartScreen prompt.** The binary is compiled on your own machine, so it
was never downloaded and carries no Mark-of-the-Web. Needs Go ≥ 1.25.

### 4. Already downloaded the zip? Unblock it

This removes the Mark-of-the-Web, so nothing you extract afterwards gets
tagged.

- **Before extracting:** right-click the `.zip` → **Properties** → **General**
  tab → tick **Unblock** → **OK**. If there's no Unblock box, the file isn't
  tagged.
- **PowerShell** (works on the zip, on an already-extracted `cwm.exe`, or both):

  ```powershell
  Unblock-File -Path .\cwm_*_windows_*.zip
  Get-ChildItem -Recurse -Filter cwm.exe | Unblock-File
  ```

Or click **More info → Run anyway** when the SmartScreen dialog appears.
Windows remembers that choice for this file.

### 5. Defender deleted or quarantined `cwm.exe`? Report the false positive

Removing the Mark-of-the-Web stops **SmartScreen**. It doesn't stop
**Defender Antivirus** from scanning the file. If Defender flags `cwm.exe`:

1. Open **Windows Security** → **Virus & threat protection** →
   **Protection history**. Find the cwm entry and note the **detection name**
   (e.g. `Trojan:Win32/Wacatac.B!ml`).
2. **Submit it to Microsoft as a false positive.** It's free, and it's what
   fixes the problem for everyone on that release:
   - <https://www.microsoft.com/wdsi/filesubmission>
   - Choose **Home customer** (or *Enterprise customer* on a managed device).
     Maintainers choose **Software developer**. Upload `cwm.exe` (or the
     release archive), select **Incorrectly detected as malware/malicious**,
     and paste the detection name.
   - Microsoft usually replies within a few days. Once it's cleared, update
     definitions (`Update-MpSignature` in an admin PowerShell) and download
     again.
3. If the checksum matches and you need cwm right away, restore it from
   Protection history (**Actions → Allow on device**), or add a narrow
   exclusion just for the cwm folder:

   ```powershell
   # admin PowerShell. Keep the exclusion as narrow as possible.
   Add-MpPreference -ExclusionPath "$env:LOCALAPPDATA\Programs\cwm"
   ```

   On managed or work machines, ask your IT team instead. Exclusions are often
   blocked by policy, and they should be.

Please also open an issue with the **detection name, cwm version and
architecture**, so maintainers know which builds to submit.

> **Smart App Control.** On Windows 11 with *Smart App Control* turned **On**,
> unsigned apps that carry the Mark-of-the-Web can be blocked with **no**
> "Run anyway" button. Options 1–4 all avoid that, because the binary carries
> no mark. SAC can't be turned back on once it's off, so don't disable it just
> for cwm.

---

## Why this happens: the Mark-of-the-Web

### SmartScreen looks for the Mark-of-the-Web

SmartScreen Application Reputation checks a program when it's launched through
the Windows shell (Explorer double-click, `ShellExecute`) **and** the file
carries the **Mark-of-the-Web (MOTW)**. MOTW is a hidden NTFS alternate data
stream named `Zone.Identifier`, usually with `ZoneId=3` (Internet). You can see
it with `Get-Content .\file -Stream Zone.Identifier`. A brand-new, unsigned file
has no reputation, so SmartScreen warns.

MOTW is **opt-in for the program that writes the file**. Browsers write it on
everything they download. Command-line and scripted downloads (the `curl.exe`
that ships with Windows, `bitsadmin`, scripts using WinINET/WinHTTP/System.Net)
don't. When you extract an archive, the **extracting tool** decides whether the
archive's mark is copied onto the files inside. See Eric Lawrence's
[*Downloads and the Mark-of-the-Web*](https://textslashplain.com/2016/04/04/downloads-and-the-mark-of-the-web).

How the mark reaches `cwm.exe` in the usual case:

1. Your browser downloads `cwm_<version>_windows_amd64.zip` and tags the
   **zip** with MOTW.
2. Explorer's "Extract all" **copies the tag onto every extracted file**,
   including `cwm.exe`. This is on purpose, so wrapping malware in an archive
   can't strip the mark.
3. Double-clicking the tagged `cwm.exe` triggers the reputation check, and
   "Unknown publisher" appears because there's no Authenticode signature.

| How you got `cwm.exe` | MOTW on `cwm.exe`? |
|---|---|
| Browser download + Explorer "Extract all" | **Yes**, so SmartScreen applies |
| Browser download + `tar -xzf` / `tar -xf` (built-in `tar.exe`, bsdtar) | **No** |
| Browser download + 7-Zip | **No** by default. 7-Zip has an opt-in setting to copy it. |
| `scoop install cwm` | **No**. Scoop's .NET downloader doesn't write MOTW. |
| `curl.exe -LO …` then extract | **No**. Command-line downloads don't add MOTW. |
| `go install …@latest` | **No**. Built locally, never downloaded. |

The archiver rows come from the community-maintained comparison
<https://github.com/nmantani/archiver-MOTW-support-comparison> (Explorer tested
on Windows 11 25H2, `tar.exe` bsdtar 3.8.4: no MOTW propagation, 7-Zip:
supported but disabled by default). Explorer's own behavior may change across
Windows builds. The tar path is reliable because it skips Explorer entirely.

The **format** itself doesn't remove the mark. The downloaded `.tar.gz` is
tagged just like a `.zip` would be. What matters is the **tool** that extracts
it. Releases ship a `.tar.gz` because people naturally extract it with
`tar.exe`, and a `.zip` with Explorer.

### Defender Antivirus deleting the file

This is a **false positive** from Defender's machine-learning heuristics, and
it's separate from MOTW. Detection names usually end in `!ml`, for example
`Trojan:Win32/Wacatac.B!ml`. Go programs trigger these often because:

- they are large, statically linked single files with an unusual layout;
- release builds are stripped (`-s -w`), so there are few symbols or metadata;
- they are **unsigned** and **new**, with zero prevalence;
- malware authors also write in Go, so the models have learned "unsigned, new,
  stripped Go binary" as a weak risk signal.

The Go FAQ covers this: <https://go.dev/doc/faq#virus>. The fix is the free
false-positive submission in [step 5](#5-defender-deleted-or-quarantined-cwmexe-report-the-false-positive).

### Why it comes back with every release

For unsigned files, reputation and false-positive clearances are tied to the
**file hash**. Every release and architecture is a different file, so each one
starts from zero. Scoop, `tar.exe` and `go install` avoid the SmartScreen part
for every release. For Defender, maintainers can submit each new `cwm.exe`
(amd64 and arm64) as a *Software developer* right after publishing.

---

## Maintainer notes

Free things that help, with no certificate:

- Ship the Windows `.tar.gz` (configured in [`.goreleaser.yaml`](../.goreleaser.yaml))
  and keep the scoop manifest (`bucket/cwm.json` in
  <https://github.com/Mr-JoE1/scoop-bucket>) current with each release's
  version, URLs and `checksums.txt` hashes.
- Submit each new `cwm.exe` to <https://www.microsoft.com/wdsi/filesubmission>
  as a **Software developer** right after publishing.
- Embed a Windows version resource (product name, file version, description),
  e.g. with `github.com/tc-hib/go-winres` or
  `github.com/josephspurrier/goversioninfo`. Files with no metadata look more
  suspicious to ML models.
- Never pack with UPX. Packed Go binaries get flagged far more often.

---

## Optional: paid code signing

**This isn't needed to distribute or use cwm.** Everything above works
without it. It's documented in case the project wants a named publisher later.

**The signing tools are free and open source.**
[`osslsigncode`](https://github.com/mtrojnar/osslsigncode) runs on Linux CI,
and Microsoft's `signtool` comes free with the Windows SDK. The
commented-out hook in [`.goreleaser.yaml`](../.goreleaser.yaml) already shows
where they plug in. **The only part that can cost money is a certificate that
Windows trusts**, and even that has a free route for OSS projects (SignPath
Foundation, below).

Signing does **not** make SmartScreen trust a file instantly. Since 2024, even
EV certificates have to build reputation. What it adds:

- a real publisher name instead of "Unknown publisher";
- reputation that builds on the **certificate** across releases, instead of
  restarting with every new hash;
- typically fewer ML false positives in Defender and other AV.

Certificate options as of late 2026:

| Option | Cost | Eligibility | Notes |
|---|---|---|---|
| **SignPath Foundation** (OSS program) | Free | OSI-licensed OSS, no proprietary parts, already released, actively maintained | Certificate is issued to *SignPath Foundation*, which is what Windows shows as the publisher. Binaries must be built by CI from the public repo. Each release needs approval. Requires a published code-signing policy and MFA. Acceptance is at their discretion. Has a GitHub Actions integration. <https://signpath.org/> |
| **Certum Open Source Code Signing** | From ~€25–69 depending on option | Individuals | Certificate reads "Open Source Developer, <name>". Needs identity verification. Since 27 Feb 2026, certs are valid for at most 459 days. <https://shop.certum.eu/code-signing.html> |
| **Azure Artifact Signing** (formerly Trusted Signing) | ~$9.99/month | Individuals in the USA/Canada only; organizations in more regions | Needs an Azure subscription and identity verification. <https://learn.microsoft.com/en-us/azure/artifact-signing/quickstart> |
| OV certificate (commercial CAs) | ~$150–300/year | Worldwide | Key must be on a hardware token or cloud HSM (CA/B Forum rule since 2023). |
| EV certificate | $400+/year | Worldwide | No longer bypasses SmartScreen reputation (changed 2024). Not worth the premium. |
| Self-signed | Free | — | Not trusted by other machines. Useless for public distribution. |

Microsoft's own comparison:
<https://learn.microsoft.com/en-us/windows/apps/package-and-deploy/code-signing-options>
