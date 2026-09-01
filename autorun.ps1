<#
==============================================================================
SWITCHER SUITE WIZARD - ENTERPRISE SELF-HEALING MULTI-OS BUILD WRAPPER (POWERSHELL)

Usage:
  .\autorun.ps1           Interactive Multi-OS selector, build, and elevated launch
  .\autorun.ps1 -f        Force rebuild: clean build/test cache + remove old EXE
  .\autorun.ps1 --hard    Full Go cache purge and fresh clean rebuild
  .\autorun.ps1 --help    Display help screen
==============================================================================
#>

[CmdletBinding()]
param (
    [Parameter(Position = 0)]
    [string]$Mode = ""
)

# ------------------------------------------------------------------------------
# 0. Global Terminal Encoding & Buffer Stabilization (Fixes Art Truncation)
# ------------------------------------------------------------------------------
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
[Console]::InputEncoding  = [System.Text.Encoding]::UTF8
$OutputEncoding           = [System.Text.Encoding]::UTF8

try {
    if ($Host.Name -eq "ConsoleHost") {
        $rawUI = $Host.UI.RawUI
        $winSize = $rawUI.WindowSize
        if ($winSize.Width -lt 110 -or $winSize.Height -lt 45) {
            $winSize.Width = [Math]::Min(110, $rawUI.MaxPhysicalWindowSize.Width)
            $winSize.Height = [Math]::Min(45, $rawUI.MaxPhysicalWindowSize.Height)
            $rawUI.WindowSize = $winSize
        }
    }
} catch {
    # Non-fatal when running under ConPTY or redirected pipes
}
# ------------------------------------------------------------------------------
# Core Environment Paths and Configuration
# ------------------------------------------------------------------------------
$ProjectDir         = Split-Path -Parent $MyInvocation.MyCommand.Path
$LocalGoDir         = Join-Path $ProjectDir ".go_runtime"
$GoBootstrapVersion = "1.24.0"
$GoZipUrl           = "https://go.dev/dl/go$GoBootstrapVersion.windows-amd64.zip"

Set-Location -Path $ProjectDir

# ANSI Terminal Color Definitions
$ESC    = [char]27
$RED    = "$ESC[0;31m"
$GREEN  = "$ESC[0;32m"
$YELLOW = "$ESC[1;33m"
$CYAN   = "$ESC[0;36m"
$BOLD   = "$ESC[1m"
$DIM    = "$ESC[2m"
$NC     = "$ESC[0m"

# Global Target Defaults
$global:TargetOS   = "windows"
$global:TargetArch = "amd64"
$global:BinaryName = "Switcher_Suite_Wizard.exe"
$global:GoBin      = $null
$global:GofmtBin   = $null

# ------------------------------------------------------------------------------
# Argument Pre-Check: Help
# ------------------------------------------------------------------------------
function Show-HelpScreen {
    Clear-Host
    Write-Host ""
    Write-Host "${BOLD}Usage:${NC}"
    Write-Host "  .\autorun.ps1            Interactive Multi-OS selector, build, and launch"
    Write-Host "  .\autorun.ps1 -f         Force rebuild with build/test cache cleanup"
    Write-Host "  .\autorun.ps1 --hard     Full Go cache purge and fresh rebuild"
    Write-Host "  .\autorun.ps1 --help     Display this help screen"
    Write-Host ""
    Write-Host "${BOLD}Features:${NC}"
    Write-Host "  => Multi-OS Target Wrapper: Interactive platform target selection."
    Write-Host "  => High-Speed Extraction: Uses native tar/curl engines for instant setup."
    Write-Host "  => Self-Healing Go Runtime: Auto-provisions portable toolchain if missing."
    Write-Host "  => Offline Resilient: Compiles directly via vendor directory if offline."
    Write-Host "  => Native Elevation: Automatically handles Windows Administrator rights."
    Write-Host "  => Zero-Latency Global CLI: 'switcher-wizard' directly accesses native binary."
    Write-Host ""
    Read-Host "Press ENTER to continue..."
    exit 0
}

if ($Mode -in @("-h", "--help", "help", "-?")) {
    Show-HelpScreen
}

# ------------------------------------------------------------------------------
# Step 1: Interactive Multi-OS Target Selection Wrapper with Favorite Art
# ------------------------------------------------------------------------------
function Display-WrapperMenu {
    Clear-Host
    $bannerPath = Join-Path $ProjectDir "banner_wrapper.txt"
    if (Test-Path $bannerPath) {
        Get-Content -LiteralPath $bannerPath | Write-Host -ForegroundColor Cyan
    } else {
        Write-Host "${YELLOW}[!]${NC} banner_wrapper.txt not found - skipping ASCII art."
    }
    Write-Host ""

    Write-Host "${CYAN}+============================================================================+${NC}"
    Write-Host "${CYAN}|${NC} ${BOLD}TARGET OPERATING SYSTEM SELECTION WRAPPER${NC}                                  ${CYAN}|${NC}"
    Write-Host "${CYAN}+============================================================================+${NC}"
    Write-Host "${CYAN}|${NC} [1] => ${GREEN}Linux (Debian / Ubuntu / Kali / RHEL / CentOS / Arch)${NC}               ${CYAN}|${NC}"
    Write-Host "${CYAN}|${NC} [2] => ${GREEN}Microsoft Windows (x64 / Win10 / Win11 / Server)${NC}                    ${CYAN}|${NC}"
    Write-Host "${CYAN}|${NC} [3] => ${GREEN}Apple macOS (Darwin Subsystem / amd64 & arm64)${NC}                      ${CYAN}|${NC}"
    Write-Host "${CYAN}|${NC} [4] => ${YELLOW}Auto-Detect Host Platform & Launch Immediately${NC}                      ${CYAN}|${NC}"
    Write-Host "${CYAN}|${NC} [Q] => Exit Build System                                                   ${CYAN}|${NC}"
    Write-Host "${CYAN}+============================================================================+${NC}"
    
    $osChoice = Read-Host " Select Target Operating System [1-4 / Q]"

    switch ($osChoice.ToUpper().Trim()) {
        "1" {
            $global:TargetOS   = "linux"
            $global:TargetArch = "amd64"
            $global:BinaryName = "Switcher_Suite_Wizard"
        }
        "2" {
            $global:TargetOS   = "windows"
            $global:TargetArch = "amd64"
            $global:BinaryName = "Switcher_Suite_Wizard.exe"
        }
        "3" {
            $global:TargetOS   = "darwin"
            $global:TargetArch = "amd64"
            $global:BinaryName = "Switcher_Suite_Wizard_macOS"
        }
        "4" {
            $global:TargetOS   = "windows"
            $global:TargetArch = "amd64"
            $global:BinaryName = "Switcher_Suite_Wizard.exe"
        }
        "Q" {
            Write-Host "`n${YELLOW}=> Operation cancelled by user.${NC}"
            exit 0
        }
        Default {
            Write-Host "`n${RED}[!] Invalid selection. Defaulting to Windows.${NC}"
            $global:TargetOS   = "windows"
            $global:TargetArch = "amd64"
            $global:BinaryName = "Switcher_Suite_Wizard.exe"
        }
    }
}

Display-WrapperMenu

# ------------------------------------------------------------------------------
# Step 2: Self-Healing Go Toolchain Discovery & High-Speed Auto-Provisioning
# ------------------------------------------------------------------------------
Write-Host "`n${BOLD}${CYAN}--- CHECKING GO TOOLCHAIN ---${NC}"

# Check 1: System PATH
$sysGo = Get-Command go -ErrorAction SilentlyContinue
if ($sysGo) {
    $global:GoBin    = "go"
    $global:GofmtBin = "gofmt"
    $versionStr = (& go version)
    Write-Host "${GREEN}[OK]${NC} System Go compiler found: $versionStr"
}
# Check 2: Standard Program Files
elseif (Test-Path "C:\Program Files\Go\bin\go.exe") {
    $env:GOROOT = "C:\Program Files\Go"
    $env:PATH   = "C:\Program Files\Go\bin;" + $env:PATH
    $global:GoBin    = "C:\Program Files\Go\bin\go.exe"
    $global:GofmtBin = "C:\Program Files\Go\bin\gofmt.exe"
    $versionStr = (& $global:GoBin version)
    Write-Host "${GREEN}[OK]${NC} Program Files Go compiler detected: $versionStr"
}
# Check 3: Standard Root Path
elseif (Test-Path "C:\Go\bin\go.exe") {
    $env:GOROOT = "C:\Go"
    $env:PATH   = "C:\Go\bin;" + $env:PATH
    $global:GoBin    = "C:\Go\bin\go.exe"
    $global:GofmtBin = "C:\Go\bin\gofmt.exe"
    $versionStr = (& $global:GoBin version)
    Write-Host "${GREEN}[OK]${NC} C:\Go compiler detected: $versionStr"
}
# Check 4: Local Portable Runtime Sandbox
elseif (Test-Path "$LocalGoDir\bin\go.exe") {
    $env:GOROOT = $LocalGoDir
    $env:PATH   = "$LocalGoDir\bin;" + $env:PATH
    $global:GoBin    = "$LocalGoDir\bin\go.exe"
    $global:GofmtBin = "$LocalGoDir\bin\gofmt.exe"
    $versionStr = (& $global:GoBin version)
    Write-Host "${GREEN}[OK]${NC} Portable Go runtime active: $versionStr"
}
# Fallback: Automated Rapid Toolchain Download and Ultra-Fast Unpacking
else {
    Write-Host "${YELLOW}[!]${NC} No Go compiler detected in PATH or system folders."
    Write-Host "${CYAN}[+]${NC} Auto-provisioning Go $GoBootstrapVersion portable toolchain..."

    $cacheDir = Join-Path $ProjectDir ".cache"
    if (-not (Test-Path $cacheDir)) {
        New-Item -ItemType Directory -Path $cacheDir | Out-Null
    }
    $zipDest = Join-Path $cacheDir "go_temp.zip"

    $hasCurl = Get-Command curl.exe -ErrorAction SilentlyContinue
    if ($hasCurl) {
        Write-Host "${CYAN}[+]${NC} Downloading archive with native high-speed curl..."
        & curl.exe -L --progress-bar "$GoZipUrl" -o "$zipDest"
    } else {
        Write-Host "${CYAN}[+]${NC} Downloading archive via PowerShell .NET stream..."
        [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
        $wc = New-Object System.Net.WebClient
        $wc.DownloadFile($GoZipUrl, $zipDest)
    }

    if (-not (Test-Path $zipDest)) {
        Write-Host "`n${RED}[X]${NC} Failed to download Go toolchain."
        Write-Host "${YELLOW}[!]${NC} Check internet connectivity or install Go manually from https://go.dev/dl/"
        Read-Host "`nPress ENTER to exit..."
        exit 1
    }

    Write-Host "${CYAN}[+]${NC} Fast-extracting portable Go compiler..."

    if (Test-Path $LocalGoDir) {
        Remove-Item -Recurse -Force $LocalGoDir
    }

    $hasTar = Get-Command tar.exe -ErrorAction SilentlyContinue
    if ($hasTar) {
        & tar.exe -xf "$zipDest" -C "$cacheDir"
        Move-Item -Path "$cacheDir\go" -Destination "$LocalGoDir" -Force
    } else {
        Add-Type -AssemblyName System.IO.Compression.FileSystem
        [System.IO.Compression.ZipFile]::ExtractToDirectory($zipDest, $cacheDir)
        Move-Item -Path "$cacheDir\go" -Destination "$LocalGoDir" -Force
    }

    Remove-Item -Recurse -Force $cacheDir -ErrorAction SilentlyContinue

    if (-not (Test-Path "$LocalGoDir\bin\go.exe")) {
        Write-Host "`n${RED}[X]${NC} Extraction failed or archive was corrupt."
        Read-Host "`nPress ENTER to exit..."
        exit 1
    }

    $env:GOROOT = $LocalGoDir
    $env:PATH   = "$LocalGoDir\bin;" + $env:PATH
    $global:GoBin    = "$LocalGoDir\bin\go.exe"
    $global:GofmtBin = "$LocalGoDir\bin\gofmt.exe"

    $versionStr = (& $global:GoBin version)
    Write-Host "${GREEN}[OK]${NC} Self-healing complete. Provisioned: $versionStr"
}

# Verify module integrity
if (-not (Test-Path "go.mod")) {
    Write-Host "${YELLOW}[!]${NC} go.mod not found. Initializing module 'switcher'..."
    & $global:GoBin mod init switcher 2>&1 | Out-Null
}
Write-Host "${GREEN}[OK]${NC} Go module integrity confirmed."

# ------------------------------------------------------------------------------
# Helper: Terminate Running Instance to Prevent File Locks
# ------------------------------------------------------------------------------
function Stop-RunningInstance {
    param([string]$name)
    $procName = [System.IO.Path]::GetFileNameWithoutExtension($name)
    $running = Get-Process -Name $procName -ErrorAction SilentlyContinue
    if ($running) {
        Write-Host "${YELLOW}[!]${NC} Terminating running instance of $name..."
        $running | Stop-Process -Force
        Start-Sleep -Seconds 1
    }
}

# ------------------------------------------------------------------------------
# Step 3: Build Mode Cache Router
# ------------------------------------------------------------------------------
if ($Mode -eq "") {
    Write-Host "`n${BOLD}${CYAN}--- NORMAL BUILD MODE ---${NC}"
    Write-Host "${CYAN}[+]${NC} Utilizing compiler caches for rapid execution."
}
elseif ($Mode -in @("-f", "--force")) {
    Write-Host "`n${BOLD}${CYAN}--- FORCE REBUILD MODE ---${NC}"
    Write-Host "${YELLOW}[!]${NC} Purging previous binaries and cleaning build/test cache..."

    Stop-RunningInstance -name $global:BinaryName

    if (Test-Path $global:BinaryName) {
        Remove-Item -Force $global:BinaryName -ErrorAction SilentlyContinue
        if (Test-Path $global:BinaryName) {
            Write-Host "${RED}[X]${NC} Could not remove $global:BinaryName. File is in use."
            Read-Host "Press ENTER to exit..."
            exit 1
        }
    }

    & $global:GoBin clean -cache -testcache
    Write-Host "${GREEN}[OK]${NC} Force cache purge complete."
}
elseif ($Mode -eq "--hard") {
    Write-Host "`n${BOLD}${CYAN}--- HARD PURGE MODE ---${NC}"
    Write-Host "${YELLOW}[!]${NC} Executing full cache, test, and fuzz purge..."

    Stop-RunningInstance -name $global:BinaryName

    if (Test-Path $global:BinaryName) {
        Remove-Item -Force $global:BinaryName -ErrorAction SilentlyContinue
        if (Test-Path $global:BinaryName) {
            Write-Host "${RED}[X]${NC} Could not delete $global:BinaryName."
            Read-Host "Press ENTER to exit..."
            exit 1
        }
    }

    Write-Host "${CYAN}[+]${NC} Cleaning Go build cache..."
    & $global:GoBin clean -cache
    Write-Host "${CYAN}[+]${NC} Cleaning Go test cache..."
    & $global:GoBin clean -testcache
    Write-Host "${CYAN}[+]${NC} Cleaning Go fuzz cache..."
    & $global:GoBin clean -fuzzcache
    Write-Host "${GREEN}[OK]${NC} Hard purge completed cleanly."
}
else {
    Write-Host "`n${RED}[X]${NC} Unknown build parameter: $Mode"
    Show-HelpScreen
}

# ------------------------------------------------------------------------------
# Step 4: Shared Build & Validation Pipeline
# ------------------------------------------------------------------------------
Write-Host "`n${BOLD}${CYAN}--- FORMATTING GO SOURCE ---${NC}"
Write-Host "${CYAN}[+]${NC} Running gofmt on project files..."

$goFiles = Get-ChildItem -Filter *.go
foreach ($file in $goFiles) {
    & $global:GofmtBin -w $file.FullName
    if ($LASTEXITCODE -ne 0) {
        Write-Host "${RED}[X]${NC} gofmt error in $($file.Name)."
        Read-Host "Press ENTER to exit..."
        exit 1
    }
}
Write-Host "${GREEN}[OK]${NC} Source code formatted successfully."

Write-Host "`n${BOLD}${CYAN}--- SYNCING GO DEPENDENCIES ---${NC}"

$BuildVendorFlag = @()
if (Test-Path "vendor") {
    Write-Host "${GREEN}[OK]${NC} Local vendor folder detected. Activating 100% OFFLINE build mode."
    $BuildVendorFlag = @("-mod=vendor")
} else {
    Write-Host "${CYAN}[+]${NC} Synchronizing module dependencies with go mod tidy..."
    & $global:GoBin mod tidy 2>&1 | Out-Null
    if ($LASTEXITCODE -ne 0) {
        Write-Host "${YELLOW}[!]${NC} Remote synchronization offline. Continuing with cached modules..."
    } else {
        Write-Host "${GREEN}[OK]${NC} Dependencies verified and synchronized."
    }
}

Write-Host "`n${BOLD}${CYAN}--- RUNNING STATIC ANALYSIS ---${NC}"
Write-Host "${CYAN}[+]${NC} Running go vet..."

if ($BuildVendorFlag.Count -gt 0) {
    & $global:GoBin vet $BuildVendorFlag ./...
} else {
    & $global:GoBin vet ./...
}

if ($LASTEXITCODE -ne 0) {
    Write-Host "`n${RED}[X]${NC} Static analysis detected errors."
    Write-Host "${YELLOW}[!]${NC} Please fix the issues identified above before compiling."
    Read-Host "`nPress ENTER to exit..."
    exit 1
}
Write-Host "${GREEN}[OK]${NC} Static analysis passed without issues."

Write-Host "`n${BOLD}${CYAN}--- RUNNING UNIT TESTS ---${NC}"
$testFiles = Get-ChildItem -Filter "*_test.go"
if ($testFiles.Count -gt 0) {
    Write-Host "${CYAN}[+]${NC} Running test suites..."
    if ($BuildVendorFlag.Count -gt 0) {
        & $global:GoBin test $BuildVendorFlag ./...
    } else {
        & $global:GoBin test ./...
    }
    if ($LASTEXITCODE -ne 0) {
        Write-Host "`n${RED}[X]${NC} Test suites failed."
        Read-Host "`nPress ENTER to exit..."
        exit 1
    }
    Write-Host "${GREEN}[OK]${NC} All tests passed successfully."
} else {
    Write-Host "${YELLOW}[!]${NC} No unit test files found. Skipping testing phase."
}

Write-Host "`n${BOLD}${CYAN}--- COMPILING TARGET ENGINE: $($global:TargetOS)/$($global:TargetArch) ---${NC}"
Write-Host "${CYAN}[+]${NC} Building $($global:BinaryName) (Optimized Release Build)..."

$env:GOOS = $global:TargetOS
$env:GOARCH = $global:TargetArch
$env:CGO_ENABLED = "0"

if ($BuildVendorFlag.Count -gt 0) {
    & $global:GoBin build $BuildVendorFlag -trimpath -ldflags="-s -w" -o $global:BinaryName .
} else {
    & $global:GoBin build -trimpath -ldflags="-s -w" -o $global:BinaryName .
}

if ($LASTEXITCODE -ne 0 -or -not (Test-Path $global:BinaryName)) {
    Write-Host "`n${RED}[X]${NC} Compilation failed."
    Write-Host "${YELLOW}[!]${NC} Check the compiler output above for details."
    Read-Host "`nPress ENTER to exit..."
    exit 1
}

Write-Host "${GREEN}[OK]${NC} Build successful!"
Write-Host "${GREEN}[OK]${NC} Target Binary: $ProjectDir\$($global:BinaryName)"

# ------------------------------------------------------------------------------
# Step 5: Execution / Cross-Compilation Handoff
# ------------------------------------------------------------------------------
if ($global:TargetOS -eq "windows") {
    Write-Host "`n${BOLD}${CYAN}--- LAUNCHING SWITCHER SUITE ---${NC}"
    Write-Host "${DIM}[i] Elevating permissions to ensure system adapter access...${NC}`n"

    Start-Sleep -Milliseconds 500

    $isAdmin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
    
    if ($isAdmin) {
        & "$ProjectDir\$($global:BinaryName)"
        $appExitCode = $LASTEXITCODE
    } else {
        try {
            $proc = Start-Process -FilePath "$ProjectDir\$($global:BinaryName)" -Verb RunAs -PassThru -Wait
            $appExitCode = $proc.ExitCode
            Write-Host "${GREEN}[OK]${NC} Elevated session launched."
        } catch {
            Write-Host "`n${RED}[!]${NC} UAC elevation prompt was cancelled or denied."
            Write-Host "${YELLOW}[*]${NC} Please run PowerShell as Administrator and run autorun.ps1 again.`n"
            Read-Host "Press ENTER to exit..."
            exit 1
        }
    }

    Write-Host ""
    if ($appExitCode -eq 0) {
        Write-Host "${GREEN}[OK]${NC} Switcher Suite session terminated normally."
    } else {
        Write-Host "${YELLOW}[!]${NC} Switcher Suite exited with code $appExitCode."
    }

    Write-Host ""
    Read-Host "Press ENTER to exit..."
    exit $appExitCode
} else {
    Write-Host "`n${BOLD}${GREEN}[OK] CROSS-COMPILATION COMPLETE!${NC}"
    Write-Host "${YELLOW}[i] Standalone executable package generated for '$($global:TargetOS)'.${NC}"
    Write-Host "${YELLOW}[i] Ready to transfer and execute at: $ProjectDir\$($global:BinaryName)${NC}`n"
    Read-Host "Press ENTER to exit..."
    exit 0
}