# Generates a self-signed code-signing certificate, exports it to a
# password-protected PFX, and prints the base64 form ready to paste into
# a GitHub Actions secret.
#
# IMPORTANT — what this does NOT do:
#   - It does NOT bypass Windows SmartScreen for end users. Self-signed
#     certs don't chain to a Microsoft-trusted root, so SmartScreen still
#     flags the download as "Unknown publisher / unrecognized app".
#     Bypassing SmartScreen on first run requires either an EV
#     Authenticode cert or Microsoft Trusted Signing.
#
# What it DOES do:
#   - Replaces "Unknown publisher" with your declared publisher name in
#     UAC and properties dialogs.
#   - Lets advanced users / enterprise admins manually import the cert
#     into the Trusted Publishers store, after which Windows treats the
#     binary as fully signed.
#   - Provides a stable identity that future downloads use, so users who
#     trusted you once trust subsequent versions automatically.
#
# Usage:
#   pwsh -File scripts/sign/generate-cert.ps1 -Subject "CN=Blacknode Self-Signed" -Password (Read-Host -AsSecureString)
# or non-interactive:
#   $pw = ConvertTo-SecureString "your-password" -AsPlainText -Force
#   pwsh -File scripts/sign/generate-cert.ps1 -Password $pw

[CmdletBinding()]
param(
    [string]$Subject = "CN=Blacknode Self-Signed",
    [Parameter(Mandatory = $true)]
    [SecureString]$Password,
    [string]$OutDir = "build/sign",
    [int]$ValidYears = 5
)

$ErrorActionPreference = "Stop"

if (-not (Test-Path $OutDir)) {
    New-Item -ItemType Directory -Path $OutDir -Force | Out-Null
}

Write-Host "==> Generating self-signed code-signing cert" -ForegroundColor Cyan
$cert = New-SelfSignedCertificate `
    -Subject $Subject `
    -Type CodeSigningCert `
    -KeyAlgorithm RSA `
    -KeyLength 2048 `
    -HashAlgorithm SHA256 `
    -KeyExportPolicy Exportable `
    -KeyUsage DigitalSignature `
    -CertStoreLocation "Cert:\CurrentUser\My" `
    -NotAfter (Get-Date).AddYears($ValidYears)

$pfxPath = Join-Path $OutDir "code-signing.pfx"
Export-PfxCertificate -Cert $cert -FilePath $pfxPath -Password $Password | Out-Null
Write-Host "==> PFX exported: $pfxPath" -ForegroundColor Green

$cerPath = Join-Path $OutDir "code-signing.cer"
Export-Certificate -Cert $cert -FilePath $cerPath -Type CERT | Out-Null
Write-Host "==> Public cert (for users who want to trust it): $cerPath" -ForegroundColor Green

# Base64-encode the PFX for the GitHub Actions secret.
$pfxBase64 = [Convert]::ToBase64String([IO.File]::ReadAllBytes($pfxPath))
$base64Path = Join-Path $OutDir "code-signing.pfx.base64.txt"
Set-Content -Path $base64Path -Value $pfxBase64 -Encoding ascii
Write-Host "==> Base64 PFX written to: $base64Path" -ForegroundColor Green

Write-Host ""
Write-Host "Thumbprint: $($cert.Thumbprint)" -ForegroundColor Yellow
Write-Host ""
Write-Host "Next steps:" -ForegroundColor Cyan
Write-Host "  1. In GitHub repo Settings -> Secrets -> Actions, add:" -ForegroundColor Gray
Write-Host "       WINDOWS_SIGN_PFX_BASE64  = (paste contents of code-signing.pfx.base64.txt)" -ForegroundColor Gray
Write-Host "       WINDOWS_SIGN_PASSWORD    = (the password you supplied)" -ForegroundColor Gray
Write-Host "  2. Commit code-signing.cer to the repo (it's the public half — safe to publish)." -ForegroundColor Gray
Write-Host "  3. Tell users: to silence the warning, double-click code-signing.cer" -ForegroundColor Gray
Write-Host "     and install it into 'Trusted Root Certification Authorities'." -ForegroundColor Gray
Write-Host ""
Write-Host "DO NOT commit code-signing.pfx or code-signing.pfx.base64.txt — both contain the private key." -ForegroundColor Red
