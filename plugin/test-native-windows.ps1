param([switch]$RequireAudit)
$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$go = Join-Path $repoRoot '.tools/go/bin/go.exe'
if (-not (Test-Path -LiteralPath $go)) { $go = (Get-Command go -ErrorAction Stop).Source }
$names = @('CGO_ENABLED', 'WEKNORA_WINDOWS_SANDBOX_TEST', 'WEKNORA_REQUIRE_WFP_AUDIT')
$saved = @{}
foreach ($name in $names) { $saved[$name] = [Environment]::GetEnvironmentVariable($name, 'Process') }
Push-Location $repoRoot
try {
    $env:CGO_ENABLED = '0'
    $env:WEKNORA_WINDOWS_SANDBOX_TEST = '1'
    $env:WEKNORA_REQUIRE_WFP_AUDIT = if ($RequireAudit) { '1' } else { '0' }
    & $go test -count=1 -v -timeout=90s ./internal/plugin/windowsandbox ./plugin/sdk/transport
    if ($LASTEXITCODE -ne 0) { throw 'Native Windows plugin acceptance failed.' }
    Write-Host 'Native Windows isolation and pipe transport passed. External directory sync runs separately with WEKNORA_NATIVE_DIRECTORY_EXE.'
}
finally {
    Pop-Location
    foreach ($name in $names) { [Environment]::SetEnvironmentVariable($name, $saved[$name], 'Process') }
}
