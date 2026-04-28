# Signs a Windows binary with a PFX cert + RFC 3161 timestamp.
#
# This is the same script the release workflow runs in CI; it's also
# usable locally:
#   pwsh -File scripts/sign/sign.ps1 -Binary blacknode.exe -PfxPath build/sign/code-signing.pfx -Password (Read-Host -AsSecureString)

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$Binary,
    [Parameter(Mandatory = $true)][string]$PfxPath,
    [Parameter(Mandatory = $true)][SecureString]$Password,
    # Public, free RFC 3161 timestamp authorities. Sectigo first; DigiCert
    # falls back if Sectigo is unreachable. Timestamps make the signature
    # remain valid AFTER the cert expires — drop these and your binaries
    # become unverifiable on cert expiry.
    [string[]]$TimestampUrls = @(
        "http://timestamp.sectigo.com",
        "http://timestamp.digicert.com"
    )
)

$ErrorActionPreference = "Stop"

if (-not (Test-Path $Binary)) {
    throw "Binary not found: $Binary"
}
if (-not (Test-Path $PfxPath)) {
    throw "PFX not found: $PfxPath"
}

# Locate signtool. Newer Windows SDKs put it under Program Files (x86).
$signtool = $null
$searchRoots = @(
    "${env:ProgramFiles(x86)}\Windows Kits\10\bin",
    "${env:ProgramFiles}\Windows Kits\10\bin"
)
foreach ($root in $searchRoots) {
    if (Test-Path $root) {
        $candidate = Get-ChildItem -Path $root -Recurse -Filter signtool.exe -ErrorAction SilentlyContinue |
            Where-Object { $_.FullName -match 'x64\\signtool\.exe$' } |
            Select-Object -First 1
        if ($candidate) { $signtool = $candidate.FullName; break }
    }
}
if (-not $signtool) {
    # PATH fallback (the GitHub Actions windows-latest runner has it on PATH).
    $signtool = (Get-Command signtool.exe -ErrorAction SilentlyContinue)?.Source
}
if (-not $signtool) {
    throw "signtool.exe not found. Install the Windows SDK or run this on a runner that ships it."
}
Write-Host "==> signtool: $signtool" -ForegroundColor Cyan

# SecureString -> plain string only inside the signtool argv. Cleared
# after the call.
$bstr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($Password)
$pwPlain = [Runtime.InteropServices.Marshal]::PtrToStringAuto($bstr)
[Runtime.InteropServices.Marshal]::ZeroFreeBSTR($bstr)

$signed = $false
foreach ($ts in $TimestampUrls) {
    Write-Host "==> Signing $Binary (timestamp: $ts)" -ForegroundColor Cyan
    & $signtool sign /fd SHA256 /td SHA256 /tr $ts /f $PfxPath /p $pwPlain $Binary
    if ($LASTEXITCODE -eq 0) {
        $signed = $true
        break
    }
    Write-Warning "signtool failed against $ts (exit $LASTEXITCODE); trying next timestamp authority."
}

# Best-effort wipe.
$pwPlain = $null
[GC]::Collect()

if (-not $signed) {
    throw "All timestamp authorities failed; binary not signed."
}

Write-Host "==> Verifying signature" -ForegroundColor Cyan
& $signtool verify /pa /v $Binary
if ($LASTEXITCODE -ne 0) {
    throw "Signature verification failed (exit $LASTEXITCODE)."
}

Write-Host "==> Signed successfully." -ForegroundColor Green
