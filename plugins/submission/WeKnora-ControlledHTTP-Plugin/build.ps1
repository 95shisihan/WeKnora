param([string]$Go = 'go')
$ErrorActionPreference = 'Stop'
Push-Location $PSScriptRoot
$previousCGO = $env:CGO_ENABLED
try {
    $env:CGO_ENABLED = '0'
    if ((& $Go env GOOS).Trim() -ne 'windows' -or (& $Go env GOARCH).Trim() -ne 'amd64') { throw 'Windows amd64 build required' }
    & $Go test -mod=mod -count=1 ./...
    if ($LASTEXITCODE -ne 0) { throw 'Plugin tests failed' }
    $stage = Join-Path $PSScriptRoot 'dist/controlled-http-0.1.0'
    New-Item -ItemType Directory -Force (Join-Path $stage 'bin') | Out-Null
    & $Go build -mod=mod -trimpath -o (Join-Path $stage 'bin/weknora-controlled-http.exe') .
    if ($LASTEXITCODE -ne 0) { throw 'Plugin build failed' }
    Copy-Item -LiteralPath 'plugin.yaml' -Destination (Join-Path $stage 'plugin.yaml')
    $archive = Join-Path $PSScriptRoot 'dist/controlled-http-windows-x64-0.1.0.zip'
    Compress-Archive -Path (Join-Path $stage '*') -DestinationPath $archive -Force
    (Get-FileHash -LiteralPath $archive).Hash.ToLowerInvariant() | Set-Content -LiteralPath ($archive + '.sha256') -Encoding ascii
    Write-Host "Built $archive"
} finally { $env:CGO_ENABLED = $previousCGO; Pop-Location }
