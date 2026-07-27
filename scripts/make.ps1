<#
    ArticleFlux task runner — TODO 1.4.

    This is `make.ps1` and not a `Makefile` for one reason: **there is no `make` on
    this box.** Verified 2026-07-26 (`command -v make` → missing), and per the
    Windows-native testing rule there is no WSL or Docker in the loop to borrow one
    from. Shipping a Makefile nobody here can run, plus a script everyone actually
    runs, is two build systems where one of them is a lie.

    The verb set is exactly the one TODO 1.4 specifies, so the names stay portable
    if a Makefile ever becomes worth having:

        gen build test wasm run lint migrate

    plus `tools` (build gwc.exe), `dev` (build + serve, the loop Cam watches) and
    `clean`.

    Usage:  ./scripts/make.ps1 <verb> [-Port 9000]

    It lives in scripts/ but every path it touches is the REPOSITORY root, which
    is $PSScriptRoot's parent — see $Root below. Nothing in here may use
    $PSScriptRoot directly: that is scripts/, and the difference is silent. A
    path built from the wrong one produces scripts/bin/articleflux.exe, which is a
    build that succeeds into a directory nobody serves from.
#>

[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateSet('gen', 'build', 'test', 'wasmtest', 'wasm', 'demo', 'run', 'dev', 'e2e', 'lint', 'migrate', 'perf', 'tools', 'clean', 'help')]
    [string]$Target = 'help',

    # 9000 is the port Cam watches to follow along; changing the default moves the
    # thing he has open in a tab.
    [int]$Port = 9000,

    # What a `demo` build calls itself. The release workflow passes the tag it
    # was triggered by; a build from a laptop is honestly "dev".
    [string]$Version = 'dev',

    # `perf` writes bin/perf/<Label>.txt. The default is the word every
    # comparison needs one of, and naming the other side is the entire workflow:
    #
    #   ./scripts/make.ps1 perf                     -> bin/perf/baseline.txt
    #   ...make a change...
    #   ./scripts/make.ps1 perf -Label after        -> bin/perf/after.txt
    #   ./scripts/make.ps1 perf -Compare            -> benchstat baseline after
    [string]$Label = 'baseline',

    # Which benchmarks to run, as a `go test -bench` regexp. The default is
    # everything; narrowing it is how a single hot path gets iterated on without
    # paying for the 25-second store fixture every time.
    [string]$Bench = '.',

    # How many times to run each benchmark. Statistics, not decoration — see
    # Invoke-Perf for why 6 and not 1.
    [int]$Count = 6,

    # Compare two previous runs instead of measuring a new one.
    [switch]$Compare,

    # The two labels -Compare reads. Defaults to the two names the workflow
    # above produces.
    [string]$From = 'baseline',
    [string]$To = 'after'
)

$ErrorActionPreference = 'Stop'

# The repository root, one level up from scripts/. Every path below hangs off
# this and never off $PSScriptRoot, and the working directory is set to it so
# that `go build ./...`, `buf lint` and the guards resolve the way they do when
# a person runs them by hand.
$Root = Split-Path $PSScriptRoot -Parent
Set-Location $Root

$GWC       = Join-Path $Root 'bin\gwc.exe'
$GWCSource = Join-Path (Split-Path $Root -Parent) 'GoWebComponents'
$ClientApp = Join-Path $Root 'client\app'
$WebSrc    = Join-Path $Root 'web'
$OutDir    = Join-Path $Root 'bin\web'
$WasmOut   = Join-Path $OutDir 'app.wasm'
$DemoDir   = Join-Path $Root 'bin\demo'

function Step($msg) { Write-Host "==> $msg" -ForegroundColor Cyan }
function Fail($msg) { Write-Host "!!! $msg" -ForegroundColor Red; exit 1 }

# Native commands set $LASTEXITCODE; PowerShell will not stop on it by itself.
function Invoke-Checked {
    param([string]$What, [scriptblock]$Body)
    & $Body
    if ($LASTEXITCODE -ne 0) { Fail "$What failed (exit $LASTEXITCODE)" }
}

function Ensure-Tools {
    if (-not (Test-Path $GWC)) {
        Step 'gwc.exe missing — building it from the GWC checkout'
        Invoke-Tools
    }
}

function Invoke-Tools {
    Step "building gwc.exe from $GWCSource"
    if (-not (Test-Path $GWCSource)) {
        Fail "GWC checkout not found at $GWCSource. D0: v5.0.0 was never tagged, so the sibling checkout is not optional."
    }
    Push-Location $GWCSource
    try { Invoke-Checked 'go build (gwc)' { go build -o $GWC ./tools/gwc } }
    finally { Pop-Location }
    Write-Host "    $GWC" -ForegroundColor DarkGray
}

function Invoke-Gen {
    Step 'buf generate'
    Invoke-Checked 'buf lint'     { buf lint }
    Invoke-Checked 'buf generate' { buf generate }
}

function Invoke-Build {
    Step 'go build ./...'
    Invoke-Checked 'go build' { go build -o bin\articleflux.exe ./cmd/articleflux }
    Invoke-Checked 'go vet'   { go build ./... }
    # `go build ./...` does NOT compile the client (8b.32). The wasm packages sit
    # behind `//go:build js`, so a native build skips them entirely — during one
    # batch the wasm build was broken for a stretch while the native build and
    # every Go test stayed green. Catching it here is catching it before CI.
    Invoke-Checked 'go build (wasm client)' {
        $env:GOOS = 'js'; $env:GOARCH = 'wasm'
        try { go build ./client/... }
        finally { Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue }
    }
}

function Invoke-Test {
    Step 'go test ./...'
    Invoke-Checked 'go test' { go test ./... }
}

# wasmtest is the ONLY way client/view ever gets tested: it is entirely
# `//go:build js && wasm` (zero untagged files), so `go test .\client\view`
# under a native build fails outright with "build constraints exclude all Go
# files". Cross-compiling the test binary to wasm and running it under Node
# (via `go test -exec`) is what client/i18n/provider_test.go already proves
# works; this is that same harness, made a verb instead of a remembered
# incantation.
#
# GOROOT and the node wrapper are resolved at run time rather than hardcoded,
# so this works on a fresh checkout without anyone hunting for
# wasm_exec_node.js under whatever GOROOT this machine's Go toolchain manager
# happens to use.
#
# Scoped to client/i18n and client/view rather than ./client/..., for the
# same reason the Makefile's mirror of this verb is: those are the two
# packages the harness exists for, and the rest of client/... is either
# native-portable (client/design) or untested, so running it through -exec
# would only add noise.
#
# -skip drops two client/i18n tests that walk a directory
# (TestEveryReferencedKeyExists, TestNoOrphanedKeys, plus srvkeys_test.go's
# two) because Go's js/wasm syscall layer refuses O_DIRECTORY ON WINDOWS —
# this machine. That is a gap in the toolchain on this platform, not a
# broken test: ci.yml's wasmtest job runs the identical suite WITHOUT this
# skip, on Linux, where the syscall is supported, so nothing here goes
# permanently unchecked — it is checked on the platform that can check it.
function Invoke-WasmTest {
    Step 'go test (wasm, via node) -> client/i18n + client/view'
    $goroot = (go env GOROOT)
    if (-not $goroot) { Fail 'go env GOROOT returned nothing' }
    $execJs = Join-Path $goroot 'lib\wasm\wasm_exec_node.js'
    if (-not (Test-Path $execJs)) {
        Fail "wasm_exec_node.js not found under $goroot\lib\wasm - is this Go's Node exec shim missing?"
    }
    if (-not (Get-Command node -ErrorAction SilentlyContinue)) {
        Fail 'node is not on PATH — the wasm test harness runs the compiled test binary under it'
    }
    Invoke-Checked 'go test (wasm)' {
        $env:GOOS = 'js'; $env:GOARCH = 'wasm'
        try {
            go test -exec="node $execJs" `
                -skip 'TestEveryReferencedKeyExists|TestNoOrphanedKeys|TestEveryServerErrorKeyExists|TestServerErrorKeysMatchTheirEnglishFallback' `
                ./client/i18n/... ./client/view/...
        } finally { Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue }
    }
}

# perf — measure, profile, and compare (plan.md M13's "perf pass").
#
# # Why a verb and not a remembered incantation
#
# The incantation is `go test -run '^$' -bench . -benchmem -count 6 -cpuprofile
# ...`, and every part of it is load-bearing in a way that is easy to get wrong
# once and then keep getting wrong:
#
#   -run '^$'    without it every TEST runs too, and internal/store's tests
#                build a 50,000-row fixture, so the numbers arrive twenty-five
#                seconds late and mixed with test output.
#   -benchmem    allocation is half the answer. A change that is 3% faster and
#                allocates 40% less is usually the better change, and a run
#                without this cannot see the second half.
#   -count 6     THIS BOX IS FANLESS. It throttles under sustained load, so two
#                consecutive single-shot runs of the same unchanged code differ
#                by 20-30% — which is larger than most real wins. One sample is
#                not a measurement here; it is a coin flip that looks like a
#                number. Six samples and a median (benchstat's job) is the
#                cheapest thing that is honest.
#   -cpuprofile  `go test` refuses it across multiple packages, which is why
#                this loops per package rather than running ./... once.
#
# # Run it on a QUIET machine, and do not believe a run that was not
#
# This is not general advice, it is a recorded failure. The first full capture
# was taken while `go test` was running in another shell, and it is unusable:
# `search common term` came back between 251ms and 512ms against the 77ms the
# same code measures in isolation, and `get item` spread eleven-fold across six
# samples of one unchanged query. -Count 6 does not rescue that, because thermal
# drift and contention are not random noise — they push one direction for as
# long as they last, so the samples agree with each other and are all wrong
# together.
#
# The tell is in the file: the B/op and allocs/op columns are IDENTICAL across
# samples while ns/op swings by a factor of ten. Allocation does not care how hot
# the machine is. When those two columns are steady and the timings are not, the
# timings are measuring the room.
#
# # Why benchstat rather than reading the two files
#
# Because eyeballing "594µs vs 633µs" is how a 6% thermal drift gets recorded as
# a regression and a real 6% win gets called noise. benchstat computes the
# median and a confidence interval and prints `~` when the difference does not
# clear it, which is exactly the judgement a person should not be making by
# hand on this machine.
function Invoke-Perf {
    $outDir  = Join-Path $Root 'bin\perf'
    $profDir = Join-Path $outDir 'prof'
    New-Item -ItemType Directory -Force $profDir | Out-Null
    $outFile = Join-Path $outDir "$Label.txt"

    # Packages that actually have benchmarks. Running `go test -bench` against
    # the rest costs a compile and a link each to print nothing.
    Step 'finding packages with benchmarks'
    $pkgs = @()
    foreach ($line in (go list -f '{{.ImportPath}}|{{.Dir}}' ./...)) {
        $parts = $line -split '\|', 2
        if ($parts.Count -ne 2) { continue }
        $hits = Get-ChildItem -Path $parts[1] -Filter '*_test.go' -File -ErrorAction SilentlyContinue |
                Select-String -Pattern '^func Benchmark' -List
        if ($hits) { $pkgs += [pscustomobject]@{ Path = $parts[0]; Dir = $parts[1] } }
    }
    if (-not $pkgs) { Fail 'no package in this module declares a Benchmark function' }
    Write-Host ("    " + ($pkgs.Path -join "`n    "))

    # Truncated rather than appended: a label names ONE run, and a file holding
    # two of them silently doubles every sample count benchstat sees.
    Set-Content -Path $outFile -Value '' -Encoding utf8 -NoNewline

    foreach ($pkg in $pkgs) {
        $short = ($pkg.Path -split '/')[-1]
        Step "bench $($pkg.Path)"
        $cpu = Join-Path $profDir "$short.cpu.pprof"
        $mem = Join-Path $profDir "$short.mem.pprof"
        # NOT Invoke-Checked: a package whose benchmark fails should not throw
        # away the packages already measured into this file. The exit code is
        # reported and the loop continues.
        $text = & go test $pkg.Path -run '^$' -bench $Bench -benchmem -count $Count `
                    -cpuprofile $cpu -memprofile $mem -o (Join-Path $profDir "$short.test.exe") 2>&1 |
                Out-String
        if ($LASTEXITCODE -ne 0) { Write-Host "!!! $($pkg.Path) exited $LASTEXITCODE" -ForegroundColor Yellow }
        Write-Host $text
        Add-Content -Path $outFile -Value $text -Encoding utf8
    }

    Write-Host "==> wrote $outFile" -ForegroundColor Green
    Write-Host "    profiles in $profDir — read one with:" -ForegroundColor DarkGray
    Write-Host "      go tool pprof -top -nodecount=25 $profDir\PKG.test.exe $profDir\PKG.cpu.pprof" -ForegroundColor DarkGray
    Write-Host "    compare against another run with:" -ForegroundColor DarkGray
    Write-Host "      ./scripts/make.ps1 perf -Compare -From $Label -To OTHER" -ForegroundColor DarkGray

    # The verdict on the run that just finished, LAST so it is the thing still on
    # screen when the run ends — a warning printed above forty lines of benchmark
    # output is a warning nobody reads. See internal/tools/benchspread for why a
    # capture from this box needs one at all.
    Write-Host ''
    & go run ./internal/tools/benchspread $outFile
}

function Invoke-PerfCompare {
    $outDir = Join-Path $Root 'bin\perf'
    $a = Join-Path $outDir "$From.txt"
    $b = Join-Path $outDir "$To.txt"
    foreach ($f in @($a, $b)) {
        if (-not (Test-Path $f)) {
            Fail "$f does not exist. Measure it first: ./scripts/make.ps1 perf -Label $([IO.Path]::GetFileNameWithoutExtension($f))"
        }
    }
    if (-not (Get-Command benchstat -ErrorAction SilentlyContinue)) {
        Fail 'benchstat is not on PATH. go install golang.org/x/perf/cmd/benchstat@latest'
    }
    Step "benchstat $From -> $To"
    & benchstat "$From=$a" "$To=$b"
}

function Invoke-Wasm {
    Ensure-Tools
    if (-not (Test-Path $ClientApp)) {
        Write-Host '    client/app has no main yet (Tier 8) — skipping wasm' -ForegroundColor DarkGray
        return
    }
    Step 'building the web root -> bin/web'
    New-Item -ItemType Directory -Force $OutDir | Out-Null

    # web/ is source; bin/web is the assembled output. Copying rather than
    # serving from web/ keeps every generated byte inside the one ignored
    # directory, so "is this file source?" is answerable from its path.
    Copy-Item (Join-Path $WebSrc 'index.html') (Join-Path $OutDir 'index.html') -Force
    # index.html registers sw.js by relative path, so a build that does not ship
    # it 404s on every load and the offline shell silently never exists (8.4).
    Copy-Item (Join-Path $WebSrc 'sw.js') (Join-Path $OutDir 'sw.js') -Force

    # The self-hosted webfonts. index.html links fonts.css by relative path and
    # fonts.css links each woff2 the same way, so missing either leaves the app
    # rendering in Georgia and system-ui with nothing in the console to say why.
    Copy-Item (Join-Path $WebSrc 'fonts.css') (Join-Path $OutDir 'fonts.css') -Force
    $fontsOut = Join-Path $OutDir 'fonts'
    if (Test-Path $fontsOut) { Remove-Item $fontsOut -Recurse -Force }
    Copy-Item (Join-Path $WebSrc 'fonts') $fontsOut -Recurse -Force

    # `go build` directly rather than `gwc build`: the launcher wraps the same
    # toolchain, and this keeps the exact flags visible — -trimpath and -s -w are
    # what the G5 baseline in wasm-baseline.txt was measured with, so building
    # differently would make the CI ratchet compare two different things.
    $env:GOOS = 'js'; $env:GOARCH = 'wasm'
    try {
        Invoke-Checked 'go build (wasm)' {
            go build -trimpath -ldflags="-s -w" -o $WasmOut ./client/app
        }
    } finally { Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue }

    # wasm_exec.js comes from the toolchain, so it must match the compiler that
    # produced the module. A stale copy from an older Go fails at instantiate
    # with an import mismatch that reads like a corrupt binary.
    $exec = Join-Path (go env GOROOT) 'lib\wasm\wasm_exec.js'
    if (-not (Test-Path $exec)) { $exec = Join-Path (go env GOROOT) 'misc\wasm\wasm_exec.js' }
    Copy-Item $exec (Join-Path $OutDir 'wasm_exec.js') -Force

    # Precompressed siblings. G5 measured 23.8 MB raw against 5.2 MB gzipped;
    # the server prefers the .gz when the client accepts it.
    #
    # Written to a temp file and MOVED into place, never compressed in situ. The
    # server prefers app.wasm.gz over app.wasm, so a .gz that is stale — or
    # truncated, which is what File::Create leaves behind when the compressor
    # throws — means the browser silently runs the previous build. That failure
    # looks exactly like "my change did nothing", and it costs an hour every time.
    #
    # CompressionLevel::Optimal, not SmallestSize: SmallestSize is .NET 5+, and on
    # Windows PowerShell 5.1 it resolves to $null, which surfaces as an
    # unrelated-looking "cannot find an overload for GZipStream".
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    foreach ($f in @($WasmOut, (Join-Path $OutDir 'wasm_exec.js'))) {
        $tmp = "$f.gz.tmp"
        $in  = [System.IO.File]::OpenRead($f)
        $out = [System.IO.File]::Create($tmp)
        $gz  = New-Object System.IO.Compression.GZipStream($out, [System.IO.Compression.CompressionLevel]::Optimal)
        try { $in.CopyTo($gz) } finally { $gz.Dispose(); $out.Dispose(); $in.Dispose() }
        Move-Item $tmp "$f.gz" -Force
    }

    # G5 / R4: the wasm size decides whether A5 (offline packs in the browser)
    # stays affordable. Print it every build so the number is never a surprise.
    $raw = [math]::Round((Get-Item $WasmOut).Length / 1MB, 1)
    $gzs = [math]::Round((Get-Item "$WasmOut.gz").Length / 1MB, 1)
    Write-Host "    app.wasm = $raw MB raw / $gzs MB gzipped  (G5 ratchet - plan.md R4)" -ForegroundColor DarkGray
}

# The GitHub Pages demo (client/demo + client/demodata), built exactly the way
# .github/workflows/pages.yml builds it.
#
# "Exactly" is the point of having this verb at all: the deployed demo is the
# only build of this application that strangers see, and a local one that
# differed from it — by shipping the uncompressed module, say — would be a
# rehearsal of a different performance. So the output directory here holds the
# same three files the workflow uploads, and nothing else.
#
# The version is a parameter for the same reason it is a linker flag in CI:
# a build that identifies itself as something it is not is worse than one that
# says "dev".
function Invoke-Demo {
    param([string]$DemoVersion = 'dev')

    Step "building the demo -> bin/demo  ($DemoVersion)"
    New-Item -ItemType Directory -Force $DemoDir | Out-Null
    # index.html, with the module name STAMPED — the same idea as the sw.js
    # stamp below, and for a failure that is just as invisible locally. This
    # build publishes ONLY app.wasm.gz, so an unstamped loader asks for
    # app.wasm first and takes a 404 in the console of every visitor, on every
    # load, for a file it deliberately does not ship.
    $indexSrc = Get-Content (Join-Path $WebSrc 'index.html') -Raw
    $stamped = $indexSrc -replace "const MODULE = 'app\.wasm';", "const MODULE = 'app.wasm.gz';"
    if ($stamped -notmatch "const MODULE = 'app\.wasm\.gz';") {
        # ASCII only inside these strings. This file has no BOM, Windows
        # PowerShell reads it as ANSI, and a UTF-8 em dash decodes to a CURLY
        # QUOTE - which PowerShell accepts as a string delimiter, ends the
        # string early, and swallows the brace below into the next one. The
        # error it produces names a function forty lines away.
        Fail "web/index.html has no ""const MODULE = 'app.wasm';"" line to stamp - the demo would 404 on every boot"
    }
    # UTF8 without a BOM: a BOM before <!doctype is bytes the browser has to
    # sniff past, and Set-Content's default encoding on Windows PowerShell is
    # the ANSI codepage, which would mangle every em dash in the file.
    [System.IO.File]::WriteAllText((Join-Path $DemoDir 'index.html'), $stamped,
        (New-Object System.Text.UTF8Encoding $false))

    # sw.js, with its cache identity STAMPED — the one file the demo does not
    # ship verbatim, and the reason is a failure that is invisible for weeks.
    #
    # The Service Worker keys its cache on VERSION and serves the wasm module
    # cache-first, because within a build that URL's contents never change. On
    # the server that is exactly right: VERSION is buildver.Version, a new
    # release changes the constant, and `activate` drops every older cache.
    #
    # The demo is published from a TAG, and a tag does not change buildver. So a
    # second demo release under an unchanged constant would leave every
    # returning visitor on the module their browser cached the first time —
    # publishing an update that nobody who has already looked can see, which is
    # the worst possible version of a demo being wrong. Stamping the copy makes
    # each published demo its own cache generation.
    #
    # web/sw.js itself is untouched, so internal/buildver's test still pins the
    # source file to the constant.
    # One line, and it has to be: PowerShell does not continue a statement after
    # a binary operator, so breaking after `-replace` is a parse error in a file
    # that is otherwise never parsed until somebody runs this verb.
    $swPath = Join-Path $DemoDir 'sw.js'
    $swStamp = "const VERSION = '$DemoVersion';"
    $sw = (Get-Content (Join-Path $WebSrc 'sw.js') -Raw) -replace "(?m)^const VERSION = .*", $swStamp
    # WriteAllText, not Set-Content: Set-Content defaults to the system ANSI
    # codepage, and this file has em dashes in it.
    [System.IO.File]::WriteAllText($swPath, $sw)
    if ($sw -notmatch [regex]::Escape($swStamp)) {
        Fail "sw.js has no 'const VERSION = ...' line to stamp - the demo would ship a stale cache key"
    }

    $raw = Join-Path $DemoDir 'app.wasm'
    $env:GOOS = 'js'; $env:GOARCH = 'wasm'
    try {
        Invoke-Checked 'go build (demo wasm)' {
            go build -trimpath -ldflags="-s -w -X main.version=$DemoVersion" -o $raw ./client/demo
        }
    } finally { Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue }

    # wasm_exec.js must come from the toolchain that produced the module.
    $exec = Join-Path (go env GOROOT) 'lib\wasm\wasm_exec.js'
    if (-not (Test-Path $exec)) { $exec = Join-Path (go env GOROOT) 'misc\wasm\wasm_exec.js' }
    Copy-Item $exec (Join-Path $DemoDir 'wasm_exec.js') -Force

    # Compressed, and then the raw module is DELETED — which is the one place
    # this differs from `wasm`, and it is deliberate. A static host cannot
    # negotiate an encoding, so the boot shim fetches app.wasm.gz and
    # decompresses it itself (see web/index.html); leaving app.wasm next to it
    # would mean the shim never took that path locally and the first time anyone
    # exercised it was on the public demo.
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $tmp = "$raw.gz.tmp"
    $in  = [System.IO.File]::OpenRead($raw)
    $out = [System.IO.File]::Create($tmp)
    $gz  = New-Object System.IO.Compression.GZipStream($out, [System.IO.Compression.CompressionLevel]::Optimal)
    try { $in.CopyTo($gz) } finally { $gz.Dispose(); $out.Dispose(); $in.Dispose() }
    Move-Item $tmp "$raw.gz" -Force
    $rawMB = [math]::Round((Get-Item $raw).Length / 1MB, 1)
    $gzMB  = [math]::Round((Get-Item "$raw.gz").Length / 1MB, 1)
    Remove-Item $raw -Force

    Write-Host "    demo.wasm = $rawMB MB raw / $gzMB MB shipped (gzipped)" -ForegroundColor DarkGray
    Write-Host "    serve bin/demo with any static file server to look at it" -ForegroundColor DarkGray
}

function Invoke-Lint {
    Step 'go vet'
    Invoke-Checked 'go vet' { go vet ./... }
    Step 'buf lint'
    Invoke-Checked 'buf lint' { buf lint }
    Step 'structural guards (A26 + tenancy)'
    Invoke-Checked 'guards' { go run ./internal/tools/guards }
}

function Invoke-Migrate {
    Ensure-Build
    Step 'articleflux migrate'
    Invoke-Checked 'migrate' { & (Join-Path $Root 'bin\articleflux.exe') migrate }
}

function Ensure-Build { if (-not (Test-Path 'bin\articleflux.exe')) { Invoke-Build } }

function Invoke-Run {
    Invoke-Build
    Step "serving on http://127.0.0.1:$Port"
    & (Join-Path $Root 'bin\articleflux.exe') serve -addr "127.0.0.1:$Port"
}

switch ($Target) {
    'tools'   { Invoke-Tools }
    'gen'     { Invoke-Gen }
    'build'   { Invoke-Build }
    'test'    { Invoke-Test }
    'wasmtest' { Invoke-WasmTest }
    'wasm'    { Invoke-Wasm }
    'demo'    { Invoke-Demo -DemoVersion $Version }
    'lint'    { Invoke-Lint }
    'migrate' { Invoke-Migrate }
    'perf'    { if ($Compare) { Invoke-PerfCompare } else { Invoke-Perf } }
    'run'     { Invoke-Run }
    'dev'     { Invoke-Wasm; Invoke-Run }
    'e2e'     {
        Invoke-Build
        Invoke-Wasm
        Step 'playwright'
        Push-Location (Join-Path $Root 'e2e')
        try {
            if (-not (Test-Path 'node_modules')) { Invoke-Checked 'npm install' { npm install } }
            Invoke-Checked 'playwright' { npx playwright test }
        } finally { Pop-Location }
    }
    'clean'   {
        Step 'clean'
        # One directory, because everything generated lives in it.
        if (Test-Path 'bin') { Remove-Item -Recurse -Force 'bin' }
    }
    default {
        Write-Host @'
ArticleFlux task runner (TODO 1.4)

  ./scripts/make.ps1 tools      build gwc.exe from ../GoWebComponents  (D0: no published tag)
  ./scripts/make.ps1 gen        buf lint + buf generate -> internal/pb
  ./scripts/make.ps1 build      go build -> bin/articleflux.exe
  ./scripts/make.ps1 test       go test ./...
  ./scripts/make.ps1 wasmtest   go test client/i18n + client/view under GOOS=js GOARCH=wasm (via node)
  ./scripts/make.ps1 wasm       build the client into bin/web, prints the G5 size
  ./scripts/make.ps1 demo       build the GitHub Pages demo into bin/demo (-Version v1.0.0)
  ./scripts/make.ps1 lint       go vet + buf lint + the A26/tenancy structural guards
  ./scripts/make.ps1 perf       benchmark every package that has one, with CPU/heap profiles
                                -Label baseline   name this run (bin/perf/<label>.txt)
                                -Bench Hot        only benchmarks matching this regexp
                                -Count 6          samples per benchmark (this box throttles)
                                -Compare -From baseline -To after    benchstat two runs
                                Prints a verdict on whether the run's timings can be trusted.
  ./scripts/make.ps1 migrate    apply migrations
  ./scripts/make.ps1 run        build, then serve on :9000
  ./scripts/make.ps1 dev        wasm + run          <- the loop to leave open
  ./scripts/make.ps1 e2e        build, then run the Playwright suite (desktop + phone)
  ./scripts/make.ps1 clean      remove bin/ (which is all generated output)

Run it from anywhere: it sets the working directory to the repository root
itself, so ./scripts/make.ps1 and scripts/make.ps1 behave identically.

Override the port with -Port; 9000 is the default because that is the tab Cam
has open.
'@
    }
}
