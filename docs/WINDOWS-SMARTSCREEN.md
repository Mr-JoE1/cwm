# Windows: SmartScreen warning or Defender deleting `cwm.exe`

You may hit one or both of these on Windows:

1. A blue box: **"Microsoft Defender SmartScreen prevented an unrecognized app
   from starting"**, showing *App: cwm.exe* and *Publisher: Unknown publisher*.
2. **Microsoft Defender Antivirus quarantines or deletes `cwm.exe`**, either
   right after you unzip it or the first time you run it.

Neither one means cwm is malware. Both come from the same two facts: cwm's
release binaries are **not code-signed yet**, and every release is a **brand-new
file Microsoft hasn't seen before**. The rest of this page explains what's going
on, how to get past it safely, and what the project plans to do about it.

---

## Why this happens

### SmartScreen: "unrecognized app"

SmartScreen checks programs by **reputation**, not by scanning them. It asks
Microsoft's cloud whether this exact file, or its signing publisher, is
commonly downloaded and known to be safe. A brand-new, unsigned file has no
reputation, so you get the warning.

SmartScreen only checks files that carry the **Mark of the Web (MOTW)**. That's
a hidden `Zone.Identifier` tag Windows adds to anything your browser downloads,
and it tells Windows the file came from the internet.

The way MOTW reaches `cwm.exe`:

- Your browser downloads `cwm_<version>_windows_amd64.zip` and tags the
  **zip** with MOTW.
- When you extract it with **File Explorer** ("Extract All…" or drag out of
  the zip), Explorer **copies the tag onto every extracted file**, including
  `cwm.exe`. Microsoft built this on purpose so attackers can't strip the mark
  by wrapping malware in a zip.
- Double-clicking `cwm.exe`, or launching it through anything that goes through
  the Windows shell, then sees the tag and runs the SmartScreen check.
  "Unknown publisher" is shown because the file has no Authenticode signature.

What carries MOTW over and what doesn't:

| How you got `cwm.exe` | Does `cwm.exe` get MOTW? |
|---|---|
| Browser download + Explorer "Extract All" | **Yes**, so SmartScreen applies |
| Browser download + `tar -xf file.zip` (built-in `tar.exe`, bsdtar) | Usually **no**. bsdtar doesn't copy `Zone.Identifier` onto extracted files. Not documented by Microsoft, so treat it as observed behavior. |
| Browser download + 7-Zip | **No** by default (7-Zip ≥ 22 has an opt-in setting to copy it) |
| `curl.exe -LO …` / `Invoke-WebRequest` then extract | **No**. Command-line downloads don't add MOTW. |
| `go install github.com/Mr-JoE1/cwm/cmd/cwm@latest` | **No**. Built on your own machine. |

The archive format (zip vs tar.gz) isn't what matters. What matters is
**which tool extracts it**. Explorer copies the tag onto extracted files;
`tar.exe` and most third-party tools don't. cwm ships `.zip` for Windows, so
most people extract with Explorer and hit the warning.

> **Smart App Control is different.** On Windows 11 machines with *Smart App
> Control* turned **On**, unsigned apps from the internet can be blocked with
> **no** "Run anyway" button. The only per-app workaround is removing MOTW
> (see *Unblock* below) or building from source. SAC can't be re-enabled
> once you turn it off, so don't disable it just for cwm.

### Defender Antivirus deleting the file

This is a **false positive** from Defender's machine-learning heuristics.
Detection names usually end in `!ml`, for example
`Trojan:Win32/Wacatac.B!ml` or `Trojan:Win32/Bearfoos.A!ml`. Go programs
trigger these often because:

- they are large, statically linked single files with an unusual layout;
- release builds are stripped (`-s -w`), so there are few symbols or metadata;
- they are **unsigned** and **new**, with zero prevalence;
- malware authors also write in Go, so the ML models have learned "unsigned,
  new, stripped Go binary" as a weak risk signal.

The Go project documents this in its FAQ: <https://go.dev/doc/faq#virus>.

### Why it comes back with every release

Reputation is tied to the **file hash** for unsigned files. Every cwm release,
and every architecture (amd64 and arm64), is a different file with a different
hash, so **reputation starts from zero each release**. Even a false-positive
report Microsoft has cleared only covers that exact file. Code signing fixes
this part: once binaries are signed, reputation builds up on the **publisher
certificate** and carries over to later releases.

---

## Getting past it (user side)

First **check the download is genuine**. Compare its SHA-256 with
`checksums.txt` from the same GitHub Release:

```powershell
Get-FileHash .\cwm_*_windows_amd64.zip -Algorithm SHA256
# compare with the matching line in checksums.txt
```

Download only from <https://github.com/Mr-JoE1/cwm/releases>. If the hash
matches, use **one** of the options below.

### Option A: Unblock the zip *before* extracting (recommended)

This removes MOTW, so the extracted `cwm.exe` never gets tagged.

1. Right-click the downloaded `.zip` → **Properties**.
2. On the **General** tab, at the bottom, tick **Unblock** → **OK**.
   (If there's no Unblock checkbox, the file isn't tagged.)
3. Now extract it.

You can also do this on `cwm.exe` itself if you've already extracted it.

### Option B: PowerShell one-liner

Removes MOTW from the zip and/or the extracted exe:

```powershell
Unblock-File -Path .\cwm_*_windows_*.zip; Get-ChildItem -Recurse -Filter cwm.exe | Unblock-File
```

### Option C: Extract without Explorer

```powershell
tar -xf .\cwm_<version>_windows_amd64.zip
```

### Option D: "Run anyway" in the SmartScreen dialog

When the blue box appears, click **More info**, then **Run anyway**. Windows
remembers this choice for that file.

### Option E: Avoid downloaded binaries entirely

```sh
go install github.com/Mr-JoE1/cwm/cmd/cwm@latest
```

---

## If Defender deleted or quarantined `cwm.exe`

1. Open **Windows Security** → **Virus & threat protection** →
   **Protection history**. Find the cwm entry and note the **detection name**
   (e.g. `Trojan:Win32/Wacatac.B!ml`).
2. **Report the false positive to Microsoft.** This is what actually fixes it
   for everyone on that release:
   - Go to <https://www.microsoft.com/wdsi/filesubmission>
   - Choose **Home customer** (or *Enterprise customer* if your device is
     managed), sign in, upload `cwm.exe` (or the release `.zip`), select
     **Incorrectly detected as malware/malicious**, and paste the detection
     name.
   - Microsoft usually replies within a few days. Once it's cleared, update
     definitions (`Update-MpSignature` in an admin PowerShell) and download
     again.
3. If you trust the build (checksum matches) and need it right away, you can
   restore it from Protection history (**Actions → Allow on device**), or add a
   narrow exclusion just for the cwm folder:
   ```powershell
   # admin PowerShell. Keep the exclusion as narrow as possible.
   Add-MpPreference -ExclusionPath "$env:LOCALAPPDATA\Programs\cwm"
   ```
   On work machines, ask your IT team instead. Exclusions are often blocked
   by policy, and they should be.

Please also open an issue on the repo with the **detection name, cwm
version, and architecture**. That tells maintainers which builds to submit.

---

## Maintainer notes: the real fix is code signing

Signing does **not** make SmartScreen trust a file instantly. Since 2024, even
EV certificates have to build reputation. What signing does:

- shows a real publisher name instead of "Unknown publisher";
- lets reputation build up on the **certificate** across releases, instead of
  starting over with every new hash;
- noticeably lowers ML false-positive rates in Defender and other AV.

Options for an individual open-source maintainer, as of late 2026:

| Option | Cost | Who / where | Notes |
|---|---|---|---|
| **SignPath Foundation** (free OSS program) | Free | OSS projects with an OSI license, no proprietary parts, already released, actively maintained | Certificate is issued to *SignPath Foundation*, so the publisher shown is "SignPath Foundation", not you. Binaries must be built by CI from the public repo. Every release needs manual approval. You must publish a code-signing policy and use MFA. Acceptance is at their discretion, so expect a review and wait. Has a GitHub Actions integration. <https://signpath.org/> · terms <https://signpath.org/terms> · apply <https://signpath.org/apply> |
| **Certum Open Source Code Signing** | From **€49** (cloud, SimplySign) · **€69** (card + reader set) · **€25** (code only, if you already own a cryptoCertum card) | Natural persons only | Certificate reads "Open Source Developer, <Your Name>". Identity check uses an ID document plus proof of address (e.g. utility bill) and the URL of your OSS project. Since 27 Feb 2026, any code-signing cert is valid for at most 459 days, so multi-year purchases mean free reissues. The cloud version is easier to use from CI than a physical card. Shop: <https://shop.certum.eu/code-signing.html> · required docs: <https://support.certum.eu/en/code-signing-required-documents/> |
| **Azure Artifact Signing** (formerly Trusted Signing) | ~**$9.99/month** (Basic, 5,000 signatures) | **Individuals: USA and Canada only.** Organizations: US, CA, EU, UK and others | Needs a paid Azure subscription whose billing account type is *Individual*. Identity verification takes a few business days. Cheapest for eligible US/CA individuals; not available to individuals elsewhere. <https://learn.microsoft.com/en-us/azure/artifact-signing/quickstart> |
| OV certificate (Sectigo, DigiCert, GlobalSign, resellers) | ~$150–300/year | Worldwide | Key must sit on a hardware token or cloud HSM (CA/B Forum rule since 2023). Same SmartScreen behavior as the options above. |
| EV certificate | $400+/year | Worldwide | No longer bypasses SmartScreen (changed in 2024). Not worth the premium for this. |
| Self-signed | Free | — | Useless for public users. |

Microsoft's own comparison table:
<https://learn.microsoft.com/en-us/windows/apps/package-and-deploy/code-signing-options>

**Recommendation for cwm:** apply to **SignPath Foundation** first (free,
CI-native, fits a public MIT repo built by GitHub Actions). If you'd rather the
publisher show your own name, or SignPath declines, **Certum Open Source in the
cloud (~€49)** is the cheapest option with your name on it for an individual
outside the US/Canada.

Where a certificate plugs into the build: see the commented-out signing block
in [`.goreleaser.yaml`](../.goreleaser.yaml).

Cheap improvements that help even without signing:

- Embed a Windows version resource (product name, company, file version,
  description), e.g. with `github.com/tc-hib/go-winres` or
  `github.com/josephspurrier/goversioninfo`. Files with no metadata look more
  suspicious to the ML models.
- Never pack with UPX. Packed Go binaries get flagged far more often.
- Submit each new release's `cwm.exe` (amd64 and arm64) to
  <https://www.microsoft.com/wdsi/filesubmission> as a **Software developer**
  right after publishing, before users hit the problem.
