$ErrorActionPreference = "Stop"

$repoRoot = Split-Path -Parent $PSScriptRoot

function Resolve-Executable {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][string[]]$Candidates
    )

    $command = Get-Command $Name -ErrorAction SilentlyContinue
    if ($command) {
        return $command.Source
    }
    foreach ($candidate in $Candidates) {
        $path = Join-Path $repoRoot $candidate
        if (Test-Path -LiteralPath $path) {
            return (Resolve-Path -LiteralPath $path).Path
        }
    }
    throw "Cannot find $Name. Install it or place it under the repository .tools directory."
}

$go = Resolve-Executable -Name "go" -Candidates @(".tools/go/bin/go.exe")
$gcc = Resolve-Executable -Name "gcc" -Candidates @(
    ".tools/msys64/mingw64/bin/gcc.exe",
    ".tools/msys64/ucrt64/bin/gcc.exe"
)
$gxx = Resolve-Executable -Name "g++" -Candidates @(
    ".tools/msys64/mingw64/bin/g++.exe",
    ".tools/msys64/ucrt64/bin/g++.exe"
)
$gccBin = Split-Path -Parent $gcc
$gccRoot = Split-Path -Parent $gccBin
$compilerRoot = Join-Path $gccRoot "lib/gcc/x86_64-w64-mingw32"
$compilerDir = Get-ChildItem -LiteralPath $compilerRoot -Directory |
    Sort-Object Name -Descending |
    Select-Object -First 1
if (-not $compilerDir) {
    throw "Cannot find GCC internal compiler under $compilerRoot."
}

$env:CGO_ENABLED = "1"
$env:CC = $gcc.Replace("\", "/")
$env:CXX = $gxx.Replace("\", "/")
$env:COMPILER_PATH = $compilerDir.FullName
$env:Path = "$gccBin;$($compilerDir.FullName);$env:Path"
# pg_query_go has a large generated C translation unit. A single compiler job
# is slower but avoids resource stalls seen on Windows during a cold build.
$env:GOMAXPROCS = "1"

$localGoPath = Join-Path $repoRoot ".tools/gopath"
if (Test-Path -LiteralPath (Join-Path $localGoPath "pkg/mod")) {
    $env:GOPATH = $localGoPath
    $env:GOMODCACHE = Join-Path $localGoPath "pkg/mod"
}
foreach ($cache in @(@("GOCACHE", ".tools/gocache"), @("GOTMPDIR", ".tools/gotmp"))) {
    $cachePath = Join-Path $repoRoot $cache[1]
    if (Test-Path -LiteralPath $cachePath) {
        Set-Item -Path "Env:$($cache[0])" -Value $cachePath
    }
}
if ($env:GOTMPDIR) {
    $env:TEMP = $env:GOTMPDIR
    $env:TMP = $env:GOTMPDIR
}

Push-Location $repoRoot
try {
    & $go test -count=1 ./internal/plugin/... ./plugin/proto ./examples/plugins/...
    if ($LASTEXITCODE -ne 0) { throw "Plugin contract tests failed." }

    & $go test -count=1 ./internal/application/service `
        -run '^TestPluginIncrementalSyncOnlyReprocessesChangedFile$'
    if ($LASTEXITCODE -ne 0) { throw "Incremental datasource acceptance test failed." }

    if ((Get-Command npm.cmd -ErrorAction SilentlyContinue) -and
        (Test-Path -LiteralPath (Join-Path $repoRoot "frontend/node_modules"))) {
        Push-Location (Join-Path $repoRoot "frontend")
        try {
            & npm.cmd run type-check
            if ($LASTEXITCODE -ne 0) { throw "Frontend type check failed." }
        }
        finally {
            Pop-Location
        }
    }

    Write-Host "Windows plugin acceptance passed." -ForegroundColor Green
}
finally {
    Pop-Location
}
