#requires -Version 7.2
[CmdletBinding()]
param([string]$KeyFile = (Join-Path $PSScriptRoot '../data/secrets/iam-private.pem'))
$ErrorActionPreference = 'Stop'
$path = [IO.Path]::GetFullPath($KeyFile)
if (Test-Path -LiteralPath $path) { Write-Output "Using existing IAM key: $path"; return }
New-Item -ItemType Directory -Force -Path (Split-Path -Parent $path) | Out-Null
$rsa = [Security.Cryptography.RSA]::Create(3072)
try {
    $pem = $rsa.ExportPkcs8PrivateKeyPem()
    # CreateNew prevents two initializers from replacing one another's key.
    $stream = [IO.File]::Open($path, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write)
    try { $bytes = [Text.Encoding]::ASCII.GetBytes($pem); $stream.Write($bytes) } finally { $stream.Dispose() }
} finally { $rsa.Dispose() }
Write-Output "Created persistent IAM key: $path"
