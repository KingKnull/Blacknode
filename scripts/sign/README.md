# Self-signed code signing for Windows builds

These scripts produce a self-signed Authenticode certificate and use it
to sign `blacknode.exe`. The release workflow picks the cert up
automatically when the matching GitHub Actions secrets are present.

## Important — what self-signing does and doesn't do

| Behavior                                              | Self-signed | Standard Authenticode | EV / Microsoft Trusted Signing |
| ----------------------------------------------------- | ----------- | --------------------- | ------------------------------ |
| Replaces "Unknown publisher" with declared name       | ✅          | ✅                    | ✅                             |
| Bypasses Windows SmartScreen on first download        | ❌          | ❌ (until reputation) | ✅                             |
| Avoids antivirus / EDR false positives                | ❌          | mostly ✅             | ✅                             |
| Acceptable for users who manually trust the cert      | ✅          | ✅                    | ✅                             |
| Works for unattended enterprise rollouts (GPO trust)  | ✅          | ✅                    | ✅                             |

**TL;DR**: a self-signed cert does not silence the SmartScreen "Windows
protected your PC" dialog for end users. They will still see a warning
and have to click "More info → Run anyway" the first time. The dialog
will, however, show your publisher name instead of "Unknown publisher",
and users (or their IT admins) can install the public `.cer` into the
Trusted Root Certification Authorities store to silence the warning
permanently.

## One-time setup

1. Generate a self-signed cert and base64-encode the PFX:

   ```powershell
   pwsh -File scripts/sign/generate-cert.ps1 -Subject "CN=Your Name" -Password (Read-Host -AsSecureString)
   ```

   This drops three files into `build/sign/`:

   - `code-signing.pfx` — private key + cert. **Never commit.**
   - `code-signing.pfx.base64.txt` — the PFX as base64 for the GitHub
     secret. **Never commit.**
   - `code-signing.cer` — public half. Safe to commit and publish.

2. In your GitHub repo: **Settings → Secrets and variables → Actions** →
   add two repository secrets:

   - `WINDOWS_SIGN_PFX_BASE64` — paste the entire contents of
     `code-signing.pfx.base64.txt`.
   - `WINDOWS_SIGN_PASSWORD` — the password you supplied to the script.

3. Commit `code-signing.cer` to the repo so users have a way to fetch
   the public cert and pre-trust it if they want.

After step 2 the next `git push --tags v...` will produce a signed
`.exe` automatically.

## Local signing

If you want to sign a binary you built locally (e.g. for an internal
release):

```powershell
pwsh -File scripts/sign/sign.ps1 `
    -Binary blacknode.exe `
    -PfxPath build/sign/code-signing.pfx `
    -Password (Read-Host -AsSecureString)
```

## Telling users how to silence the warning

Drop this into your release notes:

> **First-run warning on Windows**
>
> blacknode is signed with a self-signed certificate, so Windows
> SmartScreen will warn you the first time you run it. You can:
>
> - Click "More info → Run anyway" to launch this once, or
> - (Recommended) Download `code-signing.cer` from the repo, double-click
>   it, choose "Install Certificate" → "Local Machine" → "Trusted Root
>   Certification Authorities". Subsequent versions will run without
>   warnings.

## When to graduate to a real cert

If you start shipping to non-technical users at any volume, a real
Authenticode cert (~$200–500/yr) or Microsoft Trusted Signing
(~$10/mo + identity verification) is worth the cost — both bypass
SmartScreen reliably without users having to install anything.
