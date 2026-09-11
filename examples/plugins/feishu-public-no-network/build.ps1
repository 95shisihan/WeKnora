param([string]$TestURL = '')
$ErrorActionPreference = 'Stop'
$repo = (Resolve-Path (Join-Path $PSScriptRoot '../../..')).Path
$go = Join-Path $repo '.tools/go/bin/go.exe'
if (-not (Test-Path -LiteralPath $go)) { $go = (Get-Command go -ErrorAction Stop).Source }
$names = @('CGO_ENABLED','GOPATH','GOMODCACHE','GOCACHE','GOTMPDIR','WEKNORA_WINDOWS_SANDBOX_TEST','WEKNORA_FEISHU_DENIED_EXE','WEKNORA_FEISHU_TEST_URL')
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
    $stage = Join-Path $repo 'artifacts/feishu-public-no-network-windows-x64-0.1.0'
    New-Item -ItemType Directory -Force (Join-Path $stage 'bin') | Out-Null
    $exe = Join-Path $stage 'bin/weknora-feishu-public-no-network.exe'
    & $go build -o $exe ./examples/plugins/feishu-public-no-network
    if ($LASTEXITCODE -ne 0) { throw 'Plugin build failed' }
    if ($TestURL) {
        $env:WEKNORA_WINDOWS_SANDBOX_TEST = '1'
        $env:WEKNORA_FEISHU_DENIED_EXE = $exe
        $env:WEKNORA_FEISHU_TEST_URL = $TestURL
    } else {
        $env:WEKNORA_WINDOWS_SANDBOX_TEST = '0'
    }
    & $go test -count=1 -v ./examples/plugins/feishu-public-no-network
    if ($LASTEXITCODE -ne 0) { throw 'Plugin tests failed' }
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'plugin.yaml') -Destination $stage
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'README.md') -Destination $stage
    $zip = Join-Path $repo 'artifacts/plugins/feishu-public-no-network-windows-x64-0.1.0.zip'
    New-Item -ItemType Directory -Force (Split-Path $zip) | Out-Null
    Compress-Archive -Path (Join-Path $stage '*') -DestinationPath $zip -Force
    (Get-FileHash -LiteralPath $zip -Algorithm SHA256).Hash.ToLowerInvariant() | Set-Content -LiteralPath ($zip + '.sha256') -Encoding ascii
    Write-Host "Built $zip"
} finally {
    Pop-Location
    foreach ($name in $names) { [Environment]::SetEnvironmentVariable($name, $saved[$name], 'Process') }
}
