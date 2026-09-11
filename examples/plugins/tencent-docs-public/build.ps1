$ErrorActionPreference = 'Stop'
$repo = (Resolve-Path (Join-Path $PSScriptRoot '../../..')).Path
$go = Join-Path $repo '.tools/go/bin/go.exe'
if (-not (Test-Path -LiteralPath $go)) { $go = (Get-Command go -ErrorAction Stop).Source }
$names = @('CGO_ENABLED','GOPATH','GOMODCACHE','GOCACHE','GOTMPDIR')
$saved = @{}
foreach ($name in $names) { $saved[$name] = [Environment]::GetEnvironmentVariable($name, 'Process') }
Push-Location $repo
try {
    $env:CGO_ENABLED = '0'
    if (Test-Path '.tools/gopath/pkg/mod') {
        $env:GOPATH = Join-Path $repo '.tools/gopath'
        $env:GOMODCACHE = Join-Path $env:GOPATH 'pkg/mod'
        $env:GOCACHE = Join-Path $repo '.tools/gocache'
        $env:GOTMPDIR = Join-Path $repo '.tools/gotmp'
    }
    $platform = (& $go env GOOS).Trim()
    $arch = (& $go env GOARCH).Trim()
    if ($platform -ne 'windows' -or $arch -ne 'amd64') { throw 'This package manifest targets Windows amd64.' }
    $stage = Join-Path $repo 'artifacts/tencent-docs-public-windows-x64-0.2.0'
    New-Item -ItemType Directory -Force (Join-Path $stage 'bin') | Out-Null
    & $go build -o (Join-Path $stage 'bin/weknora-tencent-docs-public.exe') ./examples/plugins/tencent-docs-public
    if ($LASTEXITCODE -ne 0) { throw 'Plugin build failed' }
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'plugin.yaml') -Destination $stage
    $zip = Join-Path $repo 'artifacts/plugins/tencent-docs-public-windows-x64-0.2.0.zip'
    New-Item -ItemType Directory -Force (Split-Path $zip) | Out-Null
    Compress-Archive -Path (Join-Path $stage '*') -DestinationPath $zip -Force
    (Get-FileHash -LiteralPath $zip -Algorithm SHA256).Hash.ToLowerInvariant() | Set-Content -LiteralPath ($zip + '.sha256') -Encoding ascii
    Write-Host "Built $zip"
} finally {
    Pop-Location
    foreach ($name in $names) { [Environment]::SetEnvironmentVariable($name, $saved[$name], 'Process') }
}
