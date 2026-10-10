# check.ps1: every check, in one command. CI runs this exact script too, so
# "it passes here" means "it passes in CI".
#
#   .\scripts\check.ps1           everything (what CI runs)
#   .\scripts\check.ps1 -Quick    skip staticcheck, govulncheck and the database
#                                 tests: a fast check while you work
#   .\scripts\check.ps1 -Fuzz     everything, then fuzz each target for a while
#   .\scripts\check.ps1 -Fuzz -FuzzSeconds 120
#   .\scripts\check.ps1 -Skip staticcheck    leave out one tool (or several,
#                                 comma-separated), e.g. while it's broken upstream
#
# It stops at the first failure, cheapest checks first, like CI.
# Works in Windows PowerShell 5.1 and PowerShell 7 (pwsh, which CI uses).

param(
    [switch]$Quick,
    [switch]$Fuzz,
    [int]$FuzzSeconds = 20,
    [ValidateSet('staticcheck', 'govulncheck')]
    [string[]]$Skip = @()
)

$ErrorActionPreference = 'Stop'

# Run from the project root wherever the script was started from, and put the
# caller back where they were afterwards.
$startedIn = Get-Location
Set-Location (Split-Path -Parent $PSScriptRoot)

$inCI = [bool]$env:GITHUB_ACTIONS
$total = [Diagnostics.Stopwatch]::StartNew()

function Finish([int]$code) {
    Set-Location $startedIn
    exit $code
}

# Runs a program and throws if it fails. (PowerShell doesn't treat a
# program's non-zero exit code as an error by itself.)
function Invoke-Native {
    $exe, $rest = $args
    & $exe @rest
    if ($LASTEXITCODE -ne 0) {
        throw "'$exe $rest' failed (exit code $LASTEXITCODE)"
    }
}

# One named step: prints a header, times it, and stops everything on failure.
# In CI, each step becomes a collapsible section in the log.
# (The parameter names are unusual on purpose: the step's script block can see
# this function's variables, so a plain $Name would hide any $name it uses.)
function Step([string]$StepName, [scriptblock]$StepBody) {
    if ($inCI) { Write-Host "::group::$StepName" } else { Write-Host "`n==> $StepName" -ForegroundColor Cyan }
    $sw = [Diagnostics.Stopwatch]::StartNew()
    try {
        & $StepBody
    } catch {
        if ($inCI) { Write-Host "::endgroup::"; Write-Host "::error::$StepName failed: $($_.Exception.Message)" }
        Write-Host "`nFAILED: $StepName" -ForegroundColor Red
        Write-Host $_.Exception.Message -ForegroundColor Red
        Finish 1
    }
    if ($inCI) { Write-Host "::endgroup::" }
    Write-Host ("    ok: $StepName ({0:N1}s)" -f $sw.Elapsed.TotalSeconds) -ForegroundColor Green
}

function Note([string]$text) { Write-Host "    note: $text" -ForegroundColor Yellow }

# ---- the database ----

# Locally, make sure the Postgres container is up and healthy (--wait waits
# for compose.yaml's healthcheck). CI has its own Postgres service instead.
if (-not $Quick -and -not $inCI) {
    Step "Database (docker compose up)" {
        if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
            throw "docker isn't installed or isn't on PATH (or run with -Quick to skip the database tests)"
        }
        Invoke-Native docker compose up -d --wait
    }
}

# ---- cheap checks first ----

Step "Formatting (gofmt)" {
    $unformatted = gofmt -l .
    if ($LASTEXITCODE -ne 0) { throw "gofmt couldn't read some files" }
    if ($unformatted) {
        throw "These files need 'go fmt ./...':`n  $($unformatted -join "`n  ")"
    }
}

Step "Modules are tidy" { Invoke-Native go mod tidy -diff }

Step "Vet" { Invoke-Native go vet ./... }

# ---- slower checks (they download the tools the first time) ----

if ($Quick) { $Skip += 'staticcheck', 'govulncheck' }

# A stricter checker than vet: unused code, suspicious patterns,
# deprecated functions, simplifications.
if ($Skip -contains 'staticcheck') {
    Note "skipping staticcheck"
} else {
    Step "Staticcheck" { Invoke-Native go run honnef.co/go/tools/cmd/staticcheck@latest ./... }
}

# Known security holes in dependencies and the Go standard library, for
# code Snowball actually calls.
if ($Skip -contains 'govulncheck') {
    Note "skipping govulncheck"
} else {
    Step "Known vulnerabilities (govulncheck)" { Invoke-Native go run golang.org/x/vuln/cmd/govulncheck@latest ./... }
}

Step "Build" { Invoke-Native go build ./... }

# ---- tests ----

# The race detector catches goroutines touching shared data without a lock.
# It needs cgo, which needs a C compiler: Linux (and so CI) has one; Windows
# usually doesn't, unless you install one (e.g. MinGW-w64 via MSYS2). Go
# turns cgo off by itself when it finds no compiler, so ask Go.
$testArgs = @('test', '-count=1')   # -count=1: never reuse cached results
if ((go env CGO_ENABLED) -eq '1') {
    $testArgs += '-race'
} else {
    Note "no C compiler, so testing without the race detector (CI still runs it)"
}
if ($Quick) {
    $testArgs += '-short'
    Note "skipping the database tests (-Quick)"
}
$testArgs += './...'

Step "Tests ($($testArgs[1..($testArgs.Count - 2)] -join ' '))" { Invoke-Native go @testArgs }

# ---- fuzzing (only when asked) ----

# Go fuzzes one target at a time, so each gets its own run. (-short: no fuzz
# target needs the database, so don't set it up.) Anything the
# fuzzer finds is saved under that package's testdata/fuzz folder, and from
# then on runs as an ordinary test case.
if ($Fuzz) {
    $targets = @(
        @('FuzzParseHash',        './internal/auth'),
        @('FuzzPasswordProblems', './internal/auth'),
        @('FuzzSafeBack',         './internal/web'),
        @('FuzzParseCursor',      './internal/store'),
        @('FuzzRender',           './internal/markdown')
    )
    foreach ($t in $targets) {
        $target, $pkg = $t
        Step "Fuzz $target (${FuzzSeconds}s)" {
            Invoke-Native go test -short -run '^$' -fuzz "^$target`$" -fuzztime "${FuzzSeconds}s" $pkg
        }
    }
}

Write-Host ("`nAll checks passed ({0:N0}s)." -f $total.Elapsed.TotalSeconds) -ForegroundColor Green
Finish 0
