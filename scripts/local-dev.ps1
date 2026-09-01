[CmdletBinding()]
param(
    [ValidateSet('start', 'stop', 'restart', 'status', 'build')]
    [string]$Action = 'start',
    [switch]$Rebuild,
    [switch]$NoDocReader
)

$ErrorActionPreference = 'Stop'
$projectRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$toolsDir = Join-Path $projectRoot '.tools'
$goDir = Join-Path $toolsDir 'go'
$nodeDir = Join-Path $toolsDir 'node'
$msysDir = Join-Path $toolsDir 'msys64'
$pythonDir = Join-Path $toolsDir 'conda-py310'
$binDir = Join-Path $toolsDir 'bin'
$runDir = Join-Path $toolsDir 'run'
$logDir = Join-Path $toolsDir 'logs'
$backendExe = Join-Path $binDir 'weknora-lite-dev.exe'
$pidFile = Join-Path $runDir 'processes.json'
$envFile = Join-Path $projectRoot '.env.lite'

function Assert-LocalTools {
    $required = @(
        (Join-Path $goDir 'bin\go.exe'),
        (Join-Path $nodeDir 'node.exe'),
        (Join-Path $nodeDir 'npm.cmd'),
        (Join-Path $msysDir 'ucrt64\bin\gcc.exe'),
        (Join-Path $pythonDir 'python.exe'),
        $envFile
    )
    $missing = @($required | Where-Object { -not (Test-Path -LiteralPath $_) })
    if ($missing.Count -gt 0) {
        throw "Local tool installation is incomplete:`n$($missing -join "`n")"
    }
}

function Import-DotEnv([string]$Path) {
    foreach ($line in Get-Content -LiteralPath $Path) {
        $trimmed = $line.Trim()
        if (-not $trimmed -or $trimmed.StartsWith('#') -or -not $trimmed.Contains('=')) {
            continue
        }
        $name, $value = $trimmed.Split('=', 2)
        Set-Item -Path "Env:$($name.Trim())" -Value $value.Trim()
    }
}

function Set-LocalEnvironment {
    Assert-LocalTools
    Import-DotEnv $envFile

    $localPaths = @(
        (Join-Path $goDir 'bin'),
        $nodeDir,
        (Join-Path $msysDir 'ucrt64\bin'),
        (Join-Path $msysDir 'usr\bin'),
        $pythonDir,
        (Join-Path $pythonDir 'Scripts')
    )
    $env:PATH = ($localPaths -join ';') + ';' + $env:PATH
    $env:GOROOT = $goDir
    $env:GOPATH = Join-Path $toolsDir 'gopath'
    $env:GOMODCACHE = Join-Path $env:GOPATH 'pkg\mod'
    $env:GOCACHE = Join-Path $toolsDir 'go-cache'
    $env:GOPROXY = 'https://goproxy.cn,direct'
    # Go 1.26 can stall silently on this very large CGO graph under Windows;
    # verbose mode keeps its subprocess pipes draining reliably.
    $env:GOFLAGS = '-p=1 -x'
    $env:GOMAXPROCS = '4'
    $env:CGO_ENABLED = '1'
    $env:CC = Join-Path $msysDir 'ucrt64\bin\gcc.exe'
    $env:CGO_CFLAGS = '-Wno-deprecated-declarations'
    $env:PKG_CONFIG_PATH = Join-Path $msysDir 'ucrt64\lib\pkgconfig'
    $env:npm_config_cache = Join-Path $toolsDir 'npm-cache'
    $env:UV_CACHE_DIR = Join-Path $toolsDir 'uv-cache'
    $env:PLAYWRIGHT_BROWSERS_PATH = Join-Path $toolsDir 'playwright-browsers'
    $env:VITE_DEV_PROXY_TARGET = 'http://127.0.0.1:8080'
    $env:PYTHONPATH = $projectRoot
    $env:OLLAMA_MODELS = Join-Path $toolsDir 'ollama-models'
}

function Ensure-Directories {
    @($binDir, $runDir, $logDir, (Join-Path $projectRoot 'data'), (Join-Path $projectRoot 'data\files')) |
        ForEach-Object { New-Item -ItemType Directory -Force -Path $_ | Out-Null }
}

function Build-Backend {
    Set-LocalEnvironment
    Ensure-Directories
    Push-Location $projectRoot
    try {
        $goExe = Join-Path $goDir 'bin\go.exe'
        $ldflags = '-X github.com/Tencent/WeKnora/internal/handler.Version=0.7.2-dev ' +
            '-X github.com/Tencent/WeKnora/internal/handler.Edition=lite ' +
            '-X google.golang.org/protobuf/reflect/protoregistry.conflictPolicy=warn'
        Write-Host 'Building Lite backend with the project-local Go and GCC...'
        & $goExe build -tags sqlite_fts5 -ldflags $ldflags -o $backendExe ./cmd/server
        if ($LASTEXITCODE -ne 0) { throw "Go build failed with exit code $LASTEXITCODE" }
    }
    finally {
        Pop-Location
    }
}

function Ensure-FrontendDependencies {
    $viteCli = Join-Path $projectRoot 'frontend\node_modules\vite\bin\vite.js'
    if (Test-Path -LiteralPath $viteCli) { return }

    Write-Host 'Installing frontend dependencies with the project-local Node.js...'
    Push-Location (Join-Path $projectRoot 'frontend')
    try {
        & (Join-Path $nodeDir 'npm.cmd') ci
        if ($LASTEXITCODE -ne 0) { throw "npm ci failed with exit code $LASTEXITCODE" }
    }
    finally {
        Pop-Location
    }
}

function Read-ProcessTable {
    if (-not (Test-Path -LiteralPath $pidFile)) { return @{} }
    try {
        $data = Get-Content -LiteralPath $pidFile -Raw | ConvertFrom-Json
        $table = @{}
        foreach ($property in $data.PSObject.Properties) {
            $table[$property.Name] = [int]$property.Value
        }
        return $table
    }
    catch {
        return @{}
    }
}

function Write-ProcessTable([hashtable]$Table) {
    $Table | ConvertTo-Json | Set-Content -LiteralPath $pidFile -Encoding UTF8
}

function Test-ProcessId([int]$Id) {
    return $null -ne (Get-Process -Id $Id -ErrorAction SilentlyContinue)
}

function Start-LoggedProcess(
    [string]$Name,
    [string]$Executable,
    [string[]]$Arguments,
    [string]$WorkingDirectory,
    [hashtable]$Table
) {
    if ($Table.ContainsKey($Name) -and (Test-ProcessId $Table[$Name])) {
        Write-Host "$Name is already running (PID $($Table[$Name]))."
        return
    }

    $stdout = Join-Path $logDir "$Name.out.log"
    $stderr = Join-Path $logDir "$Name.err.log"
    $startParameters = @{
        FilePath               = $Executable
        WorkingDirectory       = $WorkingDirectory
        WindowStyle            = 'Hidden'
        PassThru               = $true
        RedirectStandardOutput = $stdout
        RedirectStandardError  = $stderr
    }
    if ($null -ne $Arguments -and $Arguments.Count -gt 0) {
        $startParameters.ArgumentList = $Arguments
    }
    $process = Start-Process @startParameters
    $Table[$Name] = $process.Id
    Write-Host "Started $Name (PID $($process.Id)); logs: $stdout"
}

function Start-Services {
    Set-LocalEnvironment
    Ensure-Directories
    if ($Rebuild -or -not (Test-Path -LiteralPath $backendExe)) {
        Build-Backend
    }
    Ensure-FrontendDependencies

    $table = Read-ProcessTable
    Start-LoggedProcess 'backend' $backendExe @() $projectRoot $table

    $nodeExe = Join-Path $nodeDir 'node.exe'
    $viteCli = Join-Path $projectRoot 'frontend\node_modules\vite\bin\vite.js'
    # Use a dedicated fresh origin and force a clean Vite dependency graph.
    # This avoids Edge reusing stale dev-module state from the old :5173 origin.
    Start-LoggedProcess 'frontend' $nodeExe @($viteCli, '--host', '127.0.0.1', '--port', '5174', '--force') (Join-Path $projectRoot 'frontend') $table

    if (-not $NoDocReader) {
        $pythonExe = Join-Path $pythonDir 'python.exe'
        Start-LoggedProcess 'docreader' $pythonExe @((Join-Path $projectRoot 'docreader\main.py')) (Join-Path $projectRoot 'docreader') $table
    }

    Write-ProcessTable $table
    Start-Sleep -Seconds 2
    Show-Status
}

function Stop-Services {
    Ensure-Directories
    $table = Read-ProcessTable
    foreach ($name in @('frontend', 'backend', 'docreader')) {
        if (-not $table.ContainsKey($name)) { continue }
        $processId = [int]$table[$name]
        if (Test-ProcessId $processId) {
            Stop-Process -Id $processId -Force
            Write-Host "Stopped $name (PID $processId)."
        }
        $table.Remove($name)
    }
    Write-ProcessTable $table
}

function Show-Status {
    Ensure-Directories
    $table = Read-ProcessTable
    foreach ($name in @('backend', 'frontend', 'docreader')) {
        if ($table.ContainsKey($name) -and (Test-ProcessId $table[$name])) {
            Write-Host ("{0,-10} running  PID {1}" -f $name, $table[$name]) -ForegroundColor Green
        }
        else {
            Write-Host ("{0,-10} stopped" -f $name) -ForegroundColor Yellow
        }
    }
    Write-Host 'Frontend: http://127.0.0.1:5174'
    Write-Host 'Backend:  http://127.0.0.1:8080/health'
}

switch ($Action) {
    'start'   { Start-Services }
    'stop'    { Stop-Services }
    'restart' { Stop-Services; Start-Services }
    'status'  { Show-Status }
    'build'   { Build-Backend }
}
